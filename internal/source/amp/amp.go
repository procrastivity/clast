// Package amp is the amp source (M12): Amp threads fetched read-only
// through the amp CLI's own subprocess surface. Transport, enumeration,
// completeness, identity, and storage decisions are the settled
// amp-source step-03 contract, not re-derived here.
//
// The storage model is network (M13): threads live behind Amp's servers,
// so there is no storage root on this machine — Discover enumerates the
// ambient credential's visible universe through windowed `threads
// search`, Capture writes the verbatim `threads export` bytes, and the
// source declares source.Network, which keeps it out of bare capture
// sweeps until capture.amp.auto opts in (the shared capture-policy
// contract).
//
// The CLI's own auth is used ambiently, exactly as the harness
// configured it: clast never injects credentials into the subprocess,
// never reads stdin, and never initiates a login flow. Every invocation
// is duration- and byte-bounded (callBudget); a credential failure
// surfaces as a real error, never a prompt and never a quiet empty
// result.
package amp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// Name is the registry key and M11 identity prefix.
const Name = "amp"

// TranscriptFormat is what session.json's transcript.format records for
// this source's copies (M13): one verbatim `threads export` document.
const TranscriptFormat = "amp-export"

// TranscriptFile is the artifact name Capture places the export under in
// the session directory — the fileless-source naming the contract
// settles (session.json's transcript.artifact field is the journal-side
// half of that decision and lands with registration).
const TranscriptFile = "transcript.json"

// binName is the CLI the source shells out to, resolved on PATH.
const binName = "amp"

// historyFloor is day one of an initial full enumeration (contract
// step-03 §3): it predates Amp's public existence, so an ascending
// windowed scan from here covers every thread the account can see.
var historyFloor = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// threadURL is the canonical web URL for a thread id — Discovered.Path
// and Diagnostic.Path carry it (provenance/display; it is never opened
// as a file).
func threadURL(id string) string { return "https://ampcode.com/threads/" + id }

// Source implements source.Source over the amp CLI subprocess transport.
// Every host-facing seam is injectable so tests run without a real amp,
// network, or credential material.
type Source struct {
	// bin is the amp executable — an explicit path, or "" to resolve
	// binName on PATH at call time (claude's lazy-root pattern: the
	// registry row's static New can never fail; only a duty call can).
	bin string
	// dataDir is amp's state dir (device-id.json, credential material);
	// "" means $XDG_DATA_HOME/amp or ~/.local/share/amp at call time.
	dataDir string
	// cacheDir parents the source-owned scan-state checkpoint; "" means
	// $XDG_CACHE_HOME/clast/amp or ~/.cache/clast/amp at call time.
	cacheDir string

	// run is the subprocess seam; nil means the real exec path. A fake
	// sees the already-timeout-wrapped context and returns raw
	// stdout/stderr/exit code — classification happens in cli(), so a
	// fake exercises the same post-exit behavior as a real process.
	run runFunc
	// now is the clock for enumeration's upper bound; nil → time.Now.
	now func() time.Time
	// sleep backs the capture retry waits; nil → a ctx-aware timer.
	sleep func(ctx context.Context, d time.Duration) error
	// hostname supplies this machine's name for the correlate veto;
	// nil → os.Hostname.
	hostname func() (string, error)

	// Per-invocation budgets (contract step-03 §1): 30s for the cheap
	// enumeration reads, 120s for export (observed ~1s + ~0.3s/MB; a
	// 26.7 MB real export took ~8s). Byte caps bound memory on large or
	// hostile output: 8 MiB covers a 100-row search page or 500-row
	// list page a hundred times over; 64 MiB is ~2.4x the largest
	// observed real export.
	searchBudget callBudget
	listBudget   callBudget
	exportBudget callBudget

	mu         sync.Mutex
	memo       memoExport     // one-entry Correlate→Capture export memo
	listCounts map[string]int // threads list corroboration: id → messageCount
	installID  string         // cached local installationID ("" = none)
	installOK  bool           // the installID probe has run
	invoked    bool           // the binary resolved at least once this run

	// verdicts/quarantined are this run's views of the checkpoint's two
	// settle caches (scanstate.go): the Correlate verdict per
	// (id, revision, host) and the over-cap export set per (id, cap).
	// Both load lazily from the scope's state file and are keyed to the
	// scope descriptor they were read under — a rescope reloads.
	verdicts      map[string]corrVerdict
	verdictsScope string
	quarantined   map[string]int64
	quarScope     string

	// journalRoot is the capture run's journal target, bound once per
	// run through the OPTIONAL source.JournalScope seam — scan progress
	// is keyed to it so a different journal can never reuse this
	// target's checkpoint. "" (tests, or a caller that never binds)
	// scopes to the journal:unbound marker — same sharing posture as an
	// unkeyed file, no worse than the pre-scope behavior.
	journalRoot string
	// scopeDesc/scopeAuth/scopeOK cache the run's scope descriptor —
	// computed once like the install id (account evidence cannot
	// meaningfully change inside one run).
	scopeDesc string
	scopeAuth bool
	scopeOK   bool
	// beforeRMW, when set, runs inside updateScanState after the state
	// path resolves but before the locks — the seam that lets a test
	// park a foreign write between a scan's hint read and the
	// checkpoint's locked write.
	beforeRMW func()
}

