package brief_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/registry"
	"github.com/procrastivity/clast/internal/verbs/brief"
)

var ctx = context.Background()

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

func mustCutoff(t *testing.T, s string) journal.Cutoff {
	t.Helper()
	c, err := journal.ParseCutoff(s)
	if err != nil {
		t.Fatalf("ParseCutoff(%q): %v", s, err)
	}
	return c
}

// writeTornEntry writes entry.md bytes that fail entry.Parse directly
// through journal.WriteEntry — journaltest's own Curated helper always
// writes a well-formed entry, so a torn one is authored by hand here
// (mirrors wake_test.go's own writeTornEntry).
func writeTornEntry(root, shard string, key journal.SessionKey) error {
	return journal.WriteEntry(root, shard, key, []byte("not an entry document at all"))
}

// --- git fixture helpers, ported from whereami_test.go (no mocks for
// anything git-backed) ---

func newRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func localMachine(t *testing.T) string {
	t.Helper()
	m, err := journal.Hostname()
	if err != nil {
		t.Fatalf("journal.Hostname: %v", err)
	}
	return m
}

// registerClone authors a project + this-machine's clone registration
// directly (journal.WriteProject/WriteClones), mirroring what `clast
// init` writes, without shelling out to the real binary.
func registerClone(t *testing.T, root, slug, machine, commonDir, label string) {
	t.Helper()
	if err := journal.WriteProject(root, slug, journal.Project{ID: registry.NewSource().Next(), Slug: slug}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	clone := journal.Clone{ID: registry.NewSource().Next(), GitCommonDir: commonDir, Label: label}
	if err := journal.WriteClones(root, slug, journal.ClonesFile{Machine: machine, Clones: []journal.Clone{clone}}); err != nil {
		t.Fatalf("WriteClones: %v", err)
	}
}

// --- Run: project resolution (resolveProject) ---

// TestRun_ProjectPositional_UnknownLocator_ValidationError confirms an
// explicit <project> that matches no registered project (by id or slug)
// is validation.unknown-locator — mirroring `clones`' own unknownProject
// (finding: <project> is a registry LOCATOR, not a raw filter value).
func TestRun_ProjectPositional_UnknownLocator_ValidationError(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")

	_, err := brief.Run(ctx, fx.Root(), t.TempDir(), "no-such-project", cutoff, nil, "2026-09-12")
	if err == nil {
		t.Fatal("Run succeeded on an unknown project locator, want validation.unknown-locator")
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if cerr.Code != "validation.unknown-locator" {
		t.Errorf("error code = %q, want %q", cerr.Code, "validation.unknown-locator")
	}
}

// TestRun_ProjectPositional_ResolvesBySlugAndByID confirms the positional
// resolves through the registry both ways (M16's shape rule), same as
// `clones [<project>]`.
func TestRun_ProjectPositional_ResolvesBySlugAndByID(t *testing.T) {
	fx := journaltest.New(t)
	project := journal.Project{ID: registry.NewSource().Next(), Slug: "widget"}
	if err := journal.WriteProject(fx.Root(), "widget", project); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	cutoff := mustCutoff(t, "04:00")

	for _, locator := range []string{"widget", project.ID} {
		result, err := brief.Run(ctx, fx.Root(), t.TempDir(), locator, cutoff, nil, "2026-09-12")
		if err != nil {
			t.Fatalf("Run(%q): %v", locator, err)
		}
		if result.ProjectSlug != "widget" {
			t.Errorf("Run(%q).ProjectSlug = %q, want %q", locator, result.ProjectSlug, "widget")
		}
	}
}

// TestRun_NoPositional_NotAGitRepo_ValidationError mirrors whereami's own
// pre-flight: a cwd outside any git repository is validation.not-a-git-repo,
// not a raw exec error.
func TestRun_NoPositional_NotAGitRepo_ValidationError(t *testing.T) {
	fx := journaltest.New(t)
	cutoff := mustCutoff(t, "04:00")

	_, err := brief.Run(ctx, fx.Root(), t.TempDir(), "", cutoff, nil, "2026-09-12")
	if err == nil {
		t.Fatal("Run succeeded outside a git repository, want validation.not-a-git-repo")
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if cerr.Code != "validation.not-a-git-repo" {
		t.Errorf("error code = %q, want %q", cerr.Code, "validation.not-a-git-repo")
	}
}

// TestRun_NoPositional_UnregisteredClone_RefusesUnknownClone mirrors
// whereami's own refusal.unknown-clone (V8/V22 — "follow whereami's
// posture").
func TestRun_NoPositional_UnregisteredClone_RefusesUnknownClone(t *testing.T) {
	fx := journaltest.New(t)
	dir := newRepo(t, "unregistered")
	cutoff := mustCutoff(t, "04:00")

	_, err := brief.Run(ctx, fx.Root(), dir, "", cutoff, nil, "2026-09-12")
	if err == nil {
		t.Fatal("Run succeeded on an unregistered clone, want refusal.unknown-clone")
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error %v (%T) is not a *clasterr.Error", err, err)
	}
	if cerr.Code != "refusal.unknown-clone" {
		t.Errorf("error code = %q, want %q", cerr.Code, "refusal.unknown-clone")
	}
}

// TestRun_NoPositional_RegisteredClone_ResolvesProjectAndHoistsLabel
// confirms the cwd default (V22's whereami path) resolves both the
// project slug and the clone's own label, which then hoists that
// project's matching workspace group first.
func TestRun_NoPositional_RegisteredClone_ResolvesProjectAndHoistsLabel(t *testing.T) {
	fx := journaltest.New(t)
	dir := newRepo(t, "widget")
	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	machine := localMachine(t)
	registerClone(t, fx.Root(), "widget", machine, commonDir, "dev")

	other := journal.SessionKey{Harness: "claude", NativeID: "other-01"}
	mine := journal.SessionKey{Harness: "claude", NativeID: "mine-01"}
	fx.Curated("2026-09-10", other,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "o"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"), mustParseTime(t, "2026-09-10T10:00:00-05:00"),
		"framework", "someone else's workspace",
	).WithProject("2026-09-10", other, journal.SessionProject{ID: "p", Slug: "widget", Clone: "c2", Label: "other", Path: "/other"})
	fx.Curated("2026-09-11", mine,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "m"},
		mustParseTime(t, "2026-09-11T09:00:00-05:00"), mustParseTime(t, "2026-09-11T10:00:00-05:00"),
		"framework", "my workspace's entry",
	).WithProject("2026-09-11", mine, journal.SessionProject{ID: "p", Slug: "widget", Clone: "c1", Label: "dev", Path: dir})

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), dir, "", cutoff, nil, "2026-09-12")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ProjectSlug != "widget" {
		t.Errorf("ProjectSlug = %q, want %q", result.ProjectSlug, "widget")
	}
	if result.CurrentWorkspace != "dev" {
		t.Errorf("CurrentWorkspace = %q, want %q", result.CurrentWorkspace, "dev")
	}
	if len(result.Groups) != 2 || result.Groups[0].Workspace != "dev" {
		t.Fatalf("Groups = %+v, want [dev, other] (dev hoisted first)", result.Groups)
	}
}

