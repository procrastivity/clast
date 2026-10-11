package capture

// Verb-level integration coverage for the amp source (BDS-262): a fake
// `amp` CLI on PATH answers `threads search|export|list` from files, and
// the real sweep/explicit paths drive the whole round trip — discovery
// through the checkpointed catch-up enumeration, verbatim artifact
// capture, the Unchanged seam, project correlation/backfill, curation
// retention, failure recovery, and selection gating.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/source/amp"
	"github.com/procrastivity/clast/internal/source/claude"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
	"github.com/procrastivity/clast/internal/verbs/analyzeverb"
	"github.com/procrastivity/clast/internal/verbs/show"
)

// ampTestInstallID is this machine's installationID — the value the
// fixture's device-id.json carries and correlatable exports must match.
const ampTestInstallID = "11111111-2222-4333-8444-555555555555"

// ampStubScript is the fake `amp` CLI (POSIX sh). The fixture dir named
// by $CLAST_FAKE_AMP is the server:
//   - catalog.txt: one "id rfc3339-updatedAt [archived] [pinned]" row
//     per thread — the search universe, filtered by after:/before: day
//     bounds, archived:/pinned: facets, or the id: lane, and silently
//     truncated at the server's 100-row page cap;
//   - export-<id>.json: the bytes `threads export <id>` emits verbatim
//     (absent file → the real client's does-not-exist exit);
//   - list.json: the `threads list` corroboration page (absent → []);
//   - fail-<verb>: presence makes that subcommand exit 1 with the
//     file's content on stderr; fail-export-<id> narrows it to one
//     thread while others keep serving;
//   - calls.log: one argv line per invocation — the fetch-count and
//     no-fetch assertions read it.
const ampStubScript = `#!/bin/sh
set -u
dir="${CLAST_FAKE_AMP:?CLAST_FAKE_AMP unset}"
printf '%s\n' "$*" >> "$dir/calls.log"
verb="${2:-none}"
fail="$dir/fail-$verb"
if [ -f "$fail" ]; then cat "$fail" >&2; exit 1; fi
case "$verb" in
search)
  q="${3:-}"
  case "$q" in
  id:*)
    want="${q#id:}"
    while read -r rid ru rarch rpin; do
      if [ "$rid" = "$want" ]; then
        printf '[{"id":"%s","title":"t","updatedAt":"%s"}]\n' "$rid" "$ru"
        exit 0
      fi
    done < "$dir/catalog.txt"
    printf '[]\n'
    ;;
  *)
    after=""; before=""; wantarch=""; wantpin=""
    for tok in $q; do
      case "$tok" in
      after:*) after="${tok#after:}" ;;
      before:*) before="${tok#before:}" ;;
      archived:*) wantarch="${tok#archived:}" ;;
      pinned:*) wantpin="${tok#pinned:}" ;;
      *) ;;
      esac
    done
    printf '['
    first=1
    rows=0
    while read -r rid ru rarch rpin; do
      if [ -z "$rid" ]; then continue; fi
      d="${ru%%T*}"
      keep=1
      if [ -n "$after" ] && [ "$d" \< "$after" ]; then keep=0; fi
      if [ -n "$before" ]; then
        if [ "$d" = "$before" ]; then keep=0; fi
        if [ "$d" \> "$before" ]; then keep=0; fi
      fi
      if [ -n "$wantarch" ] && [ "${rarch:-false}" != "$wantarch" ]; then keep=0; fi
      if [ -n "$wantpin" ] && [ "${rpin:-false}" != "$wantpin" ]; then keep=0; fi
      if [ "$keep" -eq 0 ]; then continue; fi
      if [ "$first" -eq 0 ]; then printf ','; fi
      first=0
      printf '{"id":"%s","title":"t","updatedAt":"%s"}' "$rid" "$ru"
      rows=$((rows + 1))
      # the server's silent page cap: a 100th row ends the page with no
      # truncation marker — the source must prove completeness itself
      if [ "$rows" -ge 100 ]; then break; fi
    done < "$dir/catalog.txt"
    printf ']\n'
    ;;
  esac
  ;;
export)
  pf="$dir/fail-export-${3:-}"
  if [ -f "$pf" ]; then cat "$pf" >&2; exit 1; fi
  f="$dir/export-${3:-}.json"
  if [ -f "$f" ]; then
    cat "$f"
  else
    printf 'Error: Thread %s does not exist.\n' "${3:-}" >&2
    exit 1
  fi
  ;;
list)
  if [ -f "$dir/list.json" ]; then cat "$dir/list.json"; else printf '[]\n'; fi
  ;;
*)
  printf 'unexpected argv: %s\n' "$*" >&2
  exit 2
  ;;
esac
exit 0
`

// ampFixture is one fake-amp test's harness: the stub's server dir, the
// pinned XDG_CACHE_HOME the checkpoint lands under, and the env/registry
// swaps every amp run needs.
type ampFixture struct {
	home       string // $CLAST_FAKE_AMP — catalog, exports, knobs, calls.log
	cache      string // $XDG_CACHE_HOME — where clast/amp/scan-state-*.json land
	claudeHome string // the sibling registry row's home — kept for re-registration
}

// newAmpFixture builds the whole amp test environment: per-test env
// seams (journal dir, config home, XDG data/cache), the stub binary on
// PATH, the amp state dir carrying device-id.json + secrets.json (the
// credential material Present probes when AMP_API_KEY is unset), and a
// registry table swapped to fresh rows — a clean amp.Source instance per
// test so memoized binary/install state can never leak between runs.
// Returns the fixture, the journal dir, and the config home.
func newAmpFixture(t *testing.T) (*ampFixture, string, string) {
	t.Helper()
	fx := &ampFixture{home: t.TempDir(), cache: t.TempDir(), claudeHome: t.TempDir()}
	claudeHome := fx.claudeHome // present-but-empty: a quiet local sibling row

	journalDir, configHome := envSeams(t, claudeHome)
	t.Setenv("XDG_CACHE_HOME", fx.cache)
	t.Setenv("AMP_API_KEY", "") // credentials come from secrets.json
	t.Setenv("CLAST_FAKE_AMP", fx.home)

	binDir := t.TempDir()
	writeFile(t, filepath.Join(binDir, "amp"), []byte(ampStubScript), 0o755)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	writeFile(t, filepath.Join(fx.home, "catalog.txt"), nil, 0o644)
	writeFile(t, filepath.Join(fx.home, "calls.log"), nil, 0o644)

	// The state dir the source probes: local install id, stored
	// credential material, and accounts.json — the non-secret account
	// evidence the scan-state checkpoint's scope key reads (its active
	// user pins which account's universe the progress belongs to).
	dataDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "amp")
	writeFile(t, filepath.Join(dataDir, "device-id.json"),
		[]byte(`{"installationID":"`+ampTestInstallID+`"}`), 0o644)
	writeFile(t, filepath.Join(dataDir, "secrets.json"), []byte("{}"), 0o600)
	writeFile(t, filepath.Join(dataDir, "accounts.json"),
		[]byte(`{"version":1,"active":{"https://ampcode.com/":"user_amp_test"}}`), 0o644)

	fx.resetAmp(t)
	return fx, journalDir, configHome
}

// resetAmp re-registers fresh source rows — a NEW amp.Source instance.
// The scope descriptor, install id, and binary markers are per-instance
// caches: one real `clast capture` invocation is one process, so a test
// that changes account evidence between runs must stand up the instance
// a new process would.
func (fx *ampFixture) resetAmp(t *testing.T) {
	t.Helper()
	prev := sourceregistry.All
	sourceregistry.All = []source.Source{claude.NewAt(fx.claudeHome), amp.New()}
	t.Cleanup(func() { sourceregistry.All = prev })
}

func writeFile(t *testing.T, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatal(err)
	}
}

// addThread puts a thread in the fake's universe: one catalog row plus
// the verbatim export document `threads export` serves.
func (fx *ampFixture) addThread(t *testing.T, id, updatedAt string, export []byte) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(fx.home, "catalog.txt"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "%s %s\n", id, updatedAt); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fx.home, "export-"+id+".json"), export, 0o644)
}

// bumpThread rewrites a catalog row's updatedAt and replaces its export
// — the "thread moved forward" case catch-up must recapture.
func (fx *ampFixture) bumpThread(t *testing.T, id, updatedAt string, export []byte) {
	t.Helper()
	path := filepath.Join(fx.home, "catalog.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ln := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !strings.HasPrefix(ln, id+" ") {
			out = append(out, ln)
		}
	}
	out = append(out, id+" "+updatedAt)
	writeFile(t, path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
	writeFile(t, filepath.Join(fx.home, "export-"+id+".json"), export, 0o644)
}

// fail makes the named subcommand (search/export/list) exit 1 with msg
// on stderr until unfail.
func (fx *ampFixture) fail(t *testing.T, verb, msg string) {
	t.Helper()
	writeFile(t, filepath.Join(fx.home, "fail-"+verb), []byte(msg+"\n"), 0o644)
}