// memoExport is the Correlate→Capture in-memory memo (contract step-03
// §9): the export fetched for correlation is reused by the adjacent
// Capture when both duties see the same enumerated revision, so a thread
// is not exported twice in one run. It is deliberately ONE entry — the
// only caller correlates then captures each item adjacently, and a
// bigger cache is unbounded memory for tens-of-MB documents.
type memoExport struct {
	id      string
	modTime time.Time
	raw     []byte
	doc     *ExportDoc
}

// New returns the amp source with all-lazy resolution: the binary is
// LookPath'd and the data dir resolved per call, so the constructor can
// sit in a static registry row.
func New() *Source { return newSource("", "") }

// NewAt returns the amp source bound to explicit paths — the fixture
// seam. bin "" still resolves binName on PATH; dataDir "" still resolves
// the platform default state dir.
func NewAt(bin, dataDir string) *Source { return newSource(bin, dataDir) }

func newSource(bin, dataDir string) *Source {
	return &Source{
		bin:      bin,
		dataDir:  dataDir,
		now:      time.Now,
		hostname: os.Hostname,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
		searchBudget: callBudget{timeout: 30 * time.Second, maxBytes: 8 << 20},
		listBudget:   callBudget{timeout: 30 * time.Second, maxBytes: 8 << 20},
		exportBudget: callBudget{timeout: 120 * time.Second, maxBytes: 64 << 20},
	}
}

// Name implements source.Source.
func (s *Source) Name() string { return Name }

// Model implements source.Source: amp is network-resident (M13) — this
// declaration alone keeps the source out of bare sweeps until
// capture.amp.auto opts in.
func (s *Source) Model() source.StorageModel { return source.Network }