// TestRun_ProjectPositional_HoistsWhenCwdMatchesSameProject confirms
// hoisting is not gated on how the project was resolved: an explicit
// <project> that happens to equal the cwd's own project still hoists the
// cwd's clone label (a deliberate generalization beyond the old bash
// porcelain, which only computed current_label when no positional was
// given — finding).
func TestRun_ProjectPositional_HoistsWhenCwdMatchesSameProject(t *testing.T) {
	fx := journaltest.New(t)
	dir := newRepo(t, "widget")
	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	registerClone(t, fx.Root(), "widget", localMachine(t), commonDir, "dev")

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), dir, "widget", cutoff, nil, "2026-09-12")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.CurrentWorkspace != "dev" {
		t.Errorf("CurrentWorkspace = %q, want %q (hoist applies for an explicit positional too)", result.CurrentWorkspace, "dev")
	}
}

// TestRun_ProjectPositional_CwdOutsideAnyProject_NoHoistNoError confirms
// an explicit <project> never refuses just because the cwd doesn't
// resolve into it (unregistered, or not even a git repo).
func TestRun_ProjectPositional_CwdOutsideAnyProject_NoHoistNoError(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	cutoff := mustCutoff(t, "04:00")

	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-12")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.CurrentWorkspace != "" {
		t.Errorf("CurrentWorkspace = %q, want \"\" (cwd not inside any registered clone)", result.CurrentWorkspace)
	}
}

