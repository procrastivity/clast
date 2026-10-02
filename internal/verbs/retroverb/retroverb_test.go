package retroverb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
	"github.com/procrastivity/clast/internal/progress"
	retroplumbing "github.com/procrastivity/clast/internal/verbs/retro"
	"github.com/procrastivity/clast/internal/verbs/retroverb"
)

func cfgWith(baseURL, model string) config.Config {
	return config.Config{
		"llm": map[string]any{
			"base_url": baseURL,
			"model":    model,
		},
	}
}

// oneEntryResult builds a minimal retroplumbing.Result with exactly one
// project group carrying one session with an entry body — enough to
// drive Summarize/Fold end to end.
func oneEntryResult(day journal.Day, body string) retroplumbing.Result {
	started := time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC)
	item := journal.WalkItem{
		Key: journal.SessionKey{Harness: "claude", NativeID: "sess-01"},
		Session: journal.Session{
			Harness: "claude", SessionID: "sess-01", Branch: "go",
			StartedAt: started, Counts: journal.SessionCounts{User: 3, Assistant: 4},
		},
		CurationPresent: true,
		Curation:        journal.Curation{State: journal.StateCurated},
	}
	row := retroplumbing.EntryRow{
		Item: item,
		Day:  day,
		Entry: entry.Entry{
			Frontmatter: entry.Frontmatter{Title: "fixed the wake ordering bug", Tags: []string{"bugfix"}},
			Body:        body,
		},
	}
	return retroplumbing.Result{
		Day:         day,
		WindowStart: day,
		Groups: []retroplumbing.ProjectGroup{
			{
				Slug:     "widget",
				Sessions: []retroplumbing.SessionRow{{Item: item, Day: day, Title: "fixed the wake ordering bug"}},
				Entries:  []retroplumbing.EntryRow{row},
			},
		},
	}
}

func TestHasEntries(t *testing.T) {
	withEntry := oneEntryResult(journal.Day("2026-09-11"), "body")
	if !retroverb.HasEntries(withEntry) {
		t.Error("HasEntries = false, want true (one group carries an entry)")
	}

	empty := retroplumbing.Result{Day: journal.Day("2026-09-11"), WindowStart: journal.Day("2026-09-11")}
	if retroverb.HasEntries(empty) {
		t.Error("HasEntries = true, want false (no groups at all)")
	}

	noEntryGroup := retroplumbing.Result{
		Day: journal.Day("2026-09-11"), WindowStart: journal.Day("2026-09-11"),
		Groups: []retroplumbing.ProjectGroup{{Slug: "widget", Sessions: []retroplumbing.SessionRow{{}}}},
	}
	if retroverb.HasEntries(noEntryGroup) {
		t.Error("HasEntries = true, want false (a session with no entry body)")
	}
}

// TestSummarize_HappyPath_RequestCarriesRenderedPair confirms Summarize
// calls the endpoint once for a single entry-bearing session and fills
// the rendered user prompt from that entry's own facts.
func TestSummarize_HappyPath_RequestCarriesRenderedPair(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: fixed the bug")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "Tracked down groupByProject sorting by first appearance instead of recency.")

	summaries, stats, err := retroverb.Summarize(context.Background(), result, client, t.TempDir(), false)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %+v, want exactly 1", summaries)
	}
	if got := summaries["claude-sess-01"]; got != "- Shipped: fixed the bug" {
		t.Errorf("summary = %q, want the stub's canned completion", got)
	}
	if stats.Requests != 1 || stats.CacheHits != 0 {
		t.Errorf("stats = %+v, want 1 request, 0 hits", stats)
	}

	reqs := stub.Requests()
	if len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1", len(reqs))
	}
	user := reqs[0].Messages[1].Content
	for _, want := range []string{"widget", "sess-01", "2026-09-11", "groupByProject sorting"} {
		if !strings.Contains(user, want) {
			t.Errorf("rendered user prompt does not contain %q:\n%s", want, user)
		}
	}
}