// Present implements source.Presence: "present" for a network source
// means the CLI is resolvable AND credential material exists — the amp
// binary on PATH plus a non-empty AMP_API_KEY or any of the harness's
// stored-credential files under the state dir. A pure local probe: no
// fetch, no login check (Discover produces the real verdict on the
// explicit path). The error names what was probed, verbatim, for
// capture.source-unavailable.
func (s *Source) Present(_ context.Context) error {
	bin := s.bin
	if bin == "" {
		bin = binName
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("amp: resolving %q: %w", bin, err)
	}
	if os.Getenv("AMP_API_KEY") != "" {
		return nil
	}
	dir := s.stateDir()
	for _, name := range []string{"secrets.json", "accounts.json", "oauth"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("amp: %s found but no credential material: AMP_API_KEY is unset and none of %s exist",
		bin, credentialProbes(dir))
}

// credentialProbes renders the paths Present probes for its error text.
func credentialProbes(dir string) string {
	return filepath.Join(dir, "secrets.json") + ", " +
		filepath.Join(dir, "accounts.json") + ", or " +
		filepath.Join(dir, "oauth") + "/"
}

// stateDir resolves amp's state dir: the injected path, else
// $XDG_DATA_HOME/amp, else ~/.local/share/amp (the XDG default — the
// contract names the default location).
func (s *Source) stateDir() string {
	if s.dataDir != "" {
		return s.dataDir
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "amp")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "amp")
	}
	return filepath.Join("~", ".local", "share", "amp") // best-effort label for error text
}

// localInstallID is this install's uuid from <stateDir>/device-id.json —
// read once per run and cached in-process (contract step-03 §4). A
// missing or unreadable file means "no local install": every thread is
// foreign — projectless but capturable.
func (s *Source) localInstallID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.installOK {
		s.installOK = true
		data, err := os.ReadFile(filepath.Join(s.stateDir(), "device-id.json"))
		if err == nil {
			var v struct {
				InstallationID string `json:"installationID"`
			}
			if json.Unmarshal(data, &v) == nil {
				s.installID = v.InstallationID
			}
		}
	}
	return s.installID
}

// binaryResolved records that the amp binary resolved for an invocation
// — the marker that lets Discover distinguish "transport absent" (never
// resolved → quiet nil,nil,nil) from a binary that vanished mid-run
// (real error).
func (s *Source) binaryResolved() {
	s.mu.Lock()
	s.invoked = true
	s.mu.Unlock()
}

// binarySeen reports whether any invocation this run resolved the
// binary.
func (s *Source) binarySeen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invoked
}

// ScopeJournal implements the OPTIONAL source.JournalScope seam: the
// capture verb hands the run's resolved journal root in before
// Discover, so the scan-state checkpoint keys to the target it
// progresses against. Rebinding rescopes — the next read or write uses
// the new target's file. The path is canonicalized best-effort so
// aliases of one target (symlinked homes, nonexistent-yet roots) share
// progress rather than forking it.
func (s *Source) ScopeJournal(root string) {
	if p, err := filepath.Abs(root); err == nil {
		root = p
	}
	if p, err := filepath.EvalSymlinks(root); err == nil {
		root = p
	}
	root = filepath.Clean(root)
	s.mu.Lock()
	if s.journalRoot != root {
		s.journalRoot = root
		s.scopeOK = false // the descriptor caches the journal token — recompute
	}
	s.mu.Unlock()
}

// clock is the enumeration clock, defaulting to time.Now.
func (s *Source) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// host is this machine's hostname for the correlate veto, defaulting to
// os.Hostname.
func (s *Source) host() (string, error) {
	if s.hostname != nil {
		return s.hostname()
	}
	return os.Hostname()
}

// wait sleeps through the retry delay, defaulting to a ctx-aware timer.
func (s *Source) wait(ctx context.Context, d time.Duration) error {
	if s.sleep != nil {
		return s.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Compile-time interface checks: the three-duty Source plus the optional
// seams — the Presence probe the explicit --harness path consults, the
// Unchanged recapture verdict that keeps a URL-pathed source's freshness
// check fetch-free, the JournalScope binding that keys scan progress to
// the run's journal target, and the transcript renderer/reader the
// offline verbs resolve by the stored transcript.format.
var (
	_ source.Source             = (*Source)(nil)
	_ source.Presence           = (*Source)(nil)
	_ source.Unchanged          = (*Source)(nil)
	_ source.JournalScope       = (*Source)(nil)
	_ source.Supersede          = (*Source)(nil)
	_ source.TranscriptRenderer = (*Source)(nil)
	_ source.TranscriptReader   = (*Source)(nil)
)