// --- Run: entries — grouping, hoisting, caps ---

func project(slug, clone, label, path string) journal.SessionProject {
	return journal.SessionProject{ID: "p-" + slug, Slug: slug, Clone: clone, Label: label, Path: path}
}

// TestRun_GroupsByLabel_CapsPerGroupAndTotal seeds five curated entries in
// one workspace and two in another, confirming the 3-per-group cap holds
// (only 3 of the 5 "dev" entries survive) while the 8-total cap is not
// yet the binding constraint (3 + 2 = 5 < 8).
func TestRun_GroupsByLabel_CapsPerGroupAndTotal(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	for i := 0; i < 5; i++ {
		key := journal.SessionKey{Harness: "claude", NativeID: "dev-0" + string(rune('1'+i))}
		fx.Curated("2026-09-1"+string(rune('0'+i)), key,
			journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "x"},
			time.Date(2026, 9, 10+i, 9, 0, 0, 0, time.UTC), time.Date(2026, 9, 10+i, 10, 0, 0, 0, time.UTC),
			"framework", "dev entry",
		).WithProject("2026-09-1"+string(rune('0'+i)), key, project("widget", "c1", "dev", "/dev"))
	}
	for i := 0; i < 2; i++ {
		key := journal.SessionKey{Harness: "claude", NativeID: "stage-0" + string(rune('1'+i))}
		fx.Curated("2026-09-0"+string(rune('1'+i)), key,
			journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "y"},
			time.Date(2026, 9, 1+i, 9, 0, 0, 0, time.UTC), time.Date(2026, 9, 1+i, 10, 0, 0, 0, time.UTC),
			"framework", "stage entry",
		).WithProject("2026-09-0"+string(rune('1'+i)), key, project("widget", "c2", "stage", "/stage"))
	}

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-20")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("Groups = %+v, want 2 workspaces", result.Groups)
	}
	byWorkspace := map[string]int{}
	total := 0
	for _, g := range result.Groups {
		byWorkspace[g.Workspace] = len(g.Entries)
		total += len(g.Entries)
	}
	if byWorkspace["dev"] != 3 {
		t.Errorf("dev group = %d entries, want 3 (per-group cap)", byWorkspace["dev"])
	}
	if byWorkspace["stage"] != 2 {
		t.Errorf("stage group = %d entries, want 2", byWorkspace["stage"])
	}
	if total != 5 {
		t.Errorf("total entries = %d, want 5 (3+2, under the 8 total cap)", total)
	}
}

// TestRun_TotalCap_BindsAcrossGroups seeds three workspaces with 3 entries
// each (9 total, over the 8-total cap) and confirms the third workspace
// (last in first-appearance order) is truncated to 2, not 3, once the
// running total hits 8.
func TestRun_TotalCap_BindsAcrossGroups(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	labels := []string{"a", "b", "c"}
	day := 1
	for _, label := range labels {
		for i := 0; i < 3; i++ {
			key := journal.SessionKey{Harness: "claude", NativeID: label + "-" + string(rune('1'+i))}
			shard := time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
			fx.Curated(shard, key,
				journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "x"},
				time.Date(2026, 9, day, 9, 0, 0, 0, time.UTC), time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC),
				"framework", label+" entry",
			).WithProject(shard, key, project("widget", label, label, "/"+label))
			day++
		}
	}

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-30")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	total := 0
	for _, g := range result.Groups {
		total += len(g.Entries)
	}
	if total != 8 {
		t.Fatalf("total entries = %d, want 8 (the total cap)", total)
	}
	// "c" is newest (highest day) so it appears first in first-appearance
	// order (no hoist target here); "a" is oldest so it is the last group
	// and absorbs the shortfall.
	last := result.Groups[len(result.Groups)-1]
	if last.Workspace != "a" || len(last.Entries) != 2 {
		t.Errorf("last group = %+v, want workspace \"a\" truncated to 2 entries", last)
	}
}