// TestSummarize_CacheHit_SecondRunMakesNoRequest drives Summarize twice
// against the same cache directory: the first run calls the endpoint and
// stores the result, the second run must produce the identical summary
// with zero further requests (V11: "an unchanged entry is not
// re-summarized on a later run").
func TestSummarize_CacheHit_SecondRunMakesNoRequest(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: fixed the bug")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	cacheDir := t.TempDir()
	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "unchanged body")

	if _, stats, err := retroverb.Summarize(context.Background(), result, client, cacheDir, false); err != nil {
		t.Fatalf("Summarize (first run): %v", err)
	} else if stats.Requests != 1 {
		t.Fatalf("first run stats = %+v, want 1 request", stats)
	}

	// Fail every subsequent request — the second run must never dial in.
	stub.Fail(500, "must never be called on a cache hit")

	summaries, stats, err := retroverb.Summarize(context.Background(), result, client, cacheDir, false)
	if err != nil {
		t.Fatalf("Summarize (second run): %v", err)
	}
	if stats.Requests != 0 || stats.CacheHits != 1 {
		t.Errorf("second run stats = %+v, want 0 requests, 1 hit", stats)
	}
	if got := summaries["claude-sess-01"]; got != "- Shipped: fixed the bug" {
		t.Errorf("second run summary = %q, want the cached completion", got)
	}
	if reqs := stub.Requests(); len(reqs) != 1 {
		t.Fatalf("stub captured %d requests total, want exactly 1 (only the first run)", len(reqs))
	}
}

// TestSummarize_Refresh_ForcesRequestAndRewritesCache confirms --refresh
// (the refresh bool) bypasses an existing cache hit, calls the endpoint
// again, and rewrites the cache entry with the new completion.
func TestSummarize_Refresh_ForcesRequestAndRewritesCache(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: first version")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	cacheDir := t.TempDir()
	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "unchanged body")

	if _, _, err := retroverb.Summarize(context.Background(), result, client, cacheDir, false); err != nil {
		t.Fatalf("Summarize (seed run): %v", err)
	}

	stub.SetResponse("- Shipped: refreshed version")
	summaries, stats, err := retroverb.Summarize(context.Background(), result, client, cacheDir, true)
	if err != nil {
		t.Fatalf("Summarize (refresh run): %v", err)
	}
	if stats.Requests != 1 || stats.CacheHits != 0 {
		t.Errorf("refresh run stats = %+v, want 1 request, 0 hits", stats)
	}
	if got := summaries["claude-sess-01"]; got != "- Shipped: refreshed version" {
		t.Errorf("refresh run summary = %q, want the refreshed completion", got)
	}
	if reqs := stub.Requests(); len(reqs) != 2 {
		t.Fatalf("stub captured %d requests, want exactly 2 (seed + refresh)", len(reqs))
	}

	// A later, non-refresh run must now hit the rewritten cache entry.
	stub.Fail(500, "must never be called once the refreshed entry is cached")
	summaries, stats, err = retroverb.Summarize(context.Background(), result, client, cacheDir, false)
	if err != nil {
		t.Fatalf("Summarize (post-refresh run): %v", err)
	}
	if stats.Requests != 0 || stats.CacheHits != 1 {
		t.Errorf("post-refresh run stats = %+v, want 0 requests, 1 hit", stats)
	}
	if got := summaries["claude-sess-01"]; got != "- Shipped: refreshed version" {
		t.Errorf("post-refresh run summary = %q, want the rewritten cached completion", got)
	}
}

// TestSummarize_CorruptCacheFile_TreatedAsMiss plants an unparsable file
// at the exact path a real cache entry would occupy (computed the same
// way production code does: render the same prompt pair and fingerprint
// it) and confirms Summarize treats it as a miss — a fresh request is
// made rather than the run failing.
func TestSummarize_CorruptCacheFile_TreatedAsMiss(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: fresh despite corruption")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	cacheDir := t.TempDir()
	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "unchanged body")

	// Prime the cache directory, then corrupt the one file it wrote.
	if _, _, err := retroverb.Summarize(context.Background(), result, client, cacheDir, false); err != nil {
		t.Fatalf("Summarize (seed run): %v", err)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", cacheDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache dir has %d entries, want exactly 1", len(entries))
	}
	cachedPath := filepath.Join(cacheDir, entries[0].Name())
	if err := os.WriteFile(cachedPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupting cache file: %v", err)
	}

	stub.SetResponse("- Shipped: fresh despite corruption")
	summaries, stats, err := retroverb.Summarize(context.Background(), result, client, cacheDir, false)
	if err != nil {
		t.Fatalf("Summarize (after corruption): %v", err)
	}
	if stats.Requests != 1 || stats.CacheHits != 0 {
		t.Errorf("stats after corruption = %+v, want 1 request, 0 hits (miss, not an error)", stats)
	}
	if got := summaries["claude-sess-01"]; got != "- Shipped: fresh despite corruption" {
		t.Errorf("summary after corruption = %q, want the freshly computed completion", got)
	}
}

