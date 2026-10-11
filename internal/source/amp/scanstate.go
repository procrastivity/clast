package amp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// pendingCap bounds the pending id set — the contract's "bounded
// retryIDs" (step-03 §3). Reaching it resets the whole checkpoint: an
// account whose live set cannot fit the lane degrades to the initial
// full rescan — correct if slower, and the file can never grow without
// bound. (A var so tests can shrink it.)
var pendingCap = 8192

// The checkpoint's phase provenance: which enumeration shape produced
// the state. Provenance only — the next scan's shape derives from
// CleanThrough as always.
const (
	// phaseInitial marks a checkpoint written by a scan that ran from
	// historyFloor — the initial bounded-history enumeration.
	phaseInitial = "initial"
	// phaseCatchup marks one written by a scan continuing from a
	// committed clean floor.
	phaseCatchup = "catchup"
)

// scanState is the source-owned catch-up checkpoint under
// $XDG_CACHE_HOME/clast/amp/ — one file PER SCOPE, named
// scan-state-<scope>.json where <scope> is a hash of the scope
// descriptor (see scopeDescriptor). A CACHE, never truth (contract
// step-03 §3): the journal is. A missing, corrupt, or scope-mismatched
// file is the initial full rescan; the file only ever makes a sweep
// cheaper, never more correct.
//
// The scope pins whose progress the file records — the ambient
// credential's account/server, the local install, and the journal
// target. Another account's pending set is never probed (its id: lane
// would drain against the wrong universe), another journal's floor is
// never trusted, and a scope switch is non-destructive: switching back
// finds this scope's file exactly as it was left.
//
// CleanThrough is the clean-window floor: enumeration below it
// completed without diagnostics, so a catch-up scan re-covers only
// [day(CleanThrough)-1d, tomorrow) — the day-overlap rescan covers the
// boundary the DSL's day granularity cannot express mid-day — plus a
// >CleanThrough post-filter on rows (MinTime). The floor may only
// advance over coverage whose discoveries are committed or recoverably
// queued: every emitted id lands in PendingIDs (queued until proven
// settled), a diagnostic-affected window holds the floor at its start,
// and a corroboration-missed id joins PendingIDs rather than crossing
// the floor unqueued.
//
// PendingIDs is the emitted-but-unconfirmed set — every id Discover
// emitted that no later observation has yet proven settled. It subsumes
// the contract's retryIDs lane (a failed Capture's id stays pending)
// and covers the crash window the verb's commit order leaves: artifacts
// land before session.json, so a crash between them leaves an orphan
// the walk only diagnoses — the still-pending id re-surfaces via the
// id: lane next sweep and is captured fresh. Confirmation drains it,
// and it has exactly three witnesses:
//
//   - Unchanged(...) == true: the journal's own read-back already holds
//     this revision — the strongest commit witness there is;
//   - a clean empty id: page: the thread left the account's universe;
//   - a terminal does-not-exist/invalid-id export verdict: ditto.
//
// A successful Capture deliberately does NOT drain — its artifact is
// merely staged at that point; session.json may never commit.
//
// Two more fields ride the same file — both settle caches, both pure
// cache data the scan can always afford to lose:
//
//   - Correlate records a correlation verdict per thread id — the dir
//     one export's env.initial evidence produced — keyed to the
//     enumerated revision that answered it and the host that computed
//     it. "" is a verdict (projectless-by-evidence), not absence: the
//     entry exists so an unchanged revision never pays a second export
//     just to re-answer "no project". An emitted revision with no
//     recorded verdict, or a different host stamp, fetches as before.
//   - Quarantined records ids whose export provably exceeds the byte
//     budget, id → the cap it failed under. Exports only grow, so a cap
//     failure at a budget the same or smaller can never succeed — the
//     id settles out of the lane instead of spending a bounded retry
//     every sweep. A run with a raised budget reopens one retry.
type scanState struct {
	// Scope is the descriptor the file was keyed under — verified on
	// load so a copied or hash-colliding file can never lend its
	// progress to another scope.
	Scope string `json:"scope"`
	// Phase records the producing scan's enumeration shape — initial
	// (from historyFloor) or catchup (from a committed floor).
	Phase        string                 `json:"phase,omitempty"`
	CleanThrough time.Time              `json:"clean_through"`
	PendingIDs   []string               `json:"pending_ids,omitempty"`
	Correlate    map[string]corrVerdict `json:"correlate,omitempty"`
	Quarantined  map[string]int64       `json:"quarantined,omitempty"`
}

