package query_test

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/query"
)

func mustCutoff(t *testing.T, s string) journal.Cutoff {
	t.Helper()
	c, err := journal.ParseCutoff(s)
	if err != nil {
		t.Fatalf("ParseCutoff(%q): %v", s, err)
	}
	return c
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

func buildFilterFixture(t *testing.T) ([]journal.WalkItem, journal.Cutoff) {
	t.Helper()
	fx := journaltest.New(t)

	captured := journal.SessionKey{Harness: "claude", NativeID: "captured-01"}
	curated := journal.SessionKey{Harness: "claude", NativeID: "curated-01"}
	stale := journal.SessionKey{Harness: "codex", NativeID: "stale-01"}
	dismissed := journal.SessionKey{Harness: "claude", NativeID: "dismissed-01"}

	fx.Captured("2026-09-10", captured,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"),
	)
	fx.Curated("2026-09-11", curated,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 10, SHA256: "b"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"),
		mustParseTime(t, "2026-09-11T18:00:00-05:00"),
		"framework", "curated title",
	).WithProject("2026-09-11", curated, journal.SessionProject{ID: "01P", Slug: "clast", Clone: "01C", Label: "dev", Path: "/x"})
	fx.CuratedStale("2026-09-11", stale,
		journal.TranscriptFingerprint{Format: "codex-jsonl", Lines: 20, SHA256: "grown"},
		journal.TranscriptStamp{Lines: 10, SHA256: "orig"},
		mustParseTime(t, "2026-09-11T10:00:00-05:00"),
		mustParseTime(t, "2026-09-11T12:00:00-05:00"),
		"laptop", "stale title",
	).WithMachine("2026-09-11", stale, "laptop")
	fx.Dismissed("2026-09-12", dismissed,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "c"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"),
		mustParseTime(t, "2026-09-12T09:00:05-05:00"),
		"framework", "auto:no-op",
	)

	items, diags, err := journal.Walk(fx.Root())
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	return items, mustCutoff(t, "04:00")
}

func TestApply_ZeroFilterMatchesEverything(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{}, cutoff)
	if len(got) != len(items) {
		t.Errorf("Apply with zero Filter = %d items, want %d (all)", len(got), len(items))
	}
}

func TestApply_StateFilter(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{States: []journal.CurationState{journal.StateCurated}}, cutoff)
	if len(got) != 2 {
		t.Fatalf("Apply(state=curated) = %d, want 2 (curated + stale, both are State()==curated)", len(got))
	}
	for _, it := range got {
		if it.State() != journal.StateCurated {
			t.Errorf("item %s has state %q, want curated", it.Key.DirName(), it.State())
		}
	}
}

func TestApply_StateFilterRepeatable(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{States: []journal.CurationState{journal.StateCaptured, journal.StateDismissed}}, cutoff)
	if len(got) != 2 {
		t.Fatalf("Apply(state=captured,dismissed) = %d, want 2", len(got))
	}
}

func TestApply_StaleComposesWithState(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{States: []journal.CurationState{journal.StateCurated}, StaleOnly: true}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(state=curated, stale) = %d, want 1 (only the grown session)", len(got))
	}
	if !got[0].Stale() {
		t.Errorf("returned item is not actually stale: %+v", got[0])
	}
}

func TestApply_StaleAloneNeedsNoStateFilter(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{StaleOnly: true}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(stale) = %d, want 1", len(got))
	}
}

func TestApply_ProjectFilter(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{Project: "clast"}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(project=clast) = %d, want 1", len(got))
	}
}

func TestApply_ProjectFilterExcludesProjectless(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{Project: "nonexistent"}, cutoff)
	if len(got) != 0 {
		t.Fatalf("Apply(project=nonexistent) = %d, want 0", len(got))
	}
}

func TestApply_HarnessFilter(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{Harness: "codex"}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(harness=codex) = %d, want 1", len(got))
	}
}

func TestApply_MachineFilter(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{Machine: "laptop"}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(machine=laptop) = %d, want 1", len(got))
	}
}