// TestRun_LabelFallsBackToBranch_WhenLabelEmpty covers V8's fallback: a
// session whose frozen project carries an empty label groups by branch
// instead.
func TestRun_LabelFallsBackToBranch_WhenLabelEmpty(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	key := journal.SessionKey{Harness: "claude", NativeID: "nolabel-01"}
	fx.Curated("2026-09-10", key,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "x"},
		mustParseTime(t, "2026-09-10T09:00:00-05:00"), mustParseTime(t, "2026-09-10T10:00:00-05:00"),
		"framework", "no label entry",
	).WithProject("2026-09-10", key, project("widget", "c1", "", "/nolabel"))

	sess, ok, err := journal.ReadSession(fx.Root(), "2026-09-10", key)
	if err != nil || !ok {
		t.Fatalf("ReadSession: ok=%v err=%v", ok, err)
	}
	sess.Branch = "feature-x"
	if err := journal.WriteSession(fx.Root(), "2026-09-10", key, sess); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-20")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 1 || result.Groups[0].Workspace != "feature-x" {
		t.Fatalf("Groups = %+v, want one group keyed by branch %q", result.Groups, "feature-x")
	}
}

// TestRun_SinceWindowRestrictsEntries confirms V31's since resolution
// applies to gathered entries (V8 leaves "recent" unpinned — resolved via
// the same posture as sessions/wake).
func TestRun_SinceWindowRestrictsEntries(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	tooOld := journal.SessionKey{Harness: "claude", NativeID: "too-old"}
	inWindow := journal.SessionKey{Harness: "claude", NativeID: "in-window"}
	fx.Curated("2026-09-01", tooOld,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-01T09:00:00-05:00"), mustParseTime(t, "2026-09-01T10:00:00-05:00"),
		"framework", "too old",
	).WithProject("2026-09-01", tooOld, project("widget", "c1", "dev", "/dev"))
	fx.Curated("2026-09-12", inWindow,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-12T09:00:00-05:00"), mustParseTime(t, "2026-09-12T10:00:00-05:00"),
		"framework", "in window",
	).WithProject("2026-09-12", inWindow, project("widget", "c1", "dev", "/dev"))

	cutoff := mustCutoff(t, "04:00")
	since := journal.Day("2026-09-10")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, &since, "2026-09-20")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 1 || len(result.Groups[0].Entries) != 1 || result.Groups[0].Entries[0].Item.Key != inWindow {
		t.Fatalf("Groups = %+v, want only %s", result.Groups, inWindow.DirName())
	}
}