// corrVerdict is one recorded correlation answer: the enumerated
// revision it answered, the hostname that computed it (the verdict is a
// LOCAL path — nonsense on another machine), and the working dir it
// resolved to ("" = projectless evidence, still a verdict).
type corrVerdict struct {
	Rev  string `json:"rev"`
	Host string `json:"host"`
	Dir  string `json:"dir"`
}

// verdictCap bounds the Correlate map inside the checkpoint — same
// posture as pendingCap: the file is a bounded cache, so past the bound
// the oldest-evidence behavior degrades to simply refetching (entries
// are never evicted one-by-one; the pendingCap reset drops the whole
// map).
const verdictCap = 8192

// scanStatePath resolves this run's checkpoint: the injected cacheDir
// seam when set, else $XDG_CACHE_HOME/clast/amp/, else ~/.cache/
// clast/amp/, holding one scan-state-<scope>.json per scope. "" when no
// home resolves — a source with nowhere to park the cache simply
// rescans every run; the file is a cache, so its absence degrades
// gracefully and is never an error.
func (s *Source) scanStatePath() string {
	dir := s.cacheDir
	if dir == "" {
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			base = filepath.Join(home, ".cache")
		}
		dir = filepath.Join(base, "clast", "amp")
	}
	desc, _ := s.scopeDescriptor()
	sum := sha256.Sum256([]byte(desc))
	return filepath.Join(dir, "scan-state-"+hex.EncodeToString(sum[:8])+".json")
}

// scopeDescriptor renders the identity this checkpoint is keyed to and
// reports whether any account evidence was readable (attributed). Three
// axes — the workplan's scope triple:
//
//   - the Amp server/account: the ambient credential's own universe,
//     evidenced without touching secret material (see accountEvidence);
//   - the source scope: this install's device-id.json installationID —
//     the same identity Correlate matches against, so an install that
//     changes (reinstall) starts over rather than inherit progress;
//   - the journal target: the run's bound journal root
//     (source.JournalScope), so a sweep pointed at a different journal
//     never reuses this one's checkpoint.
//
// The descriptor is non-secret by construction: account tokens carry
// server URL + user id (the same identifier exports record as
// creatorUserID), credential files and AMP_API_KEY contribute only a
// digest, and the journal contributes its path. It is computed once per
// run — account evidence cannot meaningfully change mid-run — and
// cached like the install id.
func (s *Source) scopeDescriptor() (desc string, attributed bool) {
	s.mu.Lock()
	if s.scopeOK {
		desc, attributed = s.scopeDesc, s.scopeAuth
		s.mu.Unlock()
		return desc, attributed
	}
	s.mu.Unlock()

	// Evidence reads happen off the lock — localInstallID locks itself.
	var tokens []string
	if acct := s.accountEvidence(); len(acct) > 0 {
		tokens = append(tokens, acct...)
		attributed = true
	}
	if id := s.localInstallID(); id != "" {
		tokens = append(tokens, "install:"+id)
	}
	s.mu.Lock()
	if s.journalRoot != "" {
		tokens = append(tokens, "journal:"+s.journalRoot)
	} else {
		tokens = append(tokens, "journal:unbound")
	}
	s.mu.Unlock()
	sort.Strings(tokens)

	desc = strings.Join(tokens, "|")
	s.mu.Lock()
	s.scopeDesc, s.scopeAuth, s.scopeOK = desc, attributed, true
	s.mu.Unlock()
	return desc, attributed
}