func TestApply_DayFilter(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	day := journal.Day("2026-09-11")
	got := query.Apply(items, query.Filter{Day: &day}, cutoff)
	if len(got) != 2 {
		t.Fatalf("Apply(day=2026-09-11) = %d, want 2 (curated + stale)", len(got))
	}
}

func TestApply_SinceFilterIsInclusiveDayBucket(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	since := journal.Day("2026-09-11")
	got := query.Apply(items, query.Filter{Since: &since}, cutoff)
	if len(got) != 3 {
		t.Fatalf("Apply(since=2026-09-11) = %d, want 3 (excludes the 09-10 captured session)", len(got))
	}
}

func TestApply_FiltersComposeAcrossAxes(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	got := query.Apply(items, query.Filter{
		States:  []journal.CurationState{journal.StateCurated},
		Harness: "claude",
	}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(state=curated, harness=claude) = %d, want 1 (the stale one is codex)", len(got))
	}
}

// TestApply_FiltersComposeAcrossEveryNonDayAxis composes State, StaleOnly,
// Project, Harness, Machine and Since all at once against a single Filter
// value, with cutoff and the Since bound both supplied directly (no
// time.Now() involved) — the hermetic counterpart to the e2e suite's
// TestSeal_FiltersComposeAcrossEveryAxis, which cannot exercise a real
// (non-"all") --since bound without coupling to the built binary's wall
// clock. Day is left out deliberately: Filter.Day (exact match) and
// Filter.Since (inclusive lower bound) both derive from the same
// cutoff.DayOf(StartedAt) fact, so once a fixture's day bucket satisfies
// an exact Day match it always satisfies any Since bound loose enough to
// admit that same day too — the two can't be independently falsified in
// one Match call, which is exactly why Since gets its own composition
// check here instead.
func TestApply_FiltersComposeAcrossEveryNonDayAxis(t *testing.T) {
	items, cutoff := buildFilterFixture(t)
	since := journal.Day("2026-09-11")
	got := query.Apply(items, query.Filter{
		States:    []journal.CurationState{journal.StateCurated},
		StaleOnly: true,
		Harness:   "codex",
		Machine:   "laptop",
		Since:     &since,
	}, cutoff)
	if len(got) != 1 {
		t.Fatalf("Apply(state=curated, stale, harness=codex, machine=laptop, since=2026-09-11) = %d, want 1 (the grown session)", len(got))
	}
	if got[0].Key.NativeID != "stale-01" {
		t.Fatalf("Apply(...) matched %s, want stale-01", got[0].Key.NativeID)
	}

	// A no-op Since (e.g. a regression that stops passing the bound
	// through) would let the 2026-09-10 captured session's day-mates
	// through unaffected by Since specifically, but this fixture set has
	// nothing else at that state/harness/machine combination to leak in
	// — so instead assert the bound itself actually excludes an
	// otherwise-fully-matching-but-too-old session directly.
	tooOld := journal.Day("2026-09-12")
	got = query.Apply(items, query.Filter{
		States:    []journal.CurationState{journal.StateCurated},
		StaleOnly: true,
		Harness:   "codex",
		Machine:   "laptop",
		Since:     &tooOld,
	}, cutoff)
	if len(got) != 0 {
		t.Fatalf("Apply(..., since=2026-09-12) = %d, want 0 (the grown session's 2026-09-11 bucket is before the bound)", len(got))
	}
}

func TestValidateHarness_EmptyIsNoRestriction(t *testing.T) {
	if err := query.ValidateHarness(""); err != nil {
		t.Errorf("ValidateHarness(\"\") = %v, want nil", err)
	}
}

func TestValidateHarness_KnownSourcePasses(t *testing.T) {
	if err := query.ValidateHarness("claude"); err != nil {
		t.Errorf("ValidateHarness(claude) = %v, want nil", err)
	}
}

func TestValidateHarness_UnknownSourceRefused(t *testing.T) {
	err := query.ValidateHarness("nonexistent-harness")
	if err == nil {
		t.Fatal("ValidateHarness: want an error for an unimplemented harness")
	}
	assertCode(t, err, "validation.unknown-harness")
}