// TestSummarize_ProjectBackfill_StillCacheHit pins the backfill case: a
// session first summarized in the no-project bucket, then resolved to a
// real project by plumbing capture's backfill (entry untouched), must be
// served from cache rather than re-summarized.
func TestSummarize_ProjectBackfill_StillCacheHit(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: fixed the bug")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	cacheDir := t.TempDir()
	day := journal.Day("2026-09-11")
	before := oneEntryResult(day, "unchanged body")
	before.Groups[0].Slug = "-"

	if _, stats, err := retroverb.Summarize(context.Background(), before, client, cacheDir, false); err != nil {
		t.Fatalf("Summarize (before backfill): %v", err)
	} else if stats.Requests != 1 {
		t.Fatalf("before-backfill stats = %+v, want 1 request", stats)
	}
	if user := stub.Requests()[0].Messages[1].Content; !strings.Contains(user, "(no project)") {
		t.Errorf("before-backfill prompt does not name (no project):\n%s", user)
	}

	stub.Fail(500, "must never be called after a project backfill")

	after := oneEntryResult(day, "unchanged body")
	summaries, stats, err := retroverb.Summarize(context.Background(), after, client, cacheDir, false)
	if err != nil {
		t.Fatalf("Summarize (after backfill): %v", err)
	}
	if stats.Requests != 0 || stats.CacheHits != 1 {
		t.Errorf("after-backfill stats = %+v, want 0 requests, 1 hit", stats)
	}
	if got := summaries["claude-sess-01"]; got != "- Shipped: fixed the bug" {
		t.Errorf("after-backfill summary = %q, want the cached completion", got)
	}
}

// TestSummarize_ManyEntries_AllSummarized drives more entries than the
// worker pool's width, so every worker runs more than once: every entry
// must come back summarized, with one request each.
func TestSummarize_ManyEntries_AllSummarized(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: something")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "body")
	g := &result.Groups[0]
	base := g.Entries[0]
	g.Entries = nil
	const n = 11
	for i := range n {
		row := base
		id := fmt.Sprintf("sess-%02d", i)
		row.Item.Key.NativeID = id
		row.Item.Session.SessionID = id
		row.Entry.Body = "body " + id
		g.Entries = append(g.Entries, row)
	}

	summaries, stats, err := retroverb.Summarize(context.Background(), result, client, t.TempDir(), false)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(summaries) != n || stats.Requests != n || stats.CacheHits != 0 {
		t.Errorf("got %d summaries, stats %+v; want %d summaries, %d requests", len(summaries), stats, n, n)
	}
	if reqs := stub.Requests(); len(reqs) != n {
		t.Errorf("stub captured %d requests, want %d", len(reqs), n)
	}
}

// TestSummarize_NoReporter_Succeeds covers the nil-reporter path, which is
// the e2e path: a context with no reporter must summarize as before.
func TestSummarize_NoReporter_Succeeds(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: something")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	result := oneEntryResult(journal.Day("2026-09-11"), "body")
	summaries, stats, err := retroverb.Summarize(context.Background(), result, client, t.TempDir(), false)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(summaries) != 1 || stats.Requests != 1 {
		t.Errorf("got %d summaries, stats %+v; want 1 summary, 1 request", len(summaries), stats)
	}
}