// TestRun_TornEntry_DroppedNotListed confirms brief's own divergence from
// sessions/wake's tolerant "keep the row, drop the title" posture: a
// curated session whose entry.md fails to parse contributes no row at
// all (there is no content to gather).
func TestRun_TornEntry_DroppedNotListed(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	torn := journal.SessionKey{Harness: "claude", NativeID: "torn-01"}
	if err := journal.WriteSession(fx.Root(), "2026-09-10", torn, journal.Session{
		Harness: torn.Harness, SessionID: torn.NativeID, Machine: "framework",
		StartedAt: mustParseTime(t, "2026-09-10T09:00:00-05:00"), LastActiveAt: mustParseTime(t, "2026-09-10T09:20:00-05:00"),
		CapturedAt: mustParseTime(t, "2026-09-10T09:25:00-05:00"),
		Transcript: journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 20, SHA256: "grown"},
		Project:    &journal.SessionProject{ID: "p", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"},
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := journal.WriteCuration(fx.Root(), "2026-09-10", torn, journal.Curation{
		State: journal.StateCurated, At: mustParseTime(t, "2026-09-10T10:00:00-05:00"), Machine: "framework",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: 20, SHA256: "grown"},
	}); err != nil {
		t.Fatalf("WriteCuration: %v", err)
	}
	if err := writeTornEntry(fx.Root(), "2026-09-10", torn); err != nil {
		t.Fatalf("writeTornEntry: %v", err)
	}

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-20")
	if err != nil {
		t.Fatalf("Run: %v, want the torn entry.md tolerated (dropped, not propagated)", err)
	}
	if len(result.Groups) != 0 {
		t.Fatalf("Groups = %+v, want none (the only entry is unreadable)", result.Groups)
	}
}

// --- Run: breadcrumbs and sessions (today) ---

// TestRun_BreadcrumbsAndSessions_ScopedToProjectAndToday confirms both
// today's breadcrumbs and today's sessions are scoped to the project
// (V19's project-only scoping for the former), regardless of curation
// state for the latter, and that a different day or a different
// project's crumb/session is excluded.
func TestRun_BreadcrumbsAndSessions_ScopedToProjectAndToday(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	today := journal.Day("2026-09-12")

	widget := "widget"
	other := "other"
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-12T09:00:00-05:00"), &widget, "today's widget crumb")
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-12T09:05:00-05:00"), &other, "today's other-project crumb")
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-11T09:00:00-05:00"), &widget, "yesterday's widget crumb")
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-12T09:10:00-05:00"), nil, "today's global crumb")

	captured := journal.SessionKey{Harness: "claude", NativeID: "captured-today"}
	fx.Captured("2026-09-12", captured,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "a"},
		mustParseTime(t, "2026-09-12T08:00:00-05:00"),
	).WithProject("2026-09-12", captured, project("widget", "c1", "dev", "/dev"))
	yesterday := journal.SessionKey{Harness: "claude", NativeID: "yesterday"}
	fx.Captured("2026-09-11", yesterday,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "b"},
		mustParseTime(t, "2026-09-11T08:00:00-05:00"),
	).WithProject("2026-09-11", yesterday, project("widget", "c1", "dev", "/dev"))
	otherProject := journal.SessionKey{Harness: "claude", NativeID: "other-project-today"}
	fx.Captured("2026-09-12", otherProject,
		journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 5, SHA256: "c"},
		mustParseTime(t, "2026-09-12T08:30:00-05:00"),
	).WithProject("2026-09-12", otherProject, project("other", "c2", "other", "/other"))

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, today)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Breadcrumbs) != 1 || result.Breadcrumbs[0].Text != "today's widget crumb" {
		t.Errorf("Breadcrumbs = %+v, want only today's widget crumb", result.Breadcrumbs)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].Item.Key != captured {
		t.Errorf("Sessions = %+v, want only today's widget session", result.Sessions)
	}
}

// TestRun_EmptyWhenAllThreeEmpty confirms V7's short-circuit fact: no
// curated entries, no breadcrumbs, no sessions today.
func TestRun_EmptyWhenAllThreeEmpty(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	cutoff := mustCutoff(t, "04:00")

	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-20")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Empty {
		t.Errorf("Empty = false, want true (no entries/breadcrumbs/sessions)")
	}
}

// TestRun_NotEmpty_WhenOnlyBreadcrumbsPresent confirms Empty is false as
// soon as any one of the three sources has something, not only when all
// three do.
func TestRun_NotEmpty_WhenOnlyBreadcrumbsPresent(t *testing.T) {
	fx := journaltest.New(t)
	if err := journal.WriteProject(fx.Root(), "widget", journal.Project{ID: "p", Slug: "widget"}); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	slug := "widget"
	fx.Breadcrumb("framework", mustParseTime(t, "2026-09-20T09:00:00-05:00"), &slug, "a crumb")

	cutoff := mustCutoff(t, "04:00")
	result, err := brief.Run(ctx, fx.Root(), t.TempDir(), "widget", cutoff, nil, "2026-09-20")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Empty {
		t.Errorf("Empty = true, want false (a breadcrumb is present)")
	}
}