func (fx *ampFixture) unfail(t *testing.T, verb string) {
	t.Helper()
	if err := os.Remove(filepath.Join(fx.home, "fail-"+verb)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// failExport fails `threads export id` alone — one thread's read broken
// while the rest of the universe serves — until unfailExport.
func (fx *ampFixture) failExport(t *testing.T, id, msg string) {
	t.Helper()
	writeFile(t, filepath.Join(fx.home, "fail-export-"+id), []byte(msg+"\n"), 0o644)
}

func (fx *ampFixture) unfailExport(t *testing.T, id string) {
	t.Helper()
	if err := os.Remove(filepath.Join(fx.home, "fail-export-"+id)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// callCount counts calls.log lines carrying the verb ("export" counts
// `threads export` invocations — the fetch-count evidence).
func (fx *ampFixture) callCount(t *testing.T, verb string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ln := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.HasPrefix(ln, "threads "+verb) {
			n++
		}
	}
	return n
}

// scanStatePaths lists every scope-keyed checkpoint under the test's
// cache home — one file per scope (account × install × journal target),
// so isolation assertions count the set rather than name one file.
func (fx *ampFixture) scanStatePaths(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(fx.cache, "clast", "amp", "scan-state-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(matches)
	return matches
}

// scanState reads THE scope's checkpoint — exactly one must exist in
// these tests (a single account/install/journal combination per run).
func (fx *ampFixture) scanState(t *testing.T) struct {
	CleanThrough string   `json:"clean_through"`
	PendingIDs   []string `json:"pending_ids"`
} {
	t.Helper()
	var st struct {
		CleanThrough string   `json:"clean_through"`
		PendingIDs   []string `json:"pending_ids"`
	}
	paths := fx.scanStatePaths(t)
	if len(paths) != 1 {
		t.Fatalf("scan-state files = %v, want exactly one scope's checkpoint", paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("scan-state read: %v", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("scan-state parse: %v", err)
	}
	return st
}

// searchCallLines returns the calls.log `threads search` lines —
// rescan evidence: an initial enumeration's first window is the
// history-floor query, a catch-up's is the floor-minus-a-day window.
func (fx *ampFixture) searchCallLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ln := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.HasPrefix(ln, "threads search ") {
			out = append(out, ln)
		}
	}
	return out
}

// ampDocSpec parameterizes the synthesized export docs — only what the
// completeness/correlation reads consult.
type ampDocSpec struct {
	InstallID  string // env.initial.platform.installationID ("" → no platform)
	WorkDir    string // a filesystem path, encoded as file:// ("" → no env.initial)
	Executor   string // meta.executorType ("" → local-client)
	Hostname   string // env.initial.hostname ("" → omitted)
	UpdatedAt  string // doc.updatedAt — the revision cursor
	CreatedMs  int64  // doc.created, epoch ms → started_at
	Messages   int    // alternating user/assistant, contiguous ids, all complete
	V          int    // doc.v — the server-side revision counter (churn bait)
	AgentState string // meta.lastKnownAgentState.state ("" → "idle")
}

// ampExportDoc synthesizes a structurally complete `threads export`
// document — contiguous messageIds from 1, every assistant state
// complete, lkas idle anchored at the last protocolMessageID.
func ampExportDoc(t *testing.T, id string, spec ampDocSpec) []byte {
	t.Helper()
	if spec.Messages == 0 {
		spec.Messages = 2
	}
	if spec.Executor == "" {
		spec.Executor = "local-client"
	}
	if spec.AgentState == "" {
		spec.AgentState = "idle"
	}
	msgs := make([]map[string]any, spec.Messages)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = map[string]any{
			"role":              role,
			"messageId":         i + 1,
			"protocolMessageID": fmt.Sprintf("M-%s-%d", id, i+1),
			"state":             map[string]any{"type": "complete"},
			"content":           []map[string]any{{"type": "text", "text": fmt.Sprintf("message %d", i+1)}},
		}
	}
	env := map[string]any{}
	if spec.WorkDir != "" || spec.InstallID != "" || spec.Hostname != "" {
		init := map[string]any{}
		if spec.WorkDir != "" {
			init["workingDirectory"] = "file://" + spec.WorkDir
		}
		if spec.InstallID != "" {
			init["platform"] = map[string]any{"installationID": spec.InstallID}
		}
		if spec.Hostname != "" {
			init["hostname"] = spec.Hostname
		}
		env["initial"] = init
	}
	doc := map[string]any{
		"id":        id,
		"v":         spec.V,
		"created":   spec.CreatedMs,
		"updatedAt": spec.UpdatedAt,
		"env":       env,
		"meta": map[string]any{
			"executorType": spec.Executor,
			"lastKnownAgentState": map[string]any{
				"state":     spec.AgentState,
				"messageID": fmt.Sprintf("M-%s-%d", id, spec.Messages),
			},
		},
		"messages": msgs,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// findAmpSession walks the journal for the one amp session dir named id.
func findAmpSession(t *testing.T, journalDir, id string) (journal.WalkItem, bool) {
	t.Helper()
	items, _, err := journal.Walk(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Key.Harness == "amp" && it.Key.NativeID == id {
			return it, true
		}
	}
	return journal.WalkItem{}, false
}

// ampArtifact reads the recorded transcript artifact bytes for a
// captured amp session — resolving through session.json's
// transcript.artifact, never a hardcoded name.
func ampArtifact(t *testing.T, journalDir string, item journal.WalkItem) []byte {
	t.Helper()
	path, err := journal.TranscriptPath(journalDir, item.Shard, item.Key, item.Session.Transcript.ArtifactName())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCommandAmpRoundTrip pins the whole explicit path: --harness amp
// discovers the thread through the checkpointed enumeration, fetches the
// export verbatim into transcript.json, records the normalized facts,
// and leaves the checkpoint holding the emitted-but-unconfirmed id.
func TestCommandAmpRoundTrip(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	export := ampExportDoc(t, "T-abc", ampDocSpec{
		InstallID: ampTestInstallID,
		UpdatedAt: "2026-08-30T10:00:00.000Z",
		CreatedMs: 1788064264680, // 2026-08-30
		Messages:  2,
	})
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z", export)

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("explicit amp capture: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("stdout = %q, want the captured line", stdout)
	}

	item, ok := findAmpSession(t, journalDir, "T-abc")
	if !ok {
		t.Fatal("no amp-T-abc session in the journal")
	}
	s := item.Session
	if s.Transcript.Format != "amp-export" || s.Transcript.Artifact != "transcript.json" {
		t.Errorf("transcript record = %+v, want amp-export + transcript.json", s.Transcript)
	}
	if s.Transcript.Lines != 2 || s.Counts.User != 1 || s.Counts.Assistant != 1 || !s.Substantive {
		t.Errorf("session facts = %+v", s)
	}
	if !s.LastActiveAt.Equal(mustRFC(t, "2026-08-30T10:00:00.000Z")) {
		t.Errorf("last_active_at = %v, want the export's updatedAt", s.LastActiveAt)
	}
	if s.SourcePath != "https://ampcode.com/threads/T-abc" {
		t.Errorf("source_path = %q, want the canonical thread URL", s.SourcePath)
	}

	// The artifact is the export, verbatim — byte for byte.
	if got := ampArtifact(t, journalDir, item); string(got) != string(export) {
		t.Error("transcript.json is not the verbatim export bytes")
	}

	// The checkpoint landed under the pinned cache home with the
	// emitted id held pending until a read-back confirms it.
	st := fx.scanState(t)
	if len(st.PendingIDs) != 1 || st.PendingIDs[0] != "T-abc" {
		t.Errorf("pending_ids = %v, want [T-abc]", st.PendingIDs)
	}
}

// TestCommandAmpSecondRunIsFetchFree pins the Unchanged seam end to end:
// the second explicit run emits nothing, and the calls log proves no
// second `threads export` ever happened — the pending id is drained by
// the journal read-back, not by another fetch.
func TestCommandAmpSecondRunIsFetchFree(t *testing.T) {
	fx, _, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680}))

	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if n := fx.callCount(t, "export"); n != 1 {
		t.Fatalf("export calls = %d, want exactly 1 (correlate memoized into capture)", n)
	}

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("second run: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	if n := fx.callCount(t, "export"); n != 1 {
		t.Fatalf("second run fetched: export calls = %d", n)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("pending_ids = %v, want drained by the Unchanged verdict", st.PendingIDs)
	}
}

// TestCommandAmpQuietCatchUpConverges is the step-12 dogfood
// regression: a quiet catch-up sweep spends work proportional to what
// changed — nothing, here. Two treadmills stood in the way: list
// corroboration re-queued every below-floor row forever, and an
// unchanged projectless commit re-ran a full `threads export` every
// sweep just to re-answer "no project". The fix pair — in-span
// corroboration gating plus the checkpointed (id, revision, host)
// verdict — is asserted on the observable ledger: calls.log and the
// checkpoint's pending set across fresh source instances.
func TestCommandAmpQuietCatchUpConverges(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)

	// T-abc is a sandbox-executor thread — legitimately projectless:
	// "" is the real verdict, and its commit must never re-export.
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{
			Executor:  "sandbox",
			UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680,
		}))

	// T-old is the treadmill bait: committed long ago, absent from the
	// search catalog, but reported by `threads list` on every sweep —
	// the row the old corroboration re-queued forever.
	if err := journal.WriteSession(journalDir, "2026-01-15",
		journal.SessionKey{Harness: "amp", NativeID: "T-old"},
		journal.Session{LastActiveAt: mustRFC(t, "2026-01-15T10:00:00.000Z")}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fx.home, "list.json"), []byte(`[
		{"id":"T-abc","updated":"2026-08-30T10:00:00.000Z","messageCount":2},
		{"id":"T-old","updated":"2026-01-15T10:00:00.000Z","messageCount":4}
	]`+"\n"), 0o644)

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("first capture: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("first run stdout = %q", stdout)
	}
	if n := fx.callCount(t, "export"); n != 1 {
		t.Fatalf("export calls = %d, want exactly 1 (correlate memoized into capture)", n)
	}
	if item, ok := findAmpSession(t, journalDir, "T-abc"); !ok || item.Session.Project != nil {
		t.Fatalf("T-abc should be committed projectless: %+v", item.Session)
	}

	// Runs two and three are fresh source instances — new processes,
	// cold memory — so the verdict and the span gate must be the
	// persisted checkpoint's, not a per-run memo's.
	for _, sweep := range []string{"second", "third"} {
		fx.resetAmp(t)
		stdout, stderr, err = runCommand(t, "--harness", "amp")
		if err != nil || stdout != "" || stderr != "" {
			t.Fatalf("%s sweep: stdout=%q stderr=%q err=%v, want fully quiet",
				sweep, stdout, stderr, err)
		}
		if n := fx.callCount(t, "export"); n != 1 {
			t.Fatalf("%s sweep: export calls = %d — an unchanged projectless "+
				"commit must answer from the recorded verdict, not a refetch", sweep, n)
		}
		if st := fx.scanState(t); len(st.PendingIDs) != 0 {
			t.Fatalf("%s sweep: pending_ids = %v, want drained — "+
				"below-floor list rows must not re-queue", sweep, st.PendingIDs)
		}
	}
}

// TestCommandAmpRecapture pins updatedAt-driven recapture: a thread
// still in the pending lane re-emits by id even below the floor's
// window, the moved updatedAt beats last_active_at, and the new export
// replaces the artifact.
func TestCommandAmpRecapture(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 1 {
		t.Fatalf("first run pending = %v, want the emitted id held", st.PendingIDs)
	}

	// The thread grows while still pending — an updatedAt still below
	// the next floor's window, so only the id: lane can emit it.
	fx.bumpThread(t, "T-abc", "2026-08-30T12:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T12:00:00.000Z", CreatedMs: 1788064264680, Messages: 4}))

	stdout, _, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if strings.TrimSpace(stdout) != "recaptured amp-T-abc" {
		t.Fatalf("stdout = %q, want the recaptured line", stdout)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")
	if item.Session.Transcript.Lines != 4 || item.Session.Counts.User != 2 {
		t.Errorf("recaptured facts = %+v / %+v", item.Session.Transcript, item.Session.Counts)
	}
	if !item.Session.LastActiveAt.Equal(mustRFC(t, "2026-08-30T12:00:00.000Z")) {
		t.Errorf("last_active_at = %v, want the bumped updatedAt", item.Session.LastActiveAt)
	}
}

// TestCommandAmpMetadataChurnKeepsFingerprint pins the canonical
// fingerprint's content scope: an export that differs only in metadata
// (v, updatedAt, meta fields — the projection ignores all of them)
// still recaptures on the moved updatedAt, stores the NEW bytes
// verbatim, and lands the SAME transcript sha256 — no conversation
// change reported, no bytes lost.
func TestCommandAmpMetadataChurnKeepsFingerprint(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2, V: 7}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")
	priorSHA := item.Session.Transcript.SHA256
	priorBytes := ampArtifact(t, journalDir, item)

	churned := ampExportDoc(t, "T-abc", ampDocSpec{
		UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680,
		Messages: 2, V: 8, // same messages, bumped revision + metadata
	})
	fx.bumpThread(t, "T-abc", "2026-08-30T11:00:00.000Z", churned)

	stdout, _, err := runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-abc" {
		t.Fatalf("recapture on moved updatedAt: stdout=%q err=%v", stdout, err)
	}
	item, _ = findAmpSession(t, journalDir, "T-abc")
	if item.Session.Transcript.SHA256 != priorSHA {
		t.Error("metadata-only churn changed the messages[] fingerprint")
	}
	if got := ampArtifact(t, journalDir, item); string(got) != string(churned) {
		t.Error("artifact did not store the new verbatim bytes")
	}
	if string(priorBytes) == string(churned) {
		t.Fatal("test setup error: churned export must differ byte-wise")
	}
}

// TestCommandAmpCorrelationAndBackfill pins project correlation both
// ways: a local-install thread in a registered clone lands its project
// at capture; one captured projectless backfills in place once the
// clone is registered — no recapture, just the in-place session.json
// rewrite the shared contract describes.
func TestCommandAmpCorrelationAndBackfill(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	repo := gitRepo(t)

	fx.addThread(t, "T-backfill", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-backfill", ampDocSpec{
			InstallID: ampTestInstallID, WorkDir: repo,
			UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680,
		}))

	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("capture: %v", err)
	}
	item, ok := findAmpSession(t, journalDir, "T-backfill")
	if !ok || item.Session.Project != nil {
		t.Fatalf("unregistered cwd should be projectless: %+v", item.Session)
	}

	// The clone registers (the `clast init` happened-after case);
	// next sweep backfills the unchanged session in place.
	registerClone(t, journalDir, "demo", repo)
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("backfill run: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(stdout) != "backfilled amp-T-backfill" {
		t.Fatalf("stdout = %q, want the backfilled line", stdout)
	}
	item, _ = findAmpSession(t, journalDir, "T-backfill")
	if item.Session.Project == nil || item.Session.Project.Slug != "demo" || item.Session.Project.Path != repo {
		t.Fatalf("backfilled project = %+v", item.Session.Project)
	}
	// The transcript copy and fingerprint are untouched — an in-place
	// session.json rewrite, not a recapture.
	if item.Session.Transcript.Artifact != "transcript.json" {
		t.Errorf("artifact record changed: %+v", item.Session.Transcript)
	}
}

// TestCommandAmpNonlocalEvidenceStaysProjectless pins the decline-don't-
// guess rule at the verb: foreign installs, sandbox and virtual
// executors, and a contradicting hostname all capture projectless —
// never resolved into a local clone.
func TestCommandAmpNonlocalEvidenceStaysProjectless(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	repo := gitRepo(t)
	registerClone(t, journalDir, "demo", repo)

	for _, tc := range []struct {
		id   string
		spec ampDocSpec
	}{
		{"T-foreign", ampDocSpec{InstallID: "22222222-3333-4555-8555-666666666666", WorkDir: repo}},
		{"T-sandbox", ampDocSpec{InstallID: ampTestInstallID, WorkDir: repo, Executor: "sandbox"}},
		{"T-virtual", ampDocSpec{Executor: "virtual"}}, // no env at all
		{"T-elsewhere", ampDocSpec{InstallID: ampTestInstallID, WorkDir: repo, Hostname: "amp-test-other-host"}},
	} {
		tc.spec.UpdatedAt = "2026-08-30T10:00:00.000Z"
		tc.spec.CreatedMs = 1788064264680
		fx.addThread(t, tc.id, tc.spec.UpdatedAt, ampExportDoc(t, tc.id, tc.spec))
	}
	stdout, _, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if n := strings.Count(stdout, "captured amp-"); n != 4 {
		t.Fatalf("stdout = %q, want all four captured", stdout)
	}
	for _, id := range []string{"T-foreign", "T-sandbox", "T-virtual", "T-elsewhere"} {
		item, ok := findAmpSession(t, journalDir, id)
		if !ok {
			t.Fatalf("%s not captured", id)
		}
		if item.Session.Project != nil {
			t.Errorf("%s resolved a project on nonlocal evidence: %+v", id, item.Session.Project)
		}
	}
}