// TestSummarize_WithReporter_Succeeds runs Summarize with a live reporter
// (stderr on /dev/null, a character device) over a cold run and a cached
// run, under -race, to cover the reporter calls from the worker goroutines.
func TestSummarize_WithReporter_Succeeds(t *testing.T) {
	stub := llmtest.New(t, "- Shipped: something")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	t.Setenv("TERM", "xterm")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer func() { _ = null.Close() }()
	rep := progress.New(&iostreams.Streams{Err: null}, cliflags.Flags{})
	if rep == nil {
		t.Skip("/dev/null is not a character device here")
	}
	defer rep.Stop()
	ctx := progress.WithReporter(context.Background(), rep)

	result := oneEntryResult(journal.Day("2026-09-11"), "body")
	cacheDir := t.TempDir()
	for i, wantHits := range []int{0, 1} {
		_, stats, err := retroverb.Summarize(ctx, result, client, cacheDir, false)
		if err != nil {
			t.Fatalf("Summarize (run %d): %v", i, err)
		}
		if stats.CacheHits != wantHits {
			t.Errorf("run %d stats = %+v, want %d cache hit(s)", i, stats, wantHits)
		}
	}
}

// TestSummarize_LLMRequestFailure_WrapsAsRetroCode confirms a failed
// request surfaces as retro.llm-request-failed (mirroring briefverb's own
// TestSynthesize_LLMRequestFailure_WrapsAsBriefCode) — this code had no
// test anywhere before llm-verbs/step-07's seal sweep, unlike brief's and
// wake's own equivalents.
func TestSummarize_LLMRequestFailure_WrapsAsRetroCode(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.Fail(500, `{"error": "boom"}`)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "body")

	_, _, err = retroverb.Summarize(context.Background(), result, client, t.TempDir(), false)
	if err == nil {
		t.Fatal("Summarize: want error, got nil")
	}
	var terr *clasterr.Error
	if !errors.As(err, &terr) {
		t.Fatalf("Summarize error is not a *clasterr.Error: %v (%T)", err, err)
	}
	if terr.Code != "retro.llm-request-failed" {
		t.Errorf("Code = %q, want %q", terr.Code, "retro.llm-request-failed")
	}
}

// TestFold_AttachesSummaryPreservesOrdering confirms Fold attaches each
// summary to its own EntryRow without disturbing the group's existing
// Sessions/Breadcrumbs, and that a session with no entry body (absent
// from the summaries map) folds in with an empty Summary rather than
// erroring.
func TestFold_AttachesSummaryPreservesOrdering(t *testing.T) {
	day := journal.Day("2026-09-11")
	result := oneEntryResult(day, "body")
	summaries := map[string]string{"claude-sess-01": "- Shipped: the fix"}

	doc := retroverb.Fold(result, summaries)
	if len(doc.Groups) != 1 || doc.Groups[0].Slug != "widget" {
		t.Fatalf("doc.Groups = %+v, want one group %q", doc.Groups, "widget")
	}
	g := doc.Groups[0]
	if len(g.Entries) != 1 || g.Entries[0].Summary != "- Shipped: the fix" {
		t.Fatalf("g.Entries = %+v, want one folded entry carrying the summary", g.Entries)
	}
	if len(g.Sessions) != 1 || g.Sessions[0].Title != "fixed the wake ordering bug" {
		t.Errorf("g.Sessions = %+v, want the original session row untouched", g.Sessions)
	}
}

// TestSummaryPromptData_JSONRoundTripSmoke is a light guard confirming
// the request body the stub captures is valid JSON with the right
// message roles — mirrors briefverb's own request-shape assertions.
func TestSummaryPromptData_JSONRoundTripSmoke(t *testing.T) {
	stub := llmtest.New(t, "ok")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}
	result := oneEntryResult(journal.Day("2026-09-11"), "body")
	if _, _, err := retroverb.Summarize(context.Background(), result, client, t.TempDir(), false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	reqs := stub.Requests()
	if len(reqs) != 1 || reqs[0].Messages[0].Role != "system" || reqs[0].Messages[1].Role != "user" {
		var b []byte
		b, _ = json.Marshal(reqs)
		t.Fatalf("requests = %s, want [system, user] messages", b)
	}
}
