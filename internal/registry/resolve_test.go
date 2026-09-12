package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
)

// regFixture authors a small registry tree through journal's own write
// primitives (WriteProject, WriteClones) — the same "fixture built through
// production's own functions" posture journal's own tests take, and wip's
// no-mocks posture for anything git-backed (testutil_test.go's newRepo /
// addRemote / addWorktree, ported and already present in this package).
type regFixture struct {
	t    *testing.T
	root string
}

func newRegFixture(t *testing.T) *regFixture {
	t.Helper()
	return &regFixture{t: t, root: t.TempDir()}
}

func (f *regFixture) project(p journal.Project) *regFixture {
	f.t.Helper()
	if err := journal.WriteProject(f.root, p.Slug, p); err != nil {
		f.t.Fatalf("fixture: WriteProject(%s): %v", p.Slug, err)
	}
	return f
}

// clone appends c to slug's clones.<machine>.json, preserving whatever rows
// that machine's file already carries — so a test can register several
// clones (of the same or different projects) under one machine file across
// several calls.
func (f *regFixture) clone(slug, machine string, c journal.Clone) *regFixture {
	f.t.Helper()
	existing, _, err := journal.ReadClones(f.root, slug, machine)
	if err != nil {
		f.t.Fatalf("fixture: ReadClones(%s/%s): %v", slug, machine, err)
	}
	existing.Machine = machine
	existing.Clones = append(existing.Clones, c)
	if err := journal.WriteClones(f.root, slug, existing); err != nil {
		f.t.Fatalf("fixture: WriteClones(%s/%s): %v", slug, machine, err)
	}
	return f
}

func mustCommonDir(t *testing.T, dir string) string {
	t.Helper()
	cd, err := CommonDir(ctx, dir)
	if err != nil {
		t.Fatalf("CommonDir(%s): %v", dir, err)
	}
	return cd
}

func localMachine(t *testing.T) string {
	t.Helper()
	m, err := journal.Hostname()
	if err != nil {
		t.Fatalf("journal.Hostname: %v", err)
	}
	return m
}

// TestResolveCurrentClone_FromSubdirectory confirms a clone registered at a
// repo's own common-dir resolves from an ordinary subdirectory of it, on
// this machine, as the main worktree (Worktree == "").
func TestResolveCurrentClone_FromSubdirectory(t *testing.T) {
	dir := newRepo(t, "widget")
	commonDir := mustCommonDir(t, dir)
	machine := localMachine(t)

	fx := newRegFixture(t)
	fx.project(journal.Project{ID: "01J9WIDGETPROJECT0000000A", Slug: "widget"})
	fx.clone("widget", machine, journal.Clone{ID: "01J9WIDGETCLONE00000000A", GitCommonDir: commonDir, Label: "dev"})

	sub := filepath.Join(dir, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := ResolveCurrentClone(ctx, fx.root, sub)
	if err != nil {
		t.Fatalf("ResolveCurrentClone: %v", err)
	}
	if got.Project.Slug != "widget" {
		t.Errorf("Project.Slug = %q, want %q", got.Project.Slug, "widget")
	}
	if got.Clone.ID != "01J9WIDGETCLONE00000000A" {
		t.Errorf("Clone.ID = %q, want %q", got.Clone.ID, "01J9WIDGETCLONE00000000A")
	}
	if got.Worktree != "" {
		t.Errorf("Worktree = %q, want \"\" (main worktree)", got.Worktree)
	}
}

// TestResolveCurrentClone_FromLinkedWorktree confirms resolution from
// inside a linked worktree finds the SAME owning clone (common-dir is
// worktree-invariant, M15) and reports the worktree's own name (M17) rather
// than "".
func TestResolveCurrentClone_FromLinkedWorktree(t *testing.T) {
	main := newRepo(t, "widget")
	commonDir := mustCommonDir(t, main)
	machine := localMachine(t)

	fx := newRegFixture(t)
	fx.project(journal.Project{ID: "01J9WIDGETPROJECT0000000B", Slug: "widget"})
	fx.clone("widget", machine, journal.Clone{ID: "01J9WIDGETCLONE00000000B", GitCommonDir: commonDir, Label: "dev"})

	wt := addWorktree(t, main, main+"-feature", "feature")

	got, err := ResolveCurrentClone(ctx, fx.root, wt)
	if err != nil {
		t.Fatalf("ResolveCurrentClone: %v", err)
	}
	if got.Clone.ID != "01J9WIDGETCLONE00000000B" {
		t.Errorf("Clone.ID = %q, want %q (same owning clone as the main worktree)", got.Clone.ID, "01J9WIDGETCLONE00000000B")
	}
	if got.Worktree != filepath.Base(wt) {
		t.Errorf("Worktree = %q, want %q (GitDir's own basename)", got.Worktree, filepath.Base(wt))
	}
}

// TestResolveCurrentClone_UnknownClone confirms a real git repo that was
// never registered resolves to ErrUnknownClone, not some zero value read as
// success.
func TestResolveCurrentClone_UnknownClone(t *testing.T) {
	dir := newRepo(t, "unregistered")
	fx := newRegFixture(t)

	_, err := ResolveCurrentClone(ctx, fx.root, dir)
	if !errors.Is(err, ErrUnknownClone) {
		t.Fatalf("ResolveCurrentClone = %v, want ErrUnknownClone", err)
	}
}

// TestResolveCurrentClone_AnotherMachinesCloneDoesNotMatch confirms a
// clone row is host-scoped (M15/S6): a row registered under a different
// machine's clones.<machine>.json, even with a common-dir that matches
// this location exactly, is never resolved locally.
func TestResolveCurrentClone_AnotherMachinesCloneDoesNotMatch(t *testing.T) {
	dir := newRepo(t, "widget")
	commonDir := mustCommonDir(t, dir)

	fx := newRegFixture(t)
	fx.project(journal.Project{ID: "01J9WIDGETPROJECT0000000C", Slug: "widget"})
	fx.clone("widget", "some-other-machine", journal.Clone{ID: "01J9WIDGETCLONE00000000C", GitCommonDir: commonDir, Label: "dev"})

	_, err := ResolveCurrentClone(ctx, fx.root, dir)
	if !errors.Is(err, ErrUnknownClone) {
		t.Fatalf("ResolveCurrentClone = %v, want ErrUnknownClone (another machine's row must not match)", err)
	}
}

// TestResolveProject_BySlugAndID confirms both halves of the shape
// dispatch: a ULID-shaped locator by id, anything else by slug.
func TestResolveProject_BySlugAndID(t *testing.T) {
	src := NewSource()
	id := src.Next()

	fx := newRegFixture(t)
	fx.project(journal.Project{ID: id, Slug: "widget"})

	bySlug, err := ResolveProject(loadView(t, fx.root), "widget")
	if err != nil {
		t.Fatalf("ResolveProject by slug: %v", err)
	}
	if bySlug.Project.ID != id {
		t.Errorf("resolved id = %q, want %q", bySlug.Project.ID, id)
	}

	byID, err := ResolveProject(loadView(t, fx.root), id)
	if err != nil {
		t.Fatalf("ResolveProject by id: %v", err)
	}
	if byID.Project.Slug != "widget" {
		t.Errorf("resolved slug = %q, want %q", byID.Project.Slug, "widget")
	}
}

// TestResolveProject_NotFound covers both shapes' distinct not-found case:
// an unknown slug and a well-shaped but unregistered id.
func TestResolveProject_NotFound(t *testing.T) {
	fx := newRegFixture(t)
	fx.project(journal.Project{ID: NewSource().Next(), Slug: "widget"})
	view := loadView(t, fx.root)

	if _, err := ResolveProject(view, "nope"); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("ResolveProject(slug nope) = %v, want ErrUnknownProject", err)
	}
	if _, err := ResolveProject(view, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("ResolveProject(unregistered id) = %v, want ErrUnknownProject", err)
	}
}

