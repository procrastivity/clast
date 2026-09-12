package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeHostname points hostname (the C2.6 seam) at name for the test.
func fakeHostname(t *testing.T, name string) {
	t.Helper()
	orig := hostname
	hostname = func() (string, error) { return name, nil }
	t.Cleanup(func() { hostname = orig })
}

// fakeNow points the shared now() seam (store.go) at a fixed instant.
func fakeNow(t *testing.T, at time.Time) {
	t.Helper()
	orig := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = orig })
}

func TestAppendBreadcrumb_CreatesFileWithMachineAndDate(t *testing.T) {
	root := t.TempDir()
	fakeHostname(t, "framework")
	fakeNow(t, time.Date(2026, 9, 11, 14, 2, 0, 0, time.Local))

	slug := "clast"
	b := Breadcrumb{At: time.Date(2026, 9, 11, 10, 22, 0, 0, time.Local), Slug: &slug, Text: "check migration before deploy"}
	if err := AppendBreadcrumb(root, b); err != nil {
		t.Fatalf("AppendBreadcrumb: %v", err)
	}

	wantPath := filepath.Join(root, "breadcrumbs", "2026-09-11.framework.jsonl")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected file at %s: %v", wantPath, err)
	}

	entries, diags, err := ReadBreadcrumbs(root, "2026-09-11")
	if err != nil {
		t.Fatalf("ReadBreadcrumbs: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	if entries[0].Machine != "framework" {
		t.Errorf("Machine = %q, want %q", entries[0].Machine, "framework")
	}
	if entries[0].Slug == nil || *entries[0].Slug != "clast" {
		t.Errorf("Slug = %v, want \"clast\"", entries[0].Slug)
	}
	if entries[0].Text != "check migration before deploy" {
		t.Errorf("Text = %q, want %q", entries[0].Text, "check migration before deploy")
	}

	// The first append must have triggered EnsureRoot's init-on-first-write.
	if _, ok, err := ReadMarker(root); err != nil || !ok {
		t.Errorf("ReadMarker after AppendBreadcrumb: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
}

func TestAppendBreadcrumb_TwoAppendsProduceTwoLines(t *testing.T) {
	root := t.TempDir()
	fakeHostname(t, "framework")
	fakeNow(t, time.Date(2026, 9, 11, 14, 2, 0, 0, time.Local))

	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Text: "first"}); err != nil {
		t.Fatalf("AppendBreadcrumb (1): %v", err)
	}
	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Text: "second"}); err != nil {
		t.Fatalf("AppendBreadcrumb (2): %v", err)
	}

	entries, diags, err := ReadBreadcrumbs(root, "2026-09-11")
	if err != nil {
		t.Fatalf("ReadBreadcrumbs: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	if entries[0].Text != "first" || entries[1].Text != "second" {
		t.Errorf("entries = %+v, want [first, second] in append order", entries)
	}
}

func TestReadBreadcrumbs_MergesAcrossMachines(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"

	// Fabricate a second machine's file directly — this machine never
	// appends to it (M5), but a reader must still see it via the glob.
	dir := filepath.Join(root, "breadcrumbs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	laptopFile := filepath.Join(dir, shard+".laptop.jsonl")
	laptopLine := `{"at":"2026-09-11T09:00:00-05:00","slug":null,"text":"from laptop"}` + "\n"
	if err := os.WriteFile(laptopFile, []byte(laptopLine), 0o644); err != nil {
		t.Fatalf("write laptop file: %v", err)
	}

	fakeHostname(t, "framework")
	fakeNow(t, time.Date(2026, 9, 11, 14, 2, 0, 0, time.Local))
	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Text: "from framework"}); err != nil {
		t.Fatalf("AppendBreadcrumb: %v", err)
	}

	entries, diags, err := ReadBreadcrumbs(root, shard)
	if err != nil {
		t.Fatalf("ReadBreadcrumbs: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 (one per machine)", entries)
	}

	machines := map[string]bool{}
	for _, e := range entries {
		machines[e.Machine] = true
	}
	if !machines["framework"] || !machines["laptop"] {
		t.Errorf("entries = %+v, want entries from both framework and laptop", entries)
	}
}