// TestCommandAmpCurationSurvivesRecapture pins retention across the
// verbatim artifact's replacement: a curated session's curation.json —
// and the transcript stamp it recorded — ride through a recapture
// untouched.
func TestCommandAmpCurationSurvivesRecapture(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatal(err)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")
	reason := "user tested"
	if err := journal.WriteCuration(journalDir, item.Shard, item.Key, journal.Curation{
		State: journal.StateDismissed, Reason: &reason,
		TranscriptAtCuration: &journal.TranscriptStamp{
			Lines: item.Session.Transcript.Lines, SHA256: item.Session.Transcript.SHA256,
		},
	}); err != nil {
		t.Fatal(err)
	}

	fx.bumpThread(t, "T-abc", "2026-08-30T11:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680, Messages: 4}))
	stdout, _, err := runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-abc" {
		t.Fatalf("recapture: stdout=%q err=%v", stdout, err)
	}
	cur, present, err := journal.ReadCuration(journalDir, item.Shard, item.Key)
	if err != nil || !present || cur.State != journal.StateDismissed || cur.Reason == nil || *cur.Reason != reason {
		t.Fatalf("curation lost across recapture: present=%v cur=%+v err=%v", present, cur, err)
	}
}

// TestCommandAmpFailurePreservesPrior pins the failed-recapture posture:
// the item is a stderr diagnostic, the prior session.json and artifact
// stand untouched, and the id stays in the pending lane so a later
// sweep retries it.
func TestCommandAmpFailurePreservesPrior(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{
			InstallID: ampTestInstallID,
			UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2,
		}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatal(err)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")
	priorBytes := ampArtifact(t, journalDir, item)

	// The thread moved — and export now fails outright. Correlate's
	// fetch fails first; the capture never starts.
	fx.bumpThread(t, "T-abc", "2026-08-30T11:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{
			InstallID: ampTestInstallID,
			UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680, Messages: 4,
		}))
	fx.fail(t, "export", "Error: transport exploded")

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("per-item failure must not fail the run: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want no captured lines", stdout)
	}
	if !strings.Contains(stderr, "T-abc") || !strings.Contains(stderr, "transport exploded") {
		t.Errorf("stderr = %q, want the item diagnostic", stderr)
	}
	item2, _ := findAmpSession(t, journalDir, "T-abc")
	if item2.Session.Transcript.Lines != 2 {
		t.Errorf("prior session.json was disturbed: %+v", item2.Session.Transcript)
	}
	if got := ampArtifact(t, journalDir, item2); string(got) != string(priorBytes) {
		t.Error("prior artifact overwritten by a failed capture")
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 1 || st.PendingIDs[0] != "T-abc" {
		t.Errorf("pending_ids = %v, want the failed id held for retry", st.PendingIDs)
	}

	// The retry lane delivers: heal the export, sweep again, and the
	// pending id recaptures.
	fx.unfail(t, "export")
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-abc" {
		t.Fatalf("post-failure sweep: stdout=%q err=%v", stdout, err)
	}
	item3, _ := findAmpSession(t, journalDir, "T-abc")
	if item3.Session.Transcript.Lines != 4 {
		t.Errorf("retry did not land the new revision: %+v", item3.Session.Transcript)
	}
}

// TestCommandAmpOrphanedArtifactRetries pins the crash window the
// commit order leaves: artifacts rename in before session.json writes,
// so a crash between them leaves a session directory whose transcript
// is present but whose session.json never landed. The walk diagnoses it
// (missing session.json is a diag, not an item), the still-pending id
// re-surfaces through the id: lane, and the sweep commits it fresh.
func TestCommandAmpOrphanedArtifactRetries(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatal(err)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")

	// Simulate the post-artifact crash: session.json gone, the
	// artifact left behind as an orphan.
	if err := os.Remove(journal.SessionJSONPath(journalDir, item.Shard, item.Key)); err != nil {
		t.Fatal(err)
	}
	if _, ok := findAmpSession(t, journalDir, "T-abc"); ok {
		t.Fatal("orphan dir must not read as a committed session")
	}

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("sweep over the orphan: %v", err)
	}
	if strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("stdout = %q, want the orphan recaptured fresh", stdout)
	}
	if !strings.Contains(stderr, "missing session.json") {
		t.Errorf("stderr = %q, want the walk's orphan diagnostic", stderr)
	}
	item2, ok := findAmpSession(t, journalDir, "T-abc")
	if !ok || item2.Session.Transcript.Lines != 2 {
		t.Fatalf("orphan not healed: %+v", item2.Session)
	}
}

// TestCommandAmpWorseExportNeverReplaces pins the step-08 recapture
// veto end to end: once a revision is committed, a fresh read that is
// provably worse — stamped behind the committed revision, shorter than
// it, or the same revision of a clean commit — is refused at the seam
// (the staged bytes are discarded, the committed session.json and
// artifact stand, the refusal is disclosed, and the id stays pending
// for the next sweep's re-read). Once the server serves a genuinely
// ahead doc the pending id recaptures normally.
func TestCommandAmpWorseExportNeverReplaces(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	const t1 = "2026-08-30T10:00:00.000Z"
	const t2 = "2026-08-30T11:00:00.000Z"

	// Three committed captures: two clean 4-message docs and one clean
	// 2-message doc (same rev churn case).
	fx.addThread(t, "T-stale", t1, ampExportDoc(t, "T-stale", ampDocSpec{UpdatedAt: t1, CreatedMs: 1788064264680, Messages: 4}))
	fx.addThread(t, "T-short", t1, ampExportDoc(t, "T-short", ampDocSpec{UpdatedAt: t1, CreatedMs: 1788064264680, Messages: 4}))
	fx.addThread(t, "T-same", t1, ampExportDoc(t, "T-same", ampDocSpec{UpdatedAt: t1, CreatedMs: 1788064264680, Messages: 2, V: 7}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	prior := map[string]journal.Session{}
	priorBytes := map[string][]byte{}
	for _, id := range []string{"T-stale", "T-short", "T-same"} {
		item, ok := findAmpSession(t, journalDir, id)
		if !ok {
			t.Fatalf("%s not committed", id)
		}
		prior[id] = item.Session
		priorBytes[id] = ampArtifact(t, journalDir, item)
	}

	// Metadata advanced for all three (catalog rows now advertise t2),
	// but the export reads are each provably worse than committed:
	fx.bumpThread(t, "T-stale", t2, // a stale-stamped read — the doc's own updatedAt predates the commit
		ampExportDoc(t, "T-stale", ampDocSpec{UpdatedAt: "2026-08-30T09:00:00.000Z", CreatedMs: 1788064264680, Messages: 6}))
	fx.bumpThread(t, "T-short", t2, // a shorter read at a newer stamp — a raced/partial export
		ampExportDoc(t, "T-short", ampDocSpec{UpdatedAt: t2, CreatedMs: 1788064264680, Messages: 2}))
	fx.bumpThread(t, "T-same", t2, // same revision stamp, only volatile churn — nothing new to prove
		ampExportDoc(t, "T-same", ampDocSpec{UpdatedAt: t1, CreatedMs: 1788064264680, Messages: 2, V: 8}))

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("vetoed recaptures must not fail the run: %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q — nothing was committed, so no captured lines may print", stdout)
	}
	for _, id := range []string{"T-stale", "T-short", "T-same"} {
		if !strings.Contains(stderr, "refusing to replace the committed capture of "+id) {
			t.Errorf("stderr lacks the refusal diagnostic for %s: %q", id, stderr)
		}
		item, _ := findAmpSession(t, journalDir, id)
		if !item.Session.LastActiveAt.Equal(prior[id].LastActiveAt) ||
			item.Session.Transcript != prior[id].Transcript {
			t.Errorf("%s session.json moved under a refused read: %+v → %+v", id, prior[id], item.Session)
		}
		if got := ampArtifact(t, journalDir, item); string(got) != string(priorBytes[id]) {
			t.Errorf("%s committed artifact was overwritten by a refused read", id)
		}
	}
	st := fx.scanState(t)
	for _, id := range []string{"T-stale", "T-short", "T-same"} {
		if !slices.Contains(st.PendingIDs, id) {
			t.Errorf("pending_ids = %v — the vetoed id %s stays owed", st.PendingIDs, id)
		}
	}

	// The pending lane converges: once the server serves genuinely
	// ahead docs, every vetoed id recaptures and drains. resetAmp stands
	// up the fresh process a real sweep would be — the memoized export
	// is per-invocation, keyed on the enumerated revision, so a doc
	// swapped under an unchanged advertisement needs a new instance.
	fx.bumpThread(t, "T-stale", t2, ampExportDoc(t, "T-stale", ampDocSpec{UpdatedAt: t2, CreatedMs: 1788064264680, Messages: 6}))
	fx.bumpThread(t, "T-short", t2, ampExportDoc(t, "T-short", ampDocSpec{UpdatedAt: t2, CreatedMs: 1788064264680, Messages: 6}))
	fx.bumpThread(t, "T-same", t2, ampExportDoc(t, "T-same", ampDocSpec{UpdatedAt: t2, CreatedMs: 1788064264680, Messages: 2, V: 9}))
	fx.resetAmp(t)
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("healed sweep: %v", err)
	}
	if got := strings.Count(stdout, "recaptured amp-"); got != 3 {
		t.Fatalf("stdout = %q, want all three recaptured", stdout)
	}
	item, _ := findAmpSession(t, journalDir, "T-short")
	if item.Session.Transcript.Lines != 6 {
		t.Errorf("T-short after heal: %+v — the fuller doc must land", item.Session.Transcript)
	}
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("settling sweep: %v", err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("pending_ids = %v, want drained after the commits proved out", st.PendingIDs)
	}
}

// TestCommandAmpIncompleteCommitRepairsSameRevision pins the repair
// half of the veto: a mid-turn (flagged) commit records incomplete on
// session.json, and a same-revision re-read that saw further — or the
// same content finally reading clean — may replace it in place, while
// a clean commit's equal read never can.
func TestCommandAmpIncompleteCommitRepairsSameRevision(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	const t1 = "2026-08-30T10:00:00.000Z"
	const t2 = "2026-08-30T11:00:00.000Z"

	// A mid-turn capture: committed under the flag, recorded
	// incomplete in session.json itself.
	fx.addThread(t, "T-flag", t1, ampExportDoc(t, "T-flag", ampDocSpec{
		UpdatedAt: t1, CreatedMs: 1788064264680, Messages: 2, AgentState: "streaming",
	}))
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-flag" {
		t.Fatalf("mid-turn capture: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	if !strings.Contains(stderr, "streaming") {
		t.Errorf("stderr = %q, want the mid-turn completeness diagnostic", stderr)
	}
	item, _ := findAmpSession(t, journalDir, "T-flag")
	if !item.Session.Incomplete {
		t.Error("session.json must record incomplete on a flagged commit")
	}

	// The catalog advertises a newer revision (t2), but the export
	// read of the moment is a fuller view of the SAME t1 revision —
	// a repair of the flagged prefix, allowed to commit in place. (The
	// advertisement moved, so the memo key moved with it — no reset
	// needed here.)
	fx.bumpThread(t, "T-flag", t2, ampExportDoc(t, "T-flag", ampDocSpec{
		UpdatedAt: t1, CreatedMs: 1788064264680, Messages: 4, // idle at t1 — reads clean now
	}))
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-flag" {
		t.Fatalf("same-revision repair: stdout=%q err=%v", stdout, err)
	}
	item, _ = findAmpSession(t, journalDir, "T-flag")
	if !item.Session.LastActiveAt.Equal(mustRFC(t, t1)) {
		t.Errorf("repair rewrote the revision stamp: %v, want still %s", item.Session.LastActiveAt, t1)
	}
	if item.Session.Transcript.Lines != 4 || item.Session.Incomplete {
		t.Errorf("repaired session = lines %d incomplete %v, want 4/false", item.Session.Transcript.Lines, item.Session.Incomplete)
	}

	// The advertised t2 is still unmet — the id stays pending until a
	// genuinely ahead doc lands. The served doc changes under the same
	// advertised revision, so the next run needs the fresh process a
	// real sweep is (the memo keys on the enumerated updatedAt).
	if st := fx.scanState(t); !slices.Contains(st.PendingIDs, "T-flag") {
		t.Fatalf("pending_ids = %v — the unmet advertised revision stays owed", st.PendingIDs)
	}
	fx.bumpThread(t, "T-flag", t2, ampExportDoc(t, "T-flag", ampDocSpec{
		UpdatedAt: t2, CreatedMs: 1788064264680, Messages: 6,
	}))
	fx.resetAmp(t)
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-flag" {
		t.Fatalf("real revision: stdout=%q err=%v", stdout, err)
	}
	item, _ = findAmpSession(t, journalDir, "T-flag")
	if !item.Session.LastActiveAt.Equal(mustRFC(t, t2)) || item.Session.Transcript.Lines != 6 {
		t.Errorf("converged session = %+v", item.Session)
	}
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("settling sweep: %v", err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("pending_ids = %v, want drained", st.PendingIDs)
	}
}