// TestResolveClone_ByLabelAndID confirms shape dispatch within a resolved
// project's own clones.
func TestResolveClone_ByLabelAndID(t *testing.T) {
	cloneID := NewSource().Next()

	fx := newRegFixture(t)
	fx.project(journal.Project{ID: NewSource().Next(), Slug: "widget"})
	fx.clone("widget", "laptop", journal.Clone{ID: cloneID, GitCommonDir: "/x/.git", Label: "dev"})

	pv, err := ResolveProject(loadView(t, fx.root), "widget")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	byLabel, err := ResolveClone(pv, "dev")
	if err != nil {
		t.Fatalf("ResolveClone by label: %v", err)
	}
	if byLabel.ID != cloneID {
		t.Errorf("resolved id = %q, want %q", byLabel.ID, cloneID)
	}

	byID, err := ResolveClone(pv, cloneID)
	if err != nil {
		t.Fatalf("ResolveClone by id: %v", err)
	}
	if byID.Label != "dev" {
		t.Errorf("resolved label = %q, want %q", byID.Label, "dev")
	}
}

// TestResolveClone_NotFound covers both shapes' distinct not-found case.
func TestResolveClone_NotFound(t *testing.T) {
	fx := newRegFixture(t)
	fx.project(journal.Project{ID: NewSource().Next(), Slug: "widget"})
	fx.clone("widget", "laptop", journal.Clone{ID: NewSource().Next(), GitCommonDir: "/x/.git", Label: "dev"})

	pv, err := ResolveProject(loadView(t, fx.root), "widget")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	if _, err := ResolveClone(pv, "nope"); !errors.Is(err, ErrUnknownClone) {
		t.Errorf("ResolveClone(label nope) = %v, want ErrUnknownClone", err)
	}
	if _, err := ResolveClone(pv, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, ErrUnknownClone) {
		t.Errorf("ResolveClone(unregistered id) = %v, want ErrUnknownClone", err)
	}
}

// TestResolveClone_AmbiguousLabelAcrossMachinesRefuses confirms M16's
// refuse-over-guess: two machines independently registering a clone of the
// same project under the same label (legal — each machine cannot see the
// other's labels at registration time) is an ambiguity error naming both
// candidates, never a guess at one.
func TestResolveClone_AmbiguousLabelAcrossMachinesRefuses(t *testing.T) {
	idA := NewSource().Next()
	idB := NewSource().Next()

	fx := newRegFixture(t)
	fx.project(journal.Project{ID: NewSource().Next(), Slug: "widget"})
	fx.clone("widget", "laptop", journal.Clone{ID: idA, GitCommonDir: "/a/.git", Label: "dev"})
	fx.clone("widget", "desktop", journal.Clone{ID: idB, GitCommonDir: "/b/.git", Label: "dev"})

	pv, err := ResolveProject(loadView(t, fx.root), "widget")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	_, err = ResolveClone(pv, "dev")
	if err == nil {
		t.Fatal("ResolveClone(dev) succeeded despite a cross-machine label collision, want AmbiguousLocatorError")
	}
	var ambiguous *AmbiguousLocatorError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("ResolveClone(dev) error = %v (%T), want *AmbiguousLocatorError", err, err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Errorf("Candidates = %v, want 2 entries", ambiguous.Candidates)
	}
}

func loadView(t *testing.T, root string) View {
	t.Helper()
	view, diags, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("Load diags = %+v, want none", diags)
	}
	return view
}