func TestReadBreadcrumbs_GarbageLineSkippedAndCounted(t *testing.T) {
	root := t.TempDir()
	shard := "2026-09-11"

	dir := filepath.Join(root, "breadcrumbs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, shard+".framework.jsonl")
	content := `{"at":"2026-09-11T09:00:00-05:00","slug":null,"text":"good line"}` + "\n" +
		`not json` + "\n" +
		`{"at":"2026-09-11T09:05:00-05:00","slug":null,"text":"also good"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, diags, err := ReadBreadcrumbs(root, shard)
	if err != nil {
		t.Fatalf("ReadBreadcrumbs returned an error for a garbage line: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 good lines", entries)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	if diags[0].Line != 2 {
		t.Errorf("diags[0].Line = %d, want 2", diags[0].Line)
	}
	if diags[0].Path != path {
		t.Errorf("diags[0].Path = %q, want %q", diags[0].Path, path)
	}
	if diags[0].Err == nil {
		t.Errorf("diags[0].Err = nil, want a parse error")
	}
}

func TestReadBreadcrumbs_NoFilesIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	entries, diags, err := ReadBreadcrumbs(root, "2026-01-01")
	if err != nil {
		t.Fatalf("ReadBreadcrumbs on an empty journal returned an error: %v", err)
	}
	if len(entries) != 0 || len(diags) != 0 {
		t.Errorf("entries = %+v, diags = %+v, want both empty", entries, diags)
	}
}

func TestAppendBreadcrumb_GlobalCrumbRoundTrip(t *testing.T) {
	root := t.TempDir()
	fakeHostname(t, "framework")
	fakeNow(t, time.Date(2026, 9, 11, 14, 2, 0, 0, time.Local))

	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Slug: nil, Text: "bump the cache version"}); err != nil {
		t.Fatalf("AppendBreadcrumb: %v", err)
	}

	entries, diags, err := ReadBreadcrumbs(root, "2026-09-11")
	if err != nil {
		t.Fatalf("ReadBreadcrumbs: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	if entries[0].Slug != nil {
		t.Errorf("Slug = %v, want nil (global crumb)", entries[0].Slug)
	}
}

func TestReadBreadcrumbs_RejectsMalformedFileDate(t *testing.T) {
	root := t.TempDir()
	for _, arg := range []string{
		"*",          // a bare glob metacharacter
		"2026-09-*",  // metacharacter smuggled into an otherwise-plausible date
		"2026-13-40", // not a real calendar date
		"not-a-date",
		"",
	} {
		if _, _, err := ReadBreadcrumbs(root, arg); err == nil {
			t.Errorf("ReadBreadcrumbs(%q): want error, got nil", arg)
		}
	}
}

// TestReadBreadcrumbsForDay_BoundaryCrumbs is the day-bucketed read's
// boundary check (M8): a crumb written at 02:30 is filed under the NEXT
// calendar date's file (AppendBreadcrumb shards by write-time date), but
// under a 04:00 cutoff it still belongs to the PREVIOUS day's bucket —
// the exact case ReadBreadcrumbsForDay's day+1 file read exists for.
// 03:59 vs. 04:00 on the day's own file pins the cutoff boundary itself.
func TestReadBreadcrumbsForDay_BoundaryCrumbs(t *testing.T) {
	root := t.TempDir()
	cutoff := mustCutoff(t, "04:00")
	loc := time.FixedZone("test", -5*3600)
	fakeHostname(t, "framework")
	day := Day("2026-09-11")

	// 02:30 on 09-12 (day+1): filed under 2026-09-12's file, but belongs
	// to day 2026-09-11's bucket under the 04:00 cutoff.
	fakeNow(t, time.Date(2026, 9, 12, 2, 30, 0, 0, loc))
	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Text: "day+1 file, before cutoff"}); err != nil {
		t.Fatalf("AppendBreadcrumb: %v", err)
	}

	// 03:59 on day itself: still belongs to the PREVIOUS bucket
	// (2026-09-10), even though it's filed under day's own file.
	fakeNow(t, time.Date(2026, 9, 11, 3, 59, 0, 0, loc))
	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Text: "day file, before cutoff"}); err != nil {
		t.Fatalf("AppendBreadcrumb: %v", err)
	}

	// 04:00 on day itself: at the cutoff, belongs to day's own bucket.
	fakeNow(t, time.Date(2026, 9, 11, 4, 0, 0, 0, loc))
	if err := AppendBreadcrumb(root, Breadcrumb{At: now(), Text: "day file, at cutoff"}); err != nil {
		t.Fatalf("AppendBreadcrumb: %v", err)
	}

	entries, diags, err := ReadBreadcrumbsForDay(root, day, cutoff)
	if err != nil {
		t.Fatalf("ReadBreadcrumbsForDay: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}

	texts := map[string]bool{}
	for _, e := range entries {
		texts[e.Text] = true
	}
	if !texts["day+1 file, before cutoff"] {
		t.Errorf("entries = %+v, want the day+1-file/before-cutoff crumb included", entries)
	}
	if !texts["day file, at cutoff"] {
		t.Errorf("entries = %+v, want the at-cutoff crumb included", entries)
	}
	if texts["day file, before cutoff"] {
		t.Errorf("entries = %+v, want the before-cutoff same-file crumb excluded (belongs to the previous day)", entries)
	}
	if len(entries) != 2 {
		t.Errorf("entries = %+v, want exactly 2", entries)
	}
}

func TestReadBreadcrumbsForDay_NoFilesIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	entries, diags, err := ReadBreadcrumbsForDay(root, Day("2026-01-01"), mustCutoff(t, "04:00"))
	if err != nil {
		t.Fatalf("ReadBreadcrumbsForDay on an empty journal returned an error: %v", err)
	}
	if len(entries) != 0 || len(diags) != 0 {
		t.Errorf("entries = %+v, diags = %+v, want both empty", entries, diags)
	}
}