// TestCommandAmpSessionWriteFailureHealsOrphan pins the interruption
// seam between artifact commit and session.json commit — exercised as
// a WriteSession failure rather than a simulated kill, same recovery
// rule: the committed artifact is an orphan (the walk diagnoses it,
// never counts it), the id stays pending, and the next sweep re-commits
// the whole session fresh.
func TestCommandAmpSessionWriteFailureHealsOrphan(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")

	// Fault: session.json is a directory — the walk's
	// missing/malformed session.json diagnostic fires, and the atomic
	// rename at session-document time cannot land onto a directory.
	sessJSON := journal.SessionJSONPath(journalDir, item.Shard, item.Key)
	if err := os.Remove(sessJSON); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sessJSON, 0o755); err != nil {
		t.Fatal(err)
	}
	newDoc := ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680, Messages: 4})
	fx.bumpThread(t, "T-abc", "2026-08-30T11:00:00.000Z", newDoc)

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	ce := assertClasterr(t, err, "internal.capture")
	if !strings.Contains(ce.Message, "session.json") {
		t.Errorf("abort error %q should name session.json", ce.Message)
	}
	if stdout != "" {
		t.Errorf("stdout = %q — nothing committed", stdout)
	}
	if !strings.Contains(stderr, "session.json") {
		t.Errorf("stderr = %q, want the walk's diagnostic naming the malformed document", stderr)
	}
	// The artifact committed before the session write failed — the
	// orphan carries the new revision's bytes, already on disk.
	orphan, err := os.ReadFile(filepath.Join(journal.SessionDir(journalDir, item.Shard, item.Key), "transcript.json"))
	if err != nil || string(orphan) != string(newDoc) {
		t.Fatalf("orphan artifact = %v / %d bytes, want the new export verbatim", err, len(orphan))
	}
	if st := fx.scanState(t); !slices.Contains(st.PendingIDs, "T-abc") {
		t.Fatalf("pending_ids = %v — the interrupted capture stays owed", st.PendingIDs)
	}

	// Recovery: clear the blocker, sweep, and the pending id commits
	// the session fresh over the orphan.
	if err := os.RemoveAll(sessJSON); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("healing sweep: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	item2, ok := findAmpSession(t, journalDir, "T-abc")
	if !ok || item2.Session.Transcript.Lines != 4 ||
		!item2.Session.LastActiveAt.Equal(mustRFC(t, "2026-08-30T11:00:00.000Z")) {
		t.Fatalf("healed session = %+v, want the new revision committed", item2.Session)
	}
}

// TestCommandAmpConcurrentSweepsMergePending exercises two real capture
// runs overlapping on the same journal and the same scope checkpoint —
// two amp.Source instances (the two-process shape), sharing the fake
// CLI, the cache dir, and the journal root. The lockfile must
// serialize their checkpoint writes, the merge must keep every actor's
// pending work, and the journal must end with every committed session
// intact — whichever interleaving the scheduler picks.
func TestCommandAmpConcurrentSweepsMergePending(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	// Seed two more ids into the pending lane through the checkpoint —
	// a prior run's unconfirmed work neither racing sweep may lose —
	// then give both catalog rows and one a broken export.
	fx.seedPending(t, "T-owed", "T-new")
	fx.addThread(t, "T-owed", "2026-09-01T10:00:00.000Z",
		ampExportDoc(t, "T-owed", ampDocSpec{UpdatedAt: "2026-09-01T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	fx.addThread(t, "T-new", "2026-09-02T10:00:00.000Z",
		ampExportDoc(t, "T-new", ampDocSpec{UpdatedAt: "2026-09-02T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	fx.failExport(t, "T-owed", "Error: transport exploded")

	// Two actors: separate Source instances (separate mutexes — only
	// the lockfile arbitrates), one scope file, one journal.
	deps := func(src *amp.Source) Deps {
		return Deps{Root: journalDir, Sources: []source.Source{src}, Now: time.Now}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	failures := make([][]SourceFailure, 2)
	for i, src := range []*amp.Source{amp.New(), amp.New()} {
		wg.Add(1)
		go func(i int, src *amp.Source) {
			defer wg.Done()
			_, _, failures[i], errs[i] = Run(context.Background(), deps(src))
		}(i, src)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("concurrent run %d: %v", i, errs[i])
		}
		if len(failures[i]) != 0 {
			t.Fatalf("concurrent run %d source failures: %v", i, failures[i])
		}
	}

	// Journal truth is coherent under either interleaving: the seeded
	// thread is captured once, the committed one stands, and the
	// broken-export id never captured.
	if _, ok := findAmpSession(t, journalDir, "T-abc"); !ok {
		t.Fatal("committed session lost under contention")
	}
	item, ok := findAmpSession(t, journalDir, "T-new")
	if !ok || item.Session.Transcript.Lines != 2 {
		t.Fatalf("T-new not captured under contention: %+v", item.Session)
	}
	if _, ok := findAmpSession(t, journalDir, "T-owed"); ok {
		t.Fatal("the failed-export thread must not be captured")
	}

	// The checkpoint is one valid, scope-stamped document; the failed
	// id is still owed; no lock is left behind.
	st := fx.scanState(t)
	if !slices.Contains(st.PendingIDs, "T-owed") {
		t.Fatalf("pending_ids = %v — the failing id must survive the race", st.PendingIDs)
	}
	for _, id := range st.PendingIDs {
		switch id {
		case "T-abc", "T-owed", "T-new":
		default:
			t.Fatalf("pending_ids = %v — garbage id survived the merge", st.PendingIDs)
		}
	}
	if locks, _ := filepath.Glob(filepath.Join(fx.cache, "clast", "amp", "*.lock")); len(locks) != 0 {
		t.Errorf("lock files left behind: %v", locks)
	}

	// Healing the broken export lets a later sweep drain the lane dry.
	fx.unfailExport(t, "T-owed")
	stdout, _, err := runCommand(t, "--harness", "amp")
	if err != nil || !strings.Contains(stdout, "captured amp-T-owed") {
		t.Fatalf("post-race capture: stdout=%q err=%v", stdout, err)
	}
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("settling sweep: %v", err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("pending_ids = %v, want drained once every id proved out", st.PendingIDs)
	}
}

// seedPending adds ids to the scope checkpoint's pending_ids set
// verbatim — a foreign run's unconfirmed work, planted the way a real
// second actor's recordScan would leave it.
func (fx *ampFixture) seedPending(t *testing.T, ids ...string) {
	t.Helper()
	paths := fx.scanStatePaths(t)
	if len(paths) != 1 {
		t.Fatalf("scan-state files = %v, want exactly one scope's checkpoint to seed", paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("seedPending: checkpoint must parse: %v", err)
	}
	set := map[string]bool{}
	if arr, ok := doc["pending_ids"].([]any); ok {
		for _, v := range arr {
			set[v.(string)] = true
		}
	}
	for _, id := range ids {
		set[id] = true
	}
	var out []string
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	doc["pending_ids"] = out
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, paths[0], raw, 0o644)
}

// TestCommandAmpChurnDuringFailedFetchKeepsPrior pins the metadata-churn
// rule at the fault level: a bumped updatedAt whose export read fails
// leaves the committed bytes and fingerprint untouched (the pending id
// retries), and the churn-only document that eventually lands stores
// the new verbatim bytes under the SAME canonical fingerprint — then
// the settled read-back drains the lane rather than requeueing on
// volatile fields forever.
func TestCommandAmpChurnDuringFailedFetchKeepsPrior(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2, V: 7}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	item, _ := findAmpSession(t, journalDir, "T-abc")
	priorSHA := item.Session.Transcript.SHA256
	priorBytes := ampArtifact(t, journalDir, item)

	// Metadata moved; the fetch fails. Two failing sweeps to prove the
	// failure is idempotent — neither disturbs the committed truth.
	fx.bumpThread(t, "T-abc", "2026-08-30T11:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680, Messages: 2, V: 8}))
	fx.fail(t, "export", "Error: transport exploded")
	for i := 0; i < 2; i++ {
		stdout, stderr, err := runCommand(t, "--harness", "amp")
		if err != nil || stdout != "" {
			t.Fatalf("failed sweep %d: stdout=%q err=%v", i, stdout, err)
		}
		if !strings.Contains(stderr, "transport exploded") {
			t.Errorf("failed sweep %d stderr = %q, want the item diagnostic", i, stderr)
		}
		item2, _ := findAmpSession(t, journalDir, "T-abc")
		if item2.Session.Transcript.SHA256 != priorSHA ||
			string(ampArtifact(t, journalDir, item2)) != string(priorBytes) {
			t.Fatalf("failed sweep %d disturbed the committed capture", i)
		}
		if st := fx.scanState(t); !slices.Contains(st.PendingIDs, "T-abc") {
			t.Fatalf("failed sweep %d lost the pending id: %v", i, st.PendingIDs)
		}
	}

	// The read heals; the served doc differs only in volatile fields —
	// new bytes committed, same fingerprint, one clean recapture.
	fx.unfail(t, "export")
	churned := ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680, Messages: 2, V: 8})
	fx.bumpThread(t, "T-abc", "2026-08-30T11:00:00.000Z", churned)
	stdout, _, err := runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-abc" {
		t.Fatalf("healed sweep: stdout=%q err=%v", stdout, err)
	}
	item, _ = findAmpSession(t, journalDir, "T-abc")
	if item.Session.Transcript.SHA256 != priorSHA {
		t.Error("metadata-only churn moved the canonical fingerprint")
	}
	if got := ampArtifact(t, journalDir, item); string(got) != string(churned) {
		t.Error("the churned verbatim bytes did not replace the committed artifact")
	}

	// …and the lane drains on the next read-back — volatile churn does
	// not requeue the id forever.
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("settling sweep: %v", err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("pending_ids = %v, want drained", st.PendingIDs)
	}
}

// TestCommandAmpSearchFailureKeepsState pins the search-path budget:
// an enumeration outage is a disclosed source failure (stderr, exit 0)
// that leaves the committed sessions, the floor, and the pending set
// exactly as they were — recordScan never ran, so nothing was written.
func TestCommandAmpSearchFailureKeepsState(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	stateBefore, err := os.ReadFile(fx.scanStatePaths(t)[0])
	if err != nil {
		t.Fatal(err)
	}

	fx.fail(t, "search", "Error: Cannot reach Amp servers")
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, "Cannot reach") {
		t.Errorf("explicit path must name the search failure: %q", ce.Message)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	_ = stderr
	item, _ := findAmpSession(t, journalDir, "T-abc")
	if item.Session.Transcript.Lines != 2 {
		t.Error("search failure disturbed the committed session")
	}
	if stateAfter, _ := os.ReadFile(fx.scanStatePaths(t)[0]); string(stateAfter) != string(stateBefore) {
		t.Error("a failed enumeration must not write the checkpoint")
	}
}

// TestCommandAmpCorruptCheckpointRescans pins the cache-corruption rule
// at the seam: garbage in the scope's checkpoint file is a zero state —
// a full rescan from the history floor — never an abort and never a
// borrowed floor. The committed thread re-enumerates, Unchanged proves
// the journal already holds it, and the pending lane drains.
func TestCommandAmpCorruptCheckpointRescans(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	paths := fx.scanStatePaths(t)
	if len(paths) != 1 {
		t.Fatalf("scan-state files = %v", paths)
	}
	if err := os.WriteFile(paths[0], []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Truncate the call log so the rescan assertion reads only the
	// post-corruption run's searches — the initial scan searched the
	// same floor window and would satisfy it vacuously.
	if err := os.WriteFile(filepath.Join(fx.home, "calls.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("corrupt checkpoint must not fail the run: %v (stderr %q)", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q — the thread is already committed; a rescan is quiet", stdout)
	}
	var rescanned bool
	for _, ln := range fx.searchCallLines(t) {
		if strings.Contains(ln, "after:2025-01-01") {
			rescanned = true
		}
	}
	if !rescanned {
		t.Errorf("corrupt state did not rescan from the floor: %v", fx.searchCallLines(t))
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("pending_ids = %v — the read-back settled the re-emitted id", st.PendingIDs)
	}
	if item, ok := findAmpSession(t, journalDir, "T-abc"); !ok || item.Session.Transcript.Lines != 2 {
		t.Error("the committed session survived nothing — the rescan must leave it standing")
	}
}

// TestCommandAmpSweepSelection pins the shared capture-policy gates on a
// network source, all through the real command: bare sweeps skip amp,
// capture.amp.auto admits it, capture.exclude overrides the opt-in, and
// --harness bypasses both.
func TestCommandAmpSweepSelection(t *testing.T) {
	fx, journalDir, configHome := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))

	// Bare sweep: a network-model source never runs — and "never runs"
	// is literal. calls.log records every stub invocation, so an empty
	// log proves zero amp spawns of any verb (not merely no searches),
	// and no checkpoint file proves Discover never executed at all:
	// the selection gate kept the source out of the walk set entirely.
	stdout, stderr, err := runCommand(t)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("bare sweep: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	if lines := fx.callLines(t); len(lines) != 0 {
		t.Fatalf("bare sweep invoked amp: %v", lines)
	}
	if paths := fx.scanStatePaths(t); len(paths) != 0 {
		t.Fatalf("bare sweep ran Discover: checkpoints %v", paths)
	}

	// capture.amp.auto opts the sweep in.
	writeConfig(t, configHome, "capture:\n  amp:\n    auto: true\n")
	stdout, _, err = runCommand(t)
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("auto sweep: stdout=%q err=%v", stdout, err)
	}
	if _, ok := findAmpSession(t, journalDir, "T-abc"); !ok {
		t.Fatal("auto sweep did not capture")
	}

	// capture.exclude beats the auto opt-in — nothing to do. A fresh
	// thread stamped right now (above every checkpoint floor) makes the
	// skip observable: it is there to capture and the sweep leaves it.
	writeConfig(t, configHome, "capture:\n  exclude: [amp]\n  amp:\n    auto: true\n")
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	fx.addThread(t, "T-def", stamp,
		ampExportDoc(t, "T-def", ampDocSpec{UpdatedAt: stamp, CreatedMs: 1788064264680, Messages: 2}))
	stdout, stderr, err = runCommand(t)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("excluded+auto sweep: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	if _, ok := findAmpSession(t, journalDir, "T-def"); ok {
		t.Fatal("excluded sweep captured")
	}

	// And --harness amp is the authorization — exclusion included. The
	// run is observable: it captures the thread the sweep left behind.
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-def" {
		t.Fatalf("explicit under exclusion: stdout=%q err=%v", stdout, err)
	}

	// The same holds with the opt-in written in its explicit-off
	// spelling: auto: false is not a revocation of --harness — the flag
	// is the per-invocation authorization either way.
	writeConfig(t, configHome, "capture:\n  exclude: [amp]\n  amp:\n    auto: false\n")
	stamp = time.Now().UTC().Format(time.RFC3339Nano)
	fx.addThread(t, "T-ghi", stamp,
		ampExportDoc(t, "T-ghi", ampDocSpec{UpdatedAt: stamp, CreatedMs: 1788064264680, Messages: 2}))
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-ghi" {
		t.Fatalf("explicit under exclusion+auto:false: stdout=%q err=%v", stdout, err)
	}
}