// accountEvidence returns the account/server tokens for the scope — the
// discriminator the "owned scope = ambient credential's universe"
// contract needs, each tier non-secret:
//
//   - accounts.json's active map — server URL → user id, the in-effect
//     account per server and the same non-secret identifier exports
//     carry as creatorUserID;
//   - a digest of AMP_API_KEY when set — env-auth installs carry no
//     accounts.json; the digest discriminates keys without storing one
//     (a high-entropy secret's sha256 is safe to cache and changing the
//     key rescopes — an account switch can never share the floor);
//   - else a stat fingerprint of the credential files Present probes
//     (secrets.json, oauth/) — re-login rewrites them, so an account
//     switch still rescopes; a mere token refresh rescans too, the
//     conservative price of never reading secret bytes;
//   - none of the above → nil: the scope is unattributed — no evidence
//     distinguishes this account from another, so the floor must never
//     persist for it (a stored floor could hide another account's
//     history). Pending bookkeeping still records; every run rescans
//     history. Conservative, per the contract's never-lose-data rule.
func (s *Source) accountEvidence() []string {
	var tokens []string
	if data, err := os.ReadFile(filepath.Join(s.stateDir(), "accounts.json")); err == nil {
		var af struct {
			Active map[string]string `json:"active"`
		}
		if json.Unmarshal(data, &af) == nil {
			for server, uid := range af.Active {
				if uid != "" {
					tokens = append(tokens, "acct:"+server+"="+uid)
				}
			}
		}
	}
	if key := os.Getenv("AMP_API_KEY"); key != "" {
		sum := sha256.Sum256([]byte("amp-api-key\x00" + key))
		tokens = append(tokens, "envkey:"+hex.EncodeToString(sum[:8]))
	}
	if len(tokens) > 0 {
		sort.Strings(tokens)
		return tokens
	}
	// No account id readable — fall back to a stat fingerprint of the
	// credential material: (name, size, mtime) of secrets.json and the
	// oauth/ entries. Never the bytes — the fingerprint discriminates
	// without reading secrets.
	var lines []string
	dir := s.stateDir()
	for _, name := range []string{"secrets.json", "oauth"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d:%d", name, info.Size(), info.ModTime().UnixNano()))
		if !info.IsDir() {
			continue
		}
		if entries, err := os.ReadDir(filepath.Join(dir, name)); err == nil {
			for _, e := range entries {
				if ei, err := e.Info(); err == nil {
					lines = append(lines, fmt.Sprintf("%s/%s:%d:%d", name, e.Name(), ei.Size(), ei.ModTime().UnixNano()))
				}
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return []string{"creds:" + hex.EncodeToString(sum[:8])}
}

// loadScanState reads this scope's checkpoint. Any failure — no cache
// dir, absent, unreadable, malformed, or a Scope field that is not this
// run's descriptor (a copied or colliding file) — is the zero state,
// which is the initial full rescan: conservative by construction, per
// the contract's "missing or corrupted state triggers a safe re-scan"
// rule. Read unlocked: the file is only ever replaced atomically, and
// the authoritative mutation re-reads under the lock anyway.
func (s *Source) loadScanState() scanState {
	desc, _ := s.scopeDescriptor()
	return loadScanStateFile(s.scanStatePath(), desc)
}

func loadScanStateFile(path, scope string) scanState {
	var st scanState
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return scanState{}
	}
	if st.Scope != scope {
		return scanState{} // another scope's checkpoint — never its progress
	}
	return st
}

// saveScanState writes st atomically (temp + rename in the same
// directory). Best-effort: the file is a cache, so every write failure
// is swallowed into "next run rescans" rather than surfaced.
func saveScanStateFile(path string, st scanState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".scan-state-*")
	if err != nil {
		return
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// The cross-process checkpoint lock's parameters. A holder's critical
// section is a read-modify-write of one small file — microseconds — so
// a lock older than scanLockStaleAge can only be a dead run's, and one
// surviving the whole scanLockWait budget is stuck: both are removed
// and retaken. Vars so tests can shrink them.
var (
	scanLockStaleAge = 30 * time.Second
	scanLockWait     = 2 * time.Second
	scanLockPoll     = 15 * time.Millisecond
)

// acquireScanLock takes the checkpoint file's advisory cross-process
// lock — <path>.lock created O_CREATE|O_EXCL, the portable atomic — and
// returns the release func. It exists to serialize read-modify-write
// between concurrent `clast capture` runs: without it, two interleaved
// writers can erase each other's pending work. Stale and stuck locks
// are stolen (no live RMW legitimately survives the wait budget), and a
// lock that cannot be taken at all degrades to an unlocked write — the
// file is a cache; its lock may never fail or hang a sweep.
func acquireScanLock(path string) func() {
	noop := func() {}
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return noop
	}
	deadline := time.Now().Add(scanLockWait)
	steals := 0
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			_ = f.Close()
			return func() { _ = os.Remove(lock) }
		}
		if !os.IsExist(err) {
			return noop // unwritable dir — the state write fails identically; proceed best-effort
		}
		fi, serr := os.Stat(lock)
		switch {
		case serr != nil:
			continue // raced a release — retry the create
		case time.Since(fi.ModTime()) > scanLockStaleAge:
			if os.Remove(lock) != nil {
				return noop
			}
		case time.Now().After(deadline) && steals < 3:
			// Past the wait budget on a live-looking lock — a real
			// RMW is microseconds, so this holder is stuck. Steal,
			// bounded: a constant ping-pong of steals degrades to an
			// unlocked write rather than an unbounded spin.
			if os.Remove(lock) != nil {
				return noop
			}
			steals++
		case time.Now().After(deadline):
			return noop // waited, stole, still contended — degrade to unlocked
		default:
			time.Sleep(scanLockPoll)
		}
	}
}