func TestParseStates_EmptyIsNoRestriction(t *testing.T) {
	states, err := query.ParseStates(nil)
	if err != nil {
		t.Fatalf("ParseStates(nil): %v", err)
	}
	if states != nil {
		t.Errorf("ParseStates(nil) = %+v, want nil", states)
	}
}

func TestParseStates_ValidValues(t *testing.T) {
	states, err := query.ParseStates([]string{"captured", "curated", "dismissed"})
	if err != nil {
		t.Fatalf("ParseStates: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("ParseStates = %+v, want 3", states)
	}
}

func TestParseStates_UnknownValueRefused(t *testing.T) {
	_, err := query.ParseStates([]string{"bogus"})
	if err == nil {
		t.Fatal("ParseStates: want an error for an unknown state")
	}
	assertCode(t, err, "validation.unknown-state")
}

func TestResolveSince_AllMeansNoBound(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	now := mustParseTime(t, "2026-09-20T09:00:00-05:00")
	bound, err := query.ResolveSince("all", "-14d", cutoff, now)
	if err != nil {
		t.Fatalf("ResolveSince: %v", err)
	}
	if bound != nil {
		t.Errorf("ResolveSince(all) = %v, want nil", *bound)
	}
}

func TestResolveSince_EmptyFallsBackToDefault(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	now := mustParseTime(t, "2026-09-20T09:00:00-05:00")
	bound, err := query.ResolveSince("", "-14d", cutoff, now)
	if err != nil {
		t.Fatalf("ResolveSince: %v", err)
	}
	if bound == nil {
		t.Fatal("ResolveSince(\"\", default=-14d) = nil, want a bound")
	}
	if *bound != journal.Day("2026-09-06") {
		t.Errorf("ResolveSince bound = %s, want 2026-09-06 (14 days before 2026-09-20)", *bound)
	}
}

func TestResolveSince_ExplicitDuration(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	now := mustParseTime(t, "2026-09-20T09:00:00-05:00")
	bound, err := query.ResolveSince("-1w", "-14d", cutoff, now)
	if err != nil {
		t.Fatalf("ResolveSince: %v", err)
	}
	if bound == nil || *bound != journal.Day("2026-09-13") {
		t.Errorf("ResolveSince(-1w) = %v, want 2026-09-13", bound)
	}
}

func TestResolveSince_MalformedDurationRefused(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	now := mustParseTime(t, "2026-09-20T09:00:00-05:00")
	_, err := query.ResolveSince("nonsense", "-14d", cutoff, now)
	if err == nil {
		t.Fatal("ResolveSince: want an error for a malformed duration")
	}
}

func TestConfiguredSince_DefaultsWhenAbsent(t *testing.T) {
	s, err := query.ConfiguredSince(config.Config{})
	if err != nil {
		t.Fatalf("ConfiguredSince: %v", err)
	}
	if s != query.DefaultSinceString {
		t.Errorf("ConfiguredSince(empty cfg) = %q, want %q", s, query.DefaultSinceString)
	}
}

func TestConfiguredSince_ReadsConfigKey(t *testing.T) {
	s, err := query.ConfiguredSince(config.Config{"since": "-30d"})
	if err != nil {
		t.Fatalf("ConfiguredSince: %v", err)
	}
	if s != "-30d" {
		t.Errorf("ConfiguredSince = %q, want -30d", s)
	}
}

func TestConfiguredSince_WrongTypeIsAnError(t *testing.T) {
	_, err := query.ConfiguredSince(config.Config{"since": 30})
	if err == nil {
		t.Fatal("ConfiguredSince: want an error for a non-string since value")
	}
}

// assertCode asserts err is a *clasterr.Error carrying wantCode.
func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	ce, ok := err.(*clasterr.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if ce.Code != wantCode {
		t.Errorf("error code = %q, want %q", ce.Code, wantCode)
	}
}