// TestCommandAmpAbsentBinary pins the two absent-transport verdicts:
// the sweep is silent (absent is not a failure) and writes no
// checkpoint; the explicit name discloses source-unavailable.
func TestCommandAmpAbsentBinary(t *testing.T) {
	fx, _, _ := newAmpFixture(t)
	t.Setenv("PATH", t.TempDir()) // no amp on PATH at all

	stdout, stderr, err := runCommand(t)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("absent-binary sweep: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	if paths := fx.scanStatePaths(t); len(paths) != 0 {
		t.Errorf("absent binary wrote checkpoints: %v", paths)
	}

	_, _, err = runCommand(t, "--harness", "amp")
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"amp"`) || !strings.Contains(ce.Message, "amp") {
		t.Errorf("message %q should name the source", ce.Message)
	}
}

// TestCommandAmpCredentialFailures pins both credential-verdict paths:
// binary present but nothing to authenticate with is unavailable at the
// Presence probe; a binary that runs and gets auth-rejected is the
// sweep's disclosed failure and the explicit path's unavailable.
func TestCommandAmpCredentialFailures(t *testing.T) {
	fx, _, configHome := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))

	// Binary resolvable, zero credential material → Present fails. The
	// fixture writes secrets.json AND accounts.json; both probe as
	// credential material, so both go.
	dataDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "amp")
	for _, name := range []string{"secrets.json", "accounts.json"} {
		if err := os.Remove(filepath.Join(dataDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := runCommand(t, "--harness", "amp")
	ce := assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, "credential") {
		t.Errorf("message %q should name the credential probe", ce.Message)
	}

	// …but the probe is the explicit path's alone. An opted-in sweep
	// never consults it — selection is the only gate, and the CLI's own
	// verdict is the sweep's arbiter: with zero credential material the
	// transport still runs and (the stub serving) the thread captures.
	writeConfig(t, configHome, "capture:\n  amp:\n    auto: true\n")
	stdout, stderr, err := runCommand(t)
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("opted-in sweep under absent credentials: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}

	// Credentials present; the server rejects them → the sweep
	// discloses a source failure (exit 0) and the explicit path maps
	// it to capture.source-unavailable.
	writeFile(t, filepath.Join(dataDir, "secrets.json"), []byte("{}"), 0o600)
	fx.fail(t, "search", "Error: 401 Unauthorized")

	stdout, stderr, err = runCommand(t)
	if err != nil {
		t.Fatalf("auth-failed sweep must still exit 0: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing captured", stdout)
	}
	if !strings.Contains(stderr, "capture: amp:") || !strings.Contains(stderr, "401") {
		t.Errorf("stderr = %q, want the amp failure disclosure", stderr)
	}

	// The same disclosure rides the --json payload's additive
	// "unavailable" row — the machine-readable half of the same
	// contract, present only because the source failed.
	stdout, _, err = runCommandFlags(t, cliflags.Flags{JSON: true})
	if err != nil {
		t.Fatalf("auth-failed --json sweep must still exit 0: %v", err)
	}
	var payload struct {
		Captured    []json.RawMessage `json:"captured"`
		Unavailable []struct {
			Source string `json:"source"`
			Error  string `json:"error"`
		} `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not one JSON value: %v; stdout=%q", err, stdout)
	}
	if len(payload.Captured) != 0 {
		t.Errorf("captured = %s, want empty under the failed sweep", payload.Captured)
	}
	if len(payload.Unavailable) != 1 || payload.Unavailable[0].Source != "amp" ||
		!strings.Contains(payload.Unavailable[0].Error, "401") {
		t.Errorf("unavailable = %+v, want one amp row carrying the auth failure", payload.Unavailable)
	}

	_, _, err = runCommand(t, "--harness", "amp")
	ce = assertClasterr(t, err, "capture.source-unavailable")
	if !strings.Contains(ce.Message, `"amp"`) || !strings.Contains(ce.Message, "401") {
		t.Errorf("message %q should name the source and the auth failure", ce.Message)
	}
}