// updateScanState applies fn to this scope's checkpoint under both
// exclusion layers — the in-process mutex and the state file's
// cross-process lock — persisting only when fn reports a mutation. It
// is the single read-modify-write site: the locked region re-reads the
// file so a mutation always applies to the freshest committed state,
// never to a stale hint. The pending-cap check is uniform: any mutation
// that overflows the bound resets the checkpoint, forcing the
// contract's full rescan.
func (s *Source) updateScanState(fn func(*scanState) bool) {
	desc, _ := s.scopeDescriptor()
	path := s.scanStatePath()
	if path == "" {
		return
	}
	if s.beforeRMW != nil {
		s.beforeRMW() // test seam — a foreign write can land here
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release := acquireScanLock(path)
	defer release()
	st := loadScanStateFile(path, desc)
	if !fn(&st) {
		return
	}
	st.Scope = desc
	if len(st.PendingIDs) > pendingCap {
		st = scanState{Scope: desc, Phase: phaseInitial} // overflow forces a full rescan
	}
	saveScanStateFile(path, st)
}

// settlePending records id as confirmed-settled — it leaves the retry
// lane. The witnesses are the ones the scanState comment lists: an
// Unchanged verdict that says the journal already holds this revision,
// a clean empty id: page, or a terminal gone-verdict export.
func (s *Source) settlePending(id string) {
	s.updateScanState(func(st *scanState) bool {
		if !slices.Contains(st.PendingIDs, id) {
			return false
		}
		st.PendingIDs = slices.DeleteFunc(st.PendingIDs, func(x string) bool { return x == id })
		return true
	})
}

// notePending ensures id sits in the retry lane — a failed Capture's
// own append (the contract's "Capture appends failed ids" rule).
// Normally redundant — an emitted id is already pending — but the cache
// file can be lost mid-run, and re-adding costs nothing against the
// retry guarantee it keeps real.
func (s *Source) notePending(id string) {
	s.updateScanState(func(st *scanState) bool {
		if slices.Contains(st.PendingIDs, id) {
			return false
		}
		st.PendingIDs = append(st.PendingIDs, id)
		sort.Strings(st.PendingIDs)
		return true
	})
}

// correlateVerdicts ensures this run's view of the checkpoint's
// recorded Correlate verdicts is loaded — once per scope from the
// state file, keyed to the descriptor it was read under so a rescope
// reloads rather than answering from another target's cache. Callers
// read or write the returned view under s.mu only.
func (s *Source) correlateVerdicts() {
	desc, _ := s.scopeDescriptor()
	s.mu.Lock()
	if s.verdicts != nil && s.verdictsScope == desc {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	st := loadScanStateFile(s.scanStatePath(), desc)
	s.mu.Lock()
	s.verdicts = st.Correlate
	if s.verdicts == nil {
		s.verdicts = map[string]corrVerdict{}
	}
	s.verdictsScope = desc
	s.mu.Unlock()
}

// cachedVerdict is the verdict cache's read side: the recorded answer
// for id, present only when it was computed against the same enumerated
// revision on this host.
func (s *Source) cachedVerdict(id, rev, host string) (corrVerdict, bool) {
	s.correlateVerdicts()
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.verdicts[id]
	if !ok || v.Rev != rev || v.Host != host {
		return corrVerdict{}, false
	}
	return v, true
}

// recordVerdict persists one Correlate answer — the dir the fetched
// export's evidence produced for this enumerated revision on this host
// — into the checkpoint's verdict map and this run's view of it. ""
// records too: "projectless by evidence" is a verdict, and caching it
// is what keeps an unchanged projectless thread from paying an export
// every sweep. Best-effort bookkeeping — a lost write just refetches.
func (s *Source) recordVerdict(id, rev, host, dir string) {
	v := corrVerdict{Rev: rev, Host: host, Dir: dir}
	s.mu.Lock()
	if s.verdicts != nil {
		s.verdicts[id] = v
	}
	s.mu.Unlock()
	s.updateScanState(func(st *scanState) bool {
		if st.Correlate == nil {
			st.Correlate = map[string]corrVerdict{}
		}
		if cur, ok := st.Correlate[id]; ok && cur == v {
			return false
		}
		if len(st.Correlate) >= verdictCap {
			return false // bounded cache — an overflowed map just refetches
		}
		st.Correlate[id] = v
		return true
	})
}

// exportQuarantined reports whether id's export is recorded over the
// byte cap under a budget the current one cannot beat — the settled
// "this fetch can never succeed" verdict. A raised budget (recorded
// cap below this run's) reopens the fetch for one retry.
func (s *Source) exportQuarantined(id string) bool {
	desc, _ := s.scopeDescriptor()
	s.mu.Lock()
	if s.quarantined == nil || s.quarScope != desc {
		s.mu.Unlock()
		st := loadScanStateFile(s.scanStatePath(), desc)
		s.mu.Lock()
		s.quarantined = st.Quarantined
		if s.quarantined == nil {
			s.quarantined = map[string]int64{}
		}
		s.quarScope = desc
	}
	capAt, ok := s.quarantined[id]
	budget := s.exportBudget.maxBytes
	s.mu.Unlock()
	return ok && budget <= capAt
}

// quarantineExport records id's export-cap failure at the current
// budget and settles it out of the pending lane. Exports only grow, so
// a fetch at this budget can never succeed — the lane exists for work
// that can still converge, and a permanently oversized export is not
// it. The settle is the drain: a rev move re-emits the id through the
// windows, where the gate answers instantly (a diagnostic, not a
// refetch) and settles it again.
func (s *Source) quarantineExport(id string) {
	capAt := s.exportBudget.maxBytes
	s.mu.Lock()
	if s.quarantined != nil {
		if cur, ok := s.quarantined[id]; !ok || cur < capAt {
			s.quarantined[id] = capAt
		}
	}
	s.mu.Unlock()
	s.updateScanState(func(st *scanState) bool {
		if st.Quarantined == nil {
			st.Quarantined = map[string]int64{}
		}
		if cur, ok := st.Quarantined[id]; ok && cur >= capAt {
			return false
		}
		st.Quarantined[id] = capAt
		return true
	})
	s.settlePending(id)
}

// recordScan persists the post-scan checkpoint. The floor advances to
// the last fully-clean ascending coverage — the scan's own start
// instant, clamped down to the scan's end bound (a bounded scan's floor
// cannot leapfrog coverage it never made) and to the earliest dirty
// window's start (diagnostic-broken coverage is re-covered next sweep)
// — and never backward, not even against a floor a concurrent run
// landed while this one scanned.
//
// Pending becomes the emitted-but-unconfirmed set — this scan's whole
// emission, prior pending ids the id: lane did not prove gone, and
// corroboration-queue entries — three-way merged against the freshly
// locked re-read: pending another run added since our scan's own read
// is kept, and pending it drained is dropped, so neither direction of a
// racing run's work is erased by our stale base.
func (s *Source) recordScan(scanStart time.Time, out scanOutcome, prior scanState, found []source.Discovered) {
	_, attributed := s.scopeDescriptor()

	// emitted is what THIS scan positively observed — the windowed
	// emission plus the corroboration queue; both are fresh evidence
	// that outranks a foreign drain.
	emitted := make(map[string]bool, len(found)+len(out.retry))
	for _, d := range found {
		emitted[d.NativeID] = true
	}
	for id := range out.retry {
		emitted[id] = true
	}
	// keep is the pending set our scan computes: the emission plus
	// prior pending the id: lane did not prove gone.
	keep := make(map[string]bool, len(emitted)+len(prior.PendingIDs))
	for id := range emitted {
		keep[id] = true
	}
	priorSet := make(map[string]bool, len(prior.PendingIDs))
	for _, id := range prior.PendingIDs {
		priorSet[id] = true
		if !out.gone[id] {
			keep[id] = true
		}
	}

	// The floor is the clean coverage's end: scanStart normally — the
	// windows covered [from, tomorrow) and every row they returned was
	// observed by then — but a scan bounded below its start ends its
	// coverage at Through, exclusive, so the floor lands one tick under
	// it (a row AT the bound was not covered and must stay above the
	// MinTime filter next run).
	floor := scanStart
	if !out.end.IsZero() && !scanStart.Before(out.end) {
		floor = out.end.Add(-time.Nanosecond)
	}
	if !out.dirty.IsZero() && out.dirty.Before(floor) {
		floor = out.dirty
	}
	if floor.Before(prior.CleanThrough) {
		floor = prior.CleanThrough // the floor never regresses
	}
	if !attributed {
		// No account evidence — this scope cannot be told apart from
		// another account's, so a persisted floor could hide another
		// account's history. Pending still records; every run rescans.
		floor = time.Time{}
	}

	// Phase is the producing scan's provenance — an initial scan had no
	// committed floor to continue from.
	phase := phaseInitial
	if !prior.CleanThrough.IsZero() {
		phase = phaseCatchup
	}

	s.updateScanState(func(st *scanState) bool {
		// Three-way merge, base = prior (what this scan read), ours =
		// keep, theirs = the freshest committed pending set:
		//   - ours wins for anything we emitted or kept;
		//   - ids pending now that our base never knew are another
		//     run's additions — keep them;
		//   - ids our base knew that are gone from theirs are another
		//     run's drains — drop them, unless our own scan just
		//     re-emitted the id (fresh evidence outranks a drain).
		merged := make(map[string]bool, len(keep)+len(st.PendingIDs))
		for id := range keep {
			merged[id] = true
		}
		for _, id := range st.PendingIDs {
			if !priorSet[id] {
				merged[id] = true
			}
		}
		for id := range priorSet {
			if !slices.Contains(st.PendingIDs, id) && !emitted[id] {
				delete(merged, id)
			}
		}
		ids := make([]string, 0, len(merged))
		for id := range merged {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		if st.CleanThrough.After(floor) {
			floor = st.CleanThrough // a concurrent run advanced further — keep it
		}
		st.CleanThrough = floor
		st.PendingIDs = ids
		st.Phase = phase
		return true
	})
}