// TestCommandAmpMalformedExport pins the incompatible-document mapping:
// an export that fetches but cannot parse is a per-item diagnostic —
// disclosed on stderr naming the thread, the run still exits 0,
// nothing commits, and the id stays pending for a later retry — while
// a healthy sibling in the same sweep captures normally. Correlate's
// own fetch is the verdict, so the whole failure costs one export call
// (the bounded-retry loop never sees unparseable bytes). Healing the
// doc converges on the next sweep through the pending lane alone.
func TestCommandAmpMalformedExport(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	const stamp = "2026-08-30T10:00:00.000Z"
	fx.addThread(t, "T-bad", stamp, []byte("{not a parseable export"))
	fx.addThread(t, "T-good", stamp,
		ampExportDoc(t, "T-good", ampDocSpec{UpdatedAt: stamp, CreatedMs: 1788064264680, Messages: 2}))

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("a malformed item must not fail the run: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(stdout) != "captured amp-T-good" {
		t.Errorf("stdout = %q, want only the healthy thread's line", stdout)
	}
	if !strings.Contains(stderr, "T-bad") || !strings.Contains(stderr, "malformed") {
		t.Errorf("stderr = %q, want the malformed-export diagnostic naming the thread", stderr)
	}
	if _, ok := findAmpSession(t, journalDir, "T-bad"); ok {
		t.Error("a document that cannot parse committed a session")
	}
	if n := fx.exportCallsFor(t, "T-bad"); n != 1 {
		t.Errorf("export calls for T-bad = %d, want 1 — correlate's fetch is the whole verdict", n)
	}
	if st := fx.scanState(t); !slices.Contains(st.PendingIDs, "T-bad") {
		t.Errorf("pending_ids = %v — the unreadable id stays owed for retry", st.PendingIDs)
	}

	// The pending lane delivers: healed bytes re-read on the next
	// sweep and commit fresh.
	fx.bumpThread(t, "T-bad", "2026-08-30T11:00:00.000Z",
		ampExportDoc(t, "T-bad", ampDocSpec{UpdatedAt: "2026-08-30T11:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-bad" {
		t.Fatalf("healed sweep: stdout=%q err=%v", stdout, err)
	}
	if item, ok := findAmpSession(t, journalDir, "T-bad"); !ok || item.Session.Transcript.Lines != 2 {
		t.Errorf("healed session = %+v", item.Session)
	}
}

// countSearchLinesContaining counts calls.log search lines carrying
// substr — "after:2025-01-01" spots an initial-scan window; a catch-up
// scan's first window is the floor's overlap day instead.
func countSearchLinesContaining(t *testing.T, fx *ampFixture, substr string) int {
	t.Helper()
	n := 0
	for _, ln := range fx.searchCallLines(t) {
		if strings.Contains(ln, substr) {
			n++
		}
	}
	return n
}

// TestCommandAmpAccountSwitchRescans pins the account axis of the scope
// key end to end: swapping accounts.json's active user under the same
// install rescopes the checkpoint — the next sweep re-enumerates from
// the history floor rather than trusting the old account's clean
// coverage, and the old scope's file survives for a switch back.
func TestCommandAmpAccountSwitchRescans(t *testing.T) {
	fx, _, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if _, _, err := runCommand(t, "--harness", "amp"); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if paths := fx.scanStatePaths(t); len(paths) != 1 {
		t.Fatalf("scan-state files = %v, want one scope's checkpoint", paths)
	}
	if n := countSearchLinesContaining(t, fx, "after:2025-01-01"); n != 1 {
		t.Fatalf("floor-window searches after run 1 = %d, want 1 (the initial scan)", n)
	}

	// The account changes under the same install — a logout+login
	// elsewhere. The checkpoint rescopes: a second file, and the next
	// sweep scans from the floor again rather than trusting the old
	// account's clean coverage. (The thread itself is already journaled
	// — the rescan emits it and Unchanged keeps it quiet; the point is
	// the SCAN, not a recapture.)
	dataDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "amp")
	writeFile(t, filepath.Join(dataDir, "accounts.json"),
		[]byte(`{"version":1,"active":{"https://ampcode.com/":"user_other_account"}}`), 0o644)
	fx.resetAmp(t) // a new invocation is a new process — the scope recomputes

	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("post-switch sweep: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("post-switch sweep = %q — the thread is unchanged for THIS journal; only the scan rescopes", stdout)
	}
	if paths := fx.scanStatePaths(t); len(paths) != 2 {
		t.Fatalf("scan-state files = %v, want both scopes' checkpoints preserved", paths)
	}
	if n := countSearchLinesContaining(t, fx, "after:2025-01-01"); n != 2 {
		t.Fatalf("floor-window searches after the switch = %d, want 2 — the new account rescanned history", n)
	}
	// …and the old account's pending set was never probed under the new
	// scope: user_other_account's checkpoint is a fresh file holding its
	// own pending ids only — the id: lane exists per scope.
	for _, path := range fx.scanStatePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"scope"`) {
			t.Errorf("checkpoint %s carries no scope stamp", path)
		}
	}
}

// TestCommandAmpJournalSwitchRescans pins the journal axis of the scope
// key: a run pointed at a different journal root can never reuse this
// journal's scan progress — the new target gets its own checkpoint and
// its own full enumeration, so the same thread captures there fresh.
func TestCommandAmpJournalSwitchRescans(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	fx.addThread(t, "T-abc", "2026-08-30T10:00:00.000Z",
		ampExportDoc(t, "T-abc", ampDocSpec{UpdatedAt: "2026-08-30T10:00:00.000Z", CreatedMs: 1788064264680, Messages: 2}))
	if stdout, _, err := runCommand(t, "--harness", "amp"); err != nil ||
		strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("first capture: stdout=%q err=%v", stdout, err)
	}
	if _, ok := findAmpSession(t, journalDir, "T-abc"); !ok {
		t.Fatal("first journal lacks the session")
	}

	// The whole run repoints at a second journal — the checkpoint must
	// rescope (a second file) and the new target scans from the floor,
	// so the same thread captures into it.
	journal2 := t.TempDir()
	t.Setenv("CLAST_JOURNAL_DIR", journal2)
	fx.resetAmp(t) // a new invocation is a new process
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("second-journal sweep: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(stdout) != "captured amp-T-abc" {
		t.Fatalf("second journal stdout = %q — the new target's scan must not trust the old one's progress", stdout)
	}
	if paths := fx.scanStatePaths(t); len(paths) != 2 {
		t.Fatalf("scan-state files = %v, want both journal scopes' checkpoints", paths)
	}
	if _, ok := findAmpSession(t, journal2, "T-abc"); !ok {
		t.Fatal("second journal lacks the session")
	}
	if n := countSearchLinesContaining(t, fx, "after:2025-01-01"); n != 2 {
		t.Fatalf("floor-window searches = %d, want 2 — each journal got its own initial scan", n)
	}
}

// ampReaderDoc is a hand-authored export exercising every block shape
// the offline read surfaces: prompt text, thinking, a paired
// tool_use/tool_result, and prose — so show --transcript and the
// analyze transcript view prove the whole mapping on stored bytes.
const ampReaderDoc = `{
  "v": 9,
  "id": "T-read",
  "env": {"initial": {
    "platform": {"installationID": "` + ampTestInstallID + `"},
    "workingDirectory": "file:///tmp/amp-read"
  }},
  "meta": {
    "executorType": "local-client",
    "agentMode": "high",
    "lastKnownAgentState": {"state": "idle", "messageID": "M-4"}
  },
  "created": 1788064264680,
  "messages": [
    {"role": "user", "messageId": 1, "protocolMessageID": "M-1",
      "createdAt": "2026-08-30T09:59:00.000Z",
      "content": [{"type": "text", "text": "AMPREADER marker prompt"}]},
    {"role": "assistant", "messageId": 2, "protocolMessageID": "M-2",
      "state": {"type": "complete", "stopReason": "tool_use"},
      "usage": {"model": "gpt-5.6-sol"},
      "content": [
        {"type": "thinking", "thinking": "pondering AMPREADER"},
        {"type": "tool_use", "id": "TU-1", "name": "finder", "input": {"pattern": "x.go"}},
        {"type": "text", "text": "calling the finder"}]},
    {"role": "user", "messageId": 3, "protocolMessageID": "M-3",
      "content": [{"type": "tool_result", "toolUseID": "TU-1",
        "run": {"result": {"output": "x.go found", "exitCode": 0}, "status": "done"}}]},
    {"role": "assistant", "messageId": 4, "protocolMessageID": "M-4",
      "state": {"type": "complete", "stopReason": "end_turn"},
      "usage": {"model": "gpt-5.6-sol"},
      "content": [{"type": "text", "text": "done AMPREADER"}]}
  ],
  "updatedAt": "2026-08-30T10:00:00.000Z"
}`

// TestCommandAmpTranscriptReadOffline pins the step-06 contract end to
// end: once the session is captured, the stored transcript reads
// through session.json's own transcript.format + transcript.artifact
// with the transport gone entirely — amp off PATH, no fetch, capture
// excluded. `show --transcript` renders the prose turns; the analyze
// transcript view gathers the structured event stream; neither touches
// the harness.
func TestCommandAmpTranscriptReadOffline(t *testing.T) {
	fx, journalDir, configHome := newAmpFixture(t)
	fx.addThread(t, "T-read", "2026-08-30T10:00:00.000Z", []byte(ampReaderDoc))

	// Capture is excluded by config AND the opt-in is written in its
	// explicit-off spelling; the --harness run still lands the session
	// (authorization beats exclusion and the gate — see the selection
	// test). Both capture-side postures then stay live for the read
	// assertions below: neither exclusion nor an un-joined sweep gate
	// can reach the readers.
	writeConfig(t, configHome, "capture:\n  exclude: [amp]\n  amp:\n    auto: false\n")
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-read" {
		t.Fatalf("capture: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	item, ok := findAmpSession(t, journalDir, "T-read")
	if !ok {
		t.Fatal("amp-T-read not in the journal")
	}

	// Transport gone: the binary no longer resolves — Present is the
	// proof the "unavailable" side of the claim is real, not assumed.
	t.Setenv("PATH", t.TempDir())
	src, ok := sourceregistry.Lookup("amp")
	if !ok {
		t.Fatal("amp not in the registry")
	}
	if err := src.(source.Presence).Present(context.Background()); err == nil {
		t.Fatal("Present must fail with amp off PATH — the read below is then provably offline")
	}

	// show --transcript — the prose view, through the stored format's
	// renderer (not the harness name).
	turns, err := show.Transcript(journalDir, item, 0)
	if err != nil {
		t.Fatalf("show.Transcript offline: %v", err)
	}
	want := []source.Turn{
		{Role: "user", Text: "AMPREADER marker prompt"},
		{Role: "assistant", Text: "calling the finder"},
		{Role: "assistant", Text: "done AMPREADER"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v, want %+v (thinking/tools must not render)", turns, want)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Errorf("turns[%d] = %+v, want %+v", i, turns[i], want[i])
		}
	}

	// …and through the actual cobra verb, flags and all.
	var outBuf, errBuf bytes.Buffer
	cmd := show.Command(&iostreams.Streams{Out: &outBuf, Err: &errBuf})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetContext(cliflags.WithFlags(context.Background(), cliflags.Flags{}))
	cmd.SetArgs([]string{"amp-T-read", "--transcript"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("show --transcript: %v (stderr %q)", err, errBuf.String())
	}
	if got := outBuf.String(); !strings.Contains(got, "user: AMPREADER marker prompt") ||
		!strings.Contains(got, "assistant: done AMPREADER") {
		t.Errorf("show --transcript output = %q", got)
	}

	// analyze's transcript view — the structured reader on the same
	// stored bytes, artifacts resolved by transcript.artifact.
	page, err := analyzeverb.GatherTranscript(
		journal.SessionDir(journalDir, item.Shard, item.Key),
		analyzeverb.Session{ID: item.Key.DirName(), Day: journal.Day(item.Shard)},
		"",
	)
	if err != nil {
		t.Fatalf("GatherTranscript offline: %v", err)
	}
	var got []string
	var call *source.ToolCall
	for _, e := range page.Events {
		k := string(e.Kind)
		if e.Label != "" {
			k += ":" + e.Label
		}
		got = append(got, k)
		if e.Kind == source.KindToolCall {
			call = e.Tool
		}
	}
	wantKinds := "meta:agentMode prompt meta:model thinking tool_call assistant assistant meta:lastKnownAgentState"
	if strings.Join(got, " ") != wantKinds {
		t.Errorf("event kinds = %q, want %q", strings.Join(got, " "), wantKinds)
	}
	if call == nil || call.ID != "TU-1" || call.Name != "finder" ||
		call.Result == nil || call.Result.Text != "x.go found" || call.Result.IsError {
		t.Errorf("paired tool call = %+v", call)
	}
	if len(page.Subagents) != 0 {
		t.Errorf("Subagents = %+v — amp exports carry none; none may be invented", page.Subagents)
	}

	// The page renders through the shipped template — the whole read
	// path, not just event collection.
	var html bytes.Buffer
	if err := analyzeverb.RenderTranscript(&html, page); err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	for _, want := range []string{"AMPREADER marker prompt", "pondering AMPREADER", "x.go found", "agentMode"} {
		if !strings.Contains(html.String(), want) {
			t.Errorf("transcript page lacks %q", want)
		}
	}
}

func mustRFC(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// --------------------------------------------------------------------
// Step-09: the seeded multi-sweep convergence suite — every scenario the
// workplan lists, driven through the real command (or capture.Run for
// the concurrent pair), asserted only on observable outcomes: artifact
// bytes, session.json facts, curation state, checkpoint pending sets,
// stderr disclosure, and the calls.log fetch ledger.

// callLines returns every argv line the fake CLI has logged, in order —
// sweep-by-sweep deltas of this are the bounded-fetch evidence.
func (fx *ampFixture) callLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// exportCallsFor counts `threads export <id>` invocations — the exact
// fetch ledger for one thread (correlate memoization makes a committed
// doc exactly one call).
func (fx *ampFixture) exportCallsFor(t *testing.T, id string) int {
	t.Helper()
	n := 0
	for _, ln := range fx.callLines(t) {
		if ln == "threads export "+id {
			n++
		}
	}
	return n
}

// scanStateDoc is one parsed scope checkpoint.
type scanStateDoc struct {
	Path         string
	Scope        string
	CleanThrough string
	PendingIDs   []string
}

// scanStateDocs parses every scope checkpoint — post-account-switch the
// cache holds one file per scope, identified by the scope descriptor
// stamped inside it.
func (fx *ampFixture) scanStateDocs(t *testing.T) []scanStateDoc {
	t.Helper()
	var out []scanStateDoc
	for _, p := range fx.scanStatePaths(t) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Scope        string   `json:"scope"`
			CleanThrough string   `json:"clean_through"`
			PendingIDs   []string `json:"pending_ids"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("checkpoint %s must parse: %v", p, err)
		}
		out = append(out, scanStateDoc{p, doc.Scope, doc.CleanThrough, doc.PendingIDs})
	}
	return out
}

// docForScope returns the checkpoint whose scope descriptor carries
// substr — the account-scoped read once more than one scope file exists.
func (fx *ampFixture) docForScope(t *testing.T, substr string) scanStateDoc {
	t.Helper()
	var found []scanStateDoc
	for _, d := range fx.scanStateDocs(t) {
		if strings.Contains(d.Scope, substr) {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		t.Fatalf("checkpoints matching %q = %v, want exactly one", substr, found)
	}
	return found[0]
}

// backdateCleanThrough rewrites the one scope checkpoint's floor — the
// durable state a capture run from long ago leaves on disk. The scope
// stamp rides along untouched, so the next sweep reads it as this
// scope's own old progress: a catch-up from an ancient floor, not a
// fresh initial scan.
func (fx *ampFixture) backdateCleanThrough(t *testing.T, ts string) {
	t.Helper()
	paths := fx.scanStatePaths(t)
	if len(paths) != 1 {
		t.Fatalf("scan-state files = %v, want exactly one scope's checkpoint to backdate", paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["clean_through"] = ts
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, paths[0], raw, 0o644)
}

// removeCheckpoint deletes the scope checkpoint outright — the "crashed
// before the progress write" case: committed sessions on disk, no
// record of what the last run covered.
func (fx *ampFixture) removeCheckpoint(t *testing.T) {
	t.Helper()
	paths := fx.scanStatePaths(t)
	if len(paths) != 1 {
		t.Fatalf("scan-state files = %v, want exactly one scope's checkpoint to remove", paths)
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
}

// switchAccount rewrites accounts.json's active user and re-registers a
// fresh Source instance — a new `clast capture` process under a changed
// ambient credential.
func (fx *ampFixture) switchAccount(t *testing.T, user string) {
	t.Helper()
	dataDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "amp")
	writeFile(t, filepath.Join(dataDir, "accounts.json"),
		[]byte(`{"version":1,"active":{"https://ampcode.com/":"`+user+`"}}`), 0o644)
	fx.resetAmp(t)
}

// setList installs the `threads list` corroboration page — one
// {"id","messageCount"} row per id.
func (fx *ampFixture) setList(t *testing.T, ids ...string) {
	t.Helper()
	rows := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]any{"id": id, "messageCount": 2})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fx.home, "list.json"), raw, 0o644)
}

// ampSessions maps the journal's amp sessions by native id — and fails
// on a duplicate, since the whole point of the suite is that session
// identity never forks.
func ampSessions(t *testing.T, journalDir string) map[string]journal.WalkItem {
	t.Helper()
	items, _, err := journal.Walk(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]journal.WalkItem{}
	for _, it := range items {
		if it.Key.Harness != "amp" {
			continue
		}
		if _, dup := out[it.Key.NativeID]; dup {
			t.Fatalf("duplicate session identity for %s", it.Key.NativeID)
		}
		out[it.Key.NativeID] = it
	}
	return out
}

// ampLocalSpec builds the spec every sequence thread shares: this
// install, the registered repo as its working directory — a session
// whose project resolves at capture, so every later unchanged verdict
// is fetch-free end to end (no backfill fetch, no re-export).
func ampLocalSpec(repo, stamp string, msgs int) ampDocSpec {
	return ampDocSpec{
		InstallID: ampTestInstallID,
		WorkDir:   repo,
		UpdatedAt: stamp,
		CreatedMs: 1788064264680,
		Messages:  msgs,
	}
}

// ampEditedDoc renders an export doc then rewrites message texts in
// place — the same message count with different conversation bytes,
// which a messageCount-driven change check can never see.
func ampEditedDoc(t *testing.T, id string, spec ampDocSpec, repl map[string]string) []byte {
	t.Helper()
	s := string(ampExportDoc(t, id, spec))
	for old, nw := range repl {
		if !strings.Contains(s, old) {
			t.Fatalf("doc for %s lacks %q to replace", id, old)
		}
		s = strings.Replace(s, old, nw, 1)
	}
	return []byte(s)
}

// TestCommandAmpSeededSweepConvergence is the step-09 seeded sequence:
// one thread universe driven through twelve real sweeps plus a
// concurrent run pair, with every fault interleaved between them. The
// asserts are the observable tail — final artifact bytes and
// session.json facts per thread, curation preserved, no duplicate
// identity, the failed id's prior capture retained, the pending lane
// draining only through real witnesses, incomplete disclosure both ways,
// and an exact calls.log fetch ledger per thread.
func TestCommandAmpSeededSweepConvergence(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	repo := gitRepo(t)
	registerClone(t, journalDir, "demo", repo)

	const (
		s0     = "2026-08-30T10:00:00.000Z" // every seed doc's revision
		sEdit  = "2026-08-31T10:00:00.000Z" // T-edit's same-count text change
		sConv  = "2026-09-01T10:00:00.000Z" // T-partial's converged revision
		sChurn = "2026-09-01T11:00:00.000Z" // T-meta's metadata-only bump
		sFail  = "2026-09-02T10:00:00.000Z" // T-fail's moved revision
		sTie   = "2026-09-15T00:00:00.000Z" // the equal-updatedAt pair, on a day boundary
		sOut   = "2026-09-20T10:00:00.000Z" // T-outage-new: born while clast was down
		sAcct  = "2026-10-05T10:00:00.000Z" // T-acct-b: born under the other account
	)

	// The seed universe, all project-correlating locals.
	docSame := ampExportDoc(t, "T-same", ampLocalSpec(repo, s0, 2))
	docEdit0 := ampExportDoc(t, "T-edit", ampLocalSpec(repo, s0, 2))
	docEdit1 := ampEditedDoc(t, "T-edit", ampLocalSpec(repo, sEdit, 2), map[string]string{
		`"text": "message 1"`: `"text": "the edited prompt"`,
		`"text": "message 2"`: `"text": "the edited answer"`,
	})
	partialSpec := ampLocalSpec(repo, s0, 2)
	partialSpec.AgentState = "streaming"
	docPart0 := ampExportDoc(t, "T-partial", partialSpec)
	docPart1 := ampExportDoc(t, "T-partial", ampLocalSpec(repo, sConv, 4))
	metaSpec0 := ampLocalSpec(repo, s0, 2)
	metaSpec0.V = 7
	docMeta0 := ampExportDoc(t, "T-meta", metaSpec0)
	metaSpec1 := ampLocalSpec(repo, sChurn, 2)
	metaSpec1.V = 8
	docMeta1 := ampExportDoc(t, "T-meta", metaSpec1)
	docFail0 := ampExportDoc(t, "T-fail", ampLocalSpec(repo, s0, 2))
	docFail1 := ampExportDoc(t, "T-fail", ampLocalSpec(repo, sFail, 4))
	docTieA := ampExportDoc(t, "T-tie-a", ampLocalSpec(repo, sTie, 2))
	docTieB := ampExportDoc(t, "T-tie-b", ampLocalSpec(repo, sTie, 2))
	docIntr := ampExportDoc(t, "T-interrupt", ampLocalSpec(repo, s0, 2))

	fx.addThread(t, "T-same", s0, docSame)
	fx.addThread(t, "T-edit", s0, docEdit0)
	fx.addThread(t, "T-partial", s0, docPart0)
	fx.addThread(t, "T-meta", s0, docMeta0)
	fx.addThread(t, "T-fail", s0, docFail0)
	fx.addThread(t, "T-tie-a", sTie, docTieA)
	fx.addThread(t, "T-tie-b", sTie, docTieB)
	fx.addThread(t, "T-interrupt", s0, docIntr)

	// SWEEP 1 — the initial enumeration: everything commits, the flagged
	// mid-turn doc commits flagged, and every emitted id lands pending.
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("sweep 1: %v (stderr %q)", err, stderr)
	}
	if n := strings.Count(stdout, "captured amp-"); n != 8 {
		t.Fatalf("sweep 1 stdout = %q, want all 8 captured", stdout)
	}
	if !strings.Contains(stderr, "streaming") {
		t.Errorf("sweep 1 stderr = %q, want T-partial's mid-turn diagnostic", stderr)
	}
	sessions := ampSessions(t, journalDir)
	if len(sessions) != 8 {
		t.Fatalf("sessions = %d, want 8", len(sessions))
	}
	if item := sessions["T-partial"]; !item.Session.Incomplete {
		t.Error("T-partial's mid-turn commit must record incomplete")
	}
	for _, id := range []string{"T-same", "T-edit", "T-partial", "T-meta", "T-fail", "T-tie-a", "T-tie-b", "T-interrupt"} {
		item := sessions[id]
		if item.Session.Project == nil || item.Session.Project.Slug != "demo" {
			t.Errorf("%s did not correlate into the registered repo: %+v", id, item.Session.Project)
		}
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 8 {
		t.Fatalf("sweep 1 pending_ids = %v, want all 8 emitted ids owed", st.PendingIDs)
	}

	// Curate T-edit and T-meta before their bumps — the pair pins the
	// canonical fingerprint both ways: the text edit must stale the
	// recorded stamp, the metadata churn must not.
	curate := func(id string) {
		item := sessions[id]
		reason := "user tested"
		if err := journal.WriteCuration(journalDir, item.Shard, item.Key, journal.Curation{
			State:  journal.StateDismissed,
			Reason: &reason,
			TranscriptAtCuration: &journal.TranscriptStamp{
				Lines: item.Session.Transcript.Lines, SHA256: item.Session.Transcript.SHA256,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	curate("T-edit")
	curate("T-meta")
	shaEdit0 := sessions["T-edit"].Session.Transcript.SHA256
	shaMeta0 := sessions["T-meta"].Session.Transcript.SHA256
	failBytes0 := ampArtifact(t, journalDir, sessions["T-fail"])

	// Mutations between the sweeps: a same-count text edit, the
	// converged revision of the flagged doc, a metadata-only bump, a
	// moved revision whose export now fails, and a crash that left an
	// orphaned artifact (session.json never committed).
	fx.bumpThread(t, "T-edit", sEdit, docEdit1)
	fx.bumpThread(t, "T-partial", sConv, docPart1)
	fx.bumpThread(t, "T-meta", sChurn, docMeta1)
	fx.bumpThread(t, "T-fail", sFail, docFail1)
	fx.failExport(t, "T-fail", "Error: transport exploded")
	if err := os.Remove(journal.SessionJSONPath(journalDir,
		sessions["T-interrupt"].Shard, sessions["T-interrupt"].Key)); err != nil {
		t.Fatal(err)
	}

	// SWEEP 2 — the pending lane re-emits everything; the three honest
	// recaptures commit, the orphan heals fresh, and the failed export
	// discloses while its committed capture stands.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("sweep 2: %v (stderr %q)", err, stderr)
	}
	for _, want := range []string{
		"captured amp-T-interrupt", // the orphan recommits fresh
		"recaptured amp-T-edit",
		"recaptured amp-T-partial",
		"recaptured amp-T-meta",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("sweep 2 stdout = %q, missing %q", stdout, want)
		}
	}
	if strings.Count(stdout, "amp-") != 4 {
		t.Errorf("sweep 2 stdout = %q, want exactly those four lines", stdout)
	}
	if !strings.Contains(stderr, "T-fail") || !strings.Contains(stderr, "transport exploded") {
		t.Errorf("sweep 2 stderr = %q, want the T-fail diagnostic", stderr)
	}
	if !strings.Contains(stderr, "missing session.json") {
		t.Errorf("sweep 2 stderr = %q, want the orphan walk diagnostic", stderr)
	}

	sessions = ampSessions(t, journalDir)
	if got := ampArtifact(t, journalDir, sessions["T-edit"]); string(got) != string(docEdit1) {
		t.Error("T-edit: the same-count text edit did not commit the new bytes")
	}
	if s := sessions["T-edit"].Session; s.Transcript.Lines != 2 || s.Transcript.SHA256 == shaEdit0 {
		t.Errorf("T-edit: lines=%d sha moved=%v — the content fingerprint must fire on a same-count edit",
			s.Transcript.Lines, s.Transcript.SHA256 != shaEdit0)
	}
	if s := sessions["T-partial"].Session; s.Incomplete || s.Transcript.Lines != 4 {
		t.Errorf("T-partial after convergence: incomplete=%v lines=%d, want false/4", s.Incomplete, s.Transcript.Lines)
	}
	if got := ampArtifact(t, journalDir, sessions["T-partial"]); string(got) != string(docPart1) {
		t.Error("T-partial: the converged doc did not replace the flagged one")
	}
	if s := sessions["T-meta"].Session; s.Transcript.SHA256 != shaMeta0 {
		t.Error("T-meta: metadata-only churn moved the canonical fingerprint")
	}
	if got := ampArtifact(t, journalDir, sessions["T-meta"]); string(got) != string(docMeta1) {
		t.Error("T-meta: the churned verbatim bytes were not stored")
	}
	// The failed export left its committed record untouched.
	if s := sessions["T-fail"].Session; s.Transcript.Lines != 2 ||
		!s.LastActiveAt.Equal(mustRFC(t, s0)) {
		t.Errorf("T-fail's committed session moved under a failed read: %+v", s)
	}
	if got := ampArtifact(t, journalDir, sessions["T-fail"]); string(got) != string(failBytes0) {
		t.Error("T-fail's committed artifact was overwritten by the failed read")
	}
	if item := sessions["T-interrupt"]; item.Session.Transcript.Lines != 2 {
		t.Errorf("the orphaned session did not recommit: %+v", item.Session)
	}
	if st := fx.scanState(t); !slices.Equal(st.PendingIDs,
		[]string{"T-edit", "T-fail", "T-interrupt", "T-meta", "T-partial"}) {
		t.Errorf("sweep 2 pending_ids = %v, want the five still-unproven ids", st.PendingIDs)
	}

	// SWEEP 3 — the failing export fails again: idempotent, prior
	// records still preserved, the lane holds just that id.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 3: stdout=%q err=%v, want nothing committed", stdout, err)
	}
	if !strings.Contains(stderr, "T-fail") {
		t.Errorf("sweep 3 stderr = %q, want the failure disclosed again", stderr)
	}
	if st := fx.scanState(t); !slices.Equal(st.PendingIDs, []string{"T-fail"}) {
		t.Errorf("sweep 3 pending_ids = %v, want [T-fail]", st.PendingIDs)
	}
	if got := ampArtifact(t, journalDir, sessions["T-fail"]); string(got) != string(failBytes0) {
		t.Error("sweep 3 disturbed the failed thread's committed artifact")
	}

	// SWEEP 4 — the export heals; the pending id recaptures its moved
	// revision on the retry lane alone (its stamp is below the floor).
	fx.unfailExport(t, "T-fail")
	stdout, _, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "recaptured amp-T-fail" {
		t.Fatalf("sweep 4 (retry lane): stdout=%q err=%v", stdout, err)
	}
	sessions = ampSessions(t, journalDir)
	if got := ampArtifact(t, journalDir, sessions["T-fail"]); string(got) != string(docFail1) {
		t.Error("T-fail: the healed retry did not land the moved revision")
	}

	// SWEEP 5 — the settled tail: every id has met a witness; nothing
	// is owed and nothing refetches.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 5: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Fatalf("sweep 5 pending_ids = %v, want drained", st.PendingIDs)
	}

	// SWEEP 6 — restart after an outage longer than any window: the
	// durable checkpoint is dated 2025-06-01, so the sweep must catch up
	// from that floor (not rescan from the history floor, not skip the
	// gap) and pick up the thread born while clast was down.
	fx.backdateCleanThrough(t, "2025-06-01T00:00:00Z")
	docOut := ampExportDoc(t, "T-outage-new", ampLocalSpec(repo, sOut, 2))
	fx.addThread(t, "T-outage-new", sOut, docOut)
	prevCalls := len(fx.callLines(t))
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-outage-new" {
		t.Fatalf("sweep 6 (outage catch-up): stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	var sawFloorWindow, sawInitialWindow bool
	for _, ln := range fx.callLines(t)[prevCalls:] {
		if strings.HasPrefix(ln, "threads search ") {
			if strings.Contains(ln, "after:2025-05-31") {
				sawFloorWindow = true
			}
			if strings.Contains(ln, "after:2025-01-01") {
				sawInitialWindow = true
			}
		}
	}
	if !sawFloorWindow || sawInitialWindow {
		t.Errorf("sweep 6 searches: floor-window=%v initial-window=%v — must catch up from the durable floor, not reset",
			sawFloorWindow, sawInitialWindow)
	}
	if st := fx.scanState(t); !slices.Equal(st.PendingIDs, []string{"T-outage-new"}) {
		t.Errorf("sweep 6 pending_ids = %v, want only the new commit owed", st.PendingIDs)
	}

	// SWEEP 7 — the outage catch-up settles: quiet, pending empty.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 7: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Fatalf("sweep 7 pending_ids = %v, want drained", st.PendingIDs)
	}

	// SWEEP 8 — the other interruption: the run died after the sessions
	// committed but before the progress write. The checkpoint is simply
	// gone, so the sweep rescans from the history floor — and the
	// committed sessions all prove out unchanged, no duplicates.
	fx.removeCheckpoint(t)
	prevCalls = len(fx.callLines(t))
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 8 (lost progress): stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	var rescan bool
	for _, ln := range fx.callLines(t)[prevCalls:] {
		if strings.HasPrefix(ln, "threads search ") && strings.Contains(ln, "after:2025-01-01") {
			rescan = true
		}
	}
	if !rescan {
		t.Error("sweep 8 must rescan from the history floor once progress is lost")
	}
	if st := fx.scanState(t); len(st.PendingIDs) != 0 {
		t.Errorf("sweep 8 pending_ids = %v, want everything drained by read-back", st.PendingIDs)
	}
	if n := len(ampSessions(t, journalDir)); n != 9 {
		t.Errorf("sessions = %d, want 9 — the rescan must not duplicate identities", n)
	}
	if n := fx.exportCallsFor(t, "T-same"); n != 1 {
		t.Errorf("T-same export calls = %d, want 1 — the unchanged thread is never refetched", n)
	}

	// The account changes mid-sequence. Scope A keeps a pending ghost
	// (foreign work a racing run queued) — under the new account's scope
	// it must never be probed or inherited, and scope A's file must not
	// be touched by B's sweeps.
	fx.seedPending(t, "T-ghost-pending")
	var scopeAPath string
	var scopeABytes []byte
	for _, d := range fx.scanStateDocs(t) {
		if strings.Contains(d.Scope, "user_amp_test") {
			scopeAPath, scopeABytes = d.Path, mustRead(t, d.Path)
		}
	}
	if scopeAPath == "" {
		t.Fatal("no checkpoint stamped for the first account's scope")
	}
	fx.switchAccount(t, "user_other_account")
	docAcctB := ampExportDoc(t, "T-acct-b", ampLocalSpec(repo, sAcct, 2))
	fx.addThread(t, "T-acct-b", sAcct, docAcctB)

	// SWEEP 9 — scope B's first run is an initial rescan of the whole
	// catalog: committed sessions prove unchanged under the new scope,
	// and only the new thread captures.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || strings.TrimSpace(stdout) != "captured amp-T-acct-b" {
		t.Fatalf("sweep 9 (scope B): stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	var scopeB *scanStateDoc
	for _, d := range fx.scanStateDocs(t) {
		if strings.Contains(d.Scope, "user_other_account") {
			dd := d
			scopeB = &dd
		}
	}
	if scopeB == nil {
		t.Fatal("no checkpoint written for the second account's scope")
	}
	for _, id := range scopeB.PendingIDs {
		if id == "T-ghost-pending" {
			t.Error("scope B inherited scope A's pending ghost — cross-scope reuse")
		}
	}
	if data := mustRead(t, scopeAPath); string(data) != string(scopeABytes) {
		t.Error("scope B's sweep rewrote scope A's checkpoint")
	}

	// SWEEP 10 — scope B settles its own pending lane.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 10: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	for _, d := range fx.scanStateDocs(t) {
		if strings.Contains(d.Scope, "user_other_account") && len(d.PendingIDs) != 0 {
			t.Errorf("scope B pending_ids = %v, want drained", d.PendingIDs)
		}
	}

	// SWEEP 11 — switching back resumes scope A's file untouched: its
	// ghost is probed once on the id: lane, comes back clean-empty
	// (the thread left the universe), and drains.
	fx.switchAccount(t, "user_amp_test")
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 11 (scope A resume): stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	stA := fx.docForScope(t, "user_amp_test")
	if len(stA.PendingIDs) != 0 {
		t.Errorf("sweep 11 pending_ids = %v — the ghost should drain on the clean-empty id: page", stA.PendingIDs)
	}
	if data := mustRead(t, scopeAPath); strings.Contains(string(data), "T-ghost-pending") {
		t.Error("the ghost id survived its clean-empty id: verdict")
	}
	if stA.CleanThrough <= "2025-06-02" {
		t.Errorf("clean_through = %q, want the catch-up floor well past the backdated outage", stA.CleanThrough)
	}

	// Overlapping runs: two concurrent Run invocations — two Source
	// instances over one journal and one scope file — racing on a fresh
	// thread stamped right now.
	stampConc := time.Now().UTC().Format(time.RFC3339Nano)
	docConc := ampExportDoc(t, "T-conc", ampLocalSpec(repo, stampConc, 2))
	fx.addThread(t, "T-conc", stampConc, docConc)
	deps := func(src *amp.Source) Deps {
		return Deps{Root: journalDir, Sources: []source.Source{src}, Now: time.Now}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	failures := make([][]SourceFailure, 2)
	for i, src := range []*amp.Source{amp.New(), amp.New()} {
		wg.Add(1)
		go func(i int, src *amp.Source) {
			defer wg.Done()
			_, _, failures[i], errs[i] = Run(context.Background(), deps(src))
		}(i, src)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil || len(failures[i]) != 0 {
			t.Fatalf("concurrent run %d: err=%v failures=%v", i, errs[i], failures[i])
		}
	}
	if locks, _ := filepath.Glob(filepath.Join(fx.cache, "clast", "amp", "*.lock")); len(locks) != 0 {
		t.Errorf("lock files left behind by the concurrent runs: %v", locks)
	}
	sessions = ampSessions(t, journalDir)
	if item, ok := sessions["T-conc"]; !ok || item.Session.Transcript.Lines != 2 {
		t.Fatalf("T-conc not committed exactly once under contention: %+v", item.Session)
	}
	if n := fx.exportCallsFor(t, "T-conc"); n < 1 || n > 2 {
		t.Errorf("T-conc export calls = %d, want 1 or 2 (one per racing instance)", n)
	}

	// SWEEP 12 — the last settle: T-conc drains.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 12: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	for _, d := range fx.scanStateDocs(t) {
		if len(d.PendingIDs) != 0 {
			t.Errorf("final pending_ids for %s = %v, want empty", d.Scope, d.PendingIDs)
		}
	}

	// The terminal ledger: one session per id, final bytes and facts
	// exactly the last converged doc of each — and the per-id export
	// ledger is exact, so no unbounded or phantom refetch can hide.
	wantDocs := map[string][]byte{
		"T-same": docSame, "T-edit": docEdit1, "T-partial": docPart1,
		"T-meta": docMeta1, "T-fail": docFail1,
		"T-tie-a": docTieA, "T-tie-b": docTieB, "T-interrupt": docIntr,
		"T-outage-new": docOut, "T-acct-b": docAcctB, "T-conc": docConc,
	}
	wantStamps := map[string]string{
		"T-same": s0, "T-edit": sEdit, "T-partial": sConv,
		"T-meta": sChurn, "T-fail": sFail,
		"T-tie-a": sTie, "T-tie-b": sTie, "T-interrupt": s0,
		"T-outage-new": sOut, "T-acct-b": sAcct, "T-conc": stampConc,
	}
	wantLines := map[string]int{
		"T-same": 2, "T-edit": 2, "T-partial": 4, "T-meta": 2, "T-fail": 4,
		"T-tie-a": 2, "T-tie-b": 2, "T-interrupt": 2,
		"T-outage-new": 2, "T-acct-b": 2, "T-conc": 2,
	}
	wantExports := map[string]int{
		"T-same": 1, "T-edit": 2, "T-partial": 2, "T-meta": 2, "T-fail": 4,
		"T-tie-a": 1, "T-tie-b": 1, "T-interrupt": 2, // the orphan heal re-reads — the one-entry memo cannot serve a cold commit
		"T-outage-new": 1, "T-acct-b": 1, "T-conc": 2,
	}
	sessions = ampSessions(t, journalDir)
	if len(sessions) != len(wantDocs) {
		t.Fatalf("final sessions = %d, want %d", len(sessions), len(wantDocs))
	}
	for id, doc := range wantDocs {
		item, ok := sessions[id]
		if !ok {
			t.Errorf("%s: no committed session", id)
			continue
		}
		s := item.Session
		if got := ampArtifact(t, journalDir, item); string(got) != string(doc) {
			t.Errorf("%s: final artifact is not the converged export's bytes", id)
		}
		if s.Transcript.Lines != wantLines[id] || s.Incomplete ||
			!s.LastActiveAt.Equal(mustRFC(t, wantStamps[id])) ||
			s.SourcePath != "https://ampcode.com/threads/"+id {
			t.Errorf("%s: final session facts = %+v", id, s)
		}
		if n := fx.exportCallsFor(t, id); n != wantExports[id] {
			t.Errorf("%s: export calls = %d, want %d", id, n, wantExports[id])
		}
	}

	// Curation survived the whole sequence — and the fingerprint pair
	// reads correctly through it: the text edit staled its recorded
	// stamp; the metadata churn did not.
	curEdit, ok, err := journal.ReadCuration(journalDir, sessions["T-edit"].Shard, sessions["T-edit"].Key)
	if err != nil || !ok || curEdit.State != journal.StateDismissed {
		t.Fatalf("T-edit curation lost: present=%v cur=%+v err=%v", ok, curEdit, err)
	}
	if curEdit.TranscriptAtCuration == nil ||
		curEdit.TranscriptAtCuration.SHA256 == sessions["T-edit"].Session.Transcript.SHA256 {
		t.Error("T-edit must read stale against its curation stamp — the content fingerprint moved")
	}
	curMeta, ok, err := journal.ReadCuration(journalDir, sessions["T-meta"].Shard, sessions["T-meta"].Key)
	if err != nil || !ok || curMeta.State != journal.StateDismissed {
		t.Fatalf("T-meta curation lost: present=%v cur=%+v err=%v", ok, curMeta, err)
	}
	if curMeta.TranscriptAtCuration == nil ||
		curMeta.TranscriptAtCuration.SHA256 != sessions["T-meta"].Session.Transcript.SHA256 {
		t.Error("T-meta must not read stale — metadata churn never moves the fingerprint")
	}
}

// mustRead is the no-comment file read the sequence asserts with.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCommandAmpCappedPageConvergesThroughPending pins the second-page
// residual through the real command: 107 threads share one equal
// updatedAt stamp, so the 100-row silent cap subdivides down to a leaf
// that still overflows — the unenumerable remainder is disclosed (never
// lost: corroboration queues it into pending) and the id: lane captures
// it next sweep. Equal stamps under a capped page lose nothing and
// duplicate nothing.
func TestCommandAmpCappedPageConvergesThroughPending(t *testing.T) {
	fx, journalDir, _ := newAmpFixture(t)
	repo := gitRepo(t)
	registerClone(t, journalDir, "demo", repo)

	const sPage = "2026-09-15T08:00:00.000Z" // every row equal — mid-day so the dirty floor still passes them
	const herd = 107
	docs := map[string][]byte{}
	var ids []string
	for i := 0; i < herd; i++ {
		id := fmt.Sprintf("T-pg-%03d", i)
		docs[id] = ampExportDoc(t, id, ampLocalSpec(repo, sPage, 2))
		fx.addThread(t, id, sPage, docs[id])
		ids = append(ids, id)
	}
	fx.setList(t, ids...) // corroboration sees the whole universe

	// SWEEP 1 — the capped leaf absorbs the catalog's first 100 and the
	// corroboration pass flags the other 7 into the pending lane.
	stdout, stderr, err := runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("sweep 1: %v (stderr %q)", err, stderr)
	}
	if n := strings.Count(stdout, "captured amp-"); n != 100 {
		t.Fatalf("sweep 1 captured = %d, want the capped page's 100", n)
	}
	if !strings.Contains(stderr, "unenumerable") {
		t.Errorf("sweep 1 stderr = %q, want the capped-leaf diagnostic", stderr)
	}
	if !strings.Contains(stderr, "T-pg-106") || !strings.Contains(stderr, "not enumerated") {
		t.Errorf("sweep 1 stderr = %q, want the corroboration-miss diagnostics", stderr)
	}
	if st := fx.scanState(t); len(st.PendingIDs) != herd {
		t.Fatalf("sweep 1 pending_ids = %d ids, want all 107 owed", len(st.PendingIDs))
	}

	// SWEEP 2 — the id: lane emits the 7 the page never could; they
	// capture. The 100 committed prove unchanged on the same pass.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil {
		t.Fatalf("sweep 2: %v (stderr %q)", err, stderr)
	}
	if n := strings.Count(stdout, "captured amp-"); n != 7 {
		t.Fatalf("sweep 2 captured = %d, want the 7 the page dropped", n)
	}
	for _, id := range []string{"T-pg-100", "T-pg-104", "T-pg-106"} {
		if !strings.Contains(stdout, id) {
			t.Errorf("sweep 2 stdout missing %s: %q", id, stdout)
		}
	}

	// SWEEP 3 — convergence: the lane re-proves what the page can't
	// enumerate. The residual is honest forever — the capped day keeps
	// its diagnostic and the 7 committed-but-unenumerable ids stay
	// queued — but nothing is ever lost or refetched.
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 3: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	prevCalls := len(fx.callLines(t))
	stdout, stderr, err = runCommand(t, "--harness", "amp")
	if err != nil || stdout != "" {
		t.Fatalf("sweep 4: stdout=%q stderr=%q err=%v, want quiet", stdout, stderr, err)
	}
	if n := len(fx.callLines(t)) - prevCalls; n > 40 {
		t.Errorf("steady-state sweep made %d CLI calls — the residual must stay bounded", n)
	}
	if n := fx.callCount(t, "export"); n != herd {
		t.Errorf("export calls = %d, want exactly %d — every thread fetched once, none refetched", n, herd)
	}

	// The residual pending set is exactly the ids the capped leaf can
	// never enumerate — committed, re-verified each sweep, never lost.
	st := fx.scanState(t)
	wantPending := []string{"T-pg-100", "T-pg-101", "T-pg-102", "T-pg-103", "T-pg-104", "T-pg-105", "T-pg-106"}
	if !slices.Equal(st.PendingIDs, wantPending) {
		t.Errorf("residual pending_ids = %v, want the capped-out seven %v", st.PendingIDs, wantPending)
	}

	// Every session committed exactly once, bytes verbatim.
	sessions := ampSessions(t, journalDir)
	if len(sessions) != herd {
		t.Fatalf("sessions = %d, want %d — no loss, no duplicates", len(sessions), herd)
	}
	for id, doc := range docs {
		if got := ampArtifact(t, journalDir, sessions[id]); string(got) != string(doc) {
			t.Errorf("%s: artifact is not the verbatim export", id)
		}
	}
}
