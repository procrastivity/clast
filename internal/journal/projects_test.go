package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListProjects_MissingJournalIsEmptyNotError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	projects, diags, err := ListProjects(root)
	if err != nil {
		t.Fatalf("ListProjects on a missing journal returned an error: %v", err)
	}
	if len(projects) != 0 || len(diags) != 0 {
		t.Errorf("ListProjects = projects:%+v diags:%+v, want both empty", projects, diags)
	}
}

func TestListProjects_MissingProjectsDirIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRoot(root); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	projects, diags, err := ListProjects(root)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 || len(diags) != 0 {
		t.Errorf("ListProjects = projects:%+v diags:%+v, want both empty", projects, diags)
	}
}

func TestListProjects_EnumeratesInSlugOrder(t *testing.T) {
	fx := newJournalFixture(t)
	root := fx.Root()

	fx.Project("widget", Project{ID: "01J9WIDGET", Slug: "widget", Remote: "github.com/acme/widget"})
	fx.Project("anvil", Project{ID: "01J9ANVIL", Slug: "anvil"})

	projects, diags, err := ListProjects(root)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %+v, want 2", projects)
	}
	if projects[0].Slug != "anvil" || projects[1].Slug != "widget" {
		t.Errorf("projects = %+v, want anvil then widget (slug order)", projects)
	}
	if projects[1].Remote != "github.com/acme/widget" {
		t.Errorf("widget.Remote = %q, want %q", projects[1].Remote, "github.com/acme/widget")
	}
}

func TestListProjects_MalformedProjectJSONCountedAndSkipped(t *testing.T) {
	fx := newJournalFixture(t)
	root := fx.Root()
	fx.Project("good", Project{ID: "01J9GOOD", Slug: "good"})

	badDir := ProjectDir(root, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(ProjectJSONPath(root, "bad"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write garbage project.json: %v", err)
	}

	projects, diags, err := ListProjects(root)
	if err != nil {
		t.Fatalf("ListProjects returned a hard error for a malformed project.json: %v", err)
	}
	if len(projects) != 1 || projects[0].Slug != "good" {
		t.Errorf("projects = %+v, want only %q", projects, "good")
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	if diags[0].Path != ProjectJSONPath(root, "bad") {
		t.Errorf("diags[0].Path = %q, want %q", diags[0].Path, ProjectJSONPath(root, "bad"))
	}
}

func TestListProjects_MissingProjectJSONCountedAndSkipped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(ProjectDir(root, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	projects, diags, err := ListProjects(root)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("projects = %+v, want none", projects)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
}

func TestListProjects_SlugMismatchCountedAndSkipped(t *testing.T) {
	fx := newJournalFixture(t)
	root := fx.Root()
	// project.json's own slug field disagrees with the directory it lives
	// under — a hand-edited (or corrupted) document, the M11-style identity
	// mismatch this function refuses to silently trust either side of.
	fx.Project("dirname-says-this", Project{ID: "01J9X", Slug: "project-json-says-this"})

	projects, diags, err := ListProjects(root)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("projects = %+v, want none (slug mismatch)", projects)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
}

func TestListClones_MissingProjectIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	clones, diags, err := ListClones(root, "never-registered")
	if err != nil {
		t.Fatalf("ListClones: %v", err)
	}
	if len(clones) != 0 || len(diags) != 0 {
		t.Errorf("ListClones = clones:%+v diags:%+v, want both empty", clones, diags)
	}
}

func TestListClones_EnumeratesAcrossMachinesInMachineOrder(t *testing.T) {
	fx := newJournalFixture(t)
	root := fx.Root()
	fx.Project("widget", Project{ID: "01J9WIDGET", Slug: "widget"})
	fx.Clones("widget", ClonesFile{
		Machine: "laptop",
		Clones:  []Clone{{ID: "01J9C1", GitCommonDir: "/home/dev/Code/widget/.git", Label: "dev"}},
	})
	fx.Clones("widget", ClonesFile{
		Machine: "desktop",
		Clones: []Clone{
			{ID: "01J9C2", GitCommonDir: "/home/dev/Code/widget-perf/.git", Label: "perf"},
			{ID: "01J9C3", GitCommonDir: "/home/dev/Code/widget-review/.git", Label: "review"},
		},
	})

	clones, diags, err := ListClones(root, "widget")
	if err != nil {
		t.Fatalf("ListClones: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if len(clones) != 3 {
		t.Fatalf("clones = %+v, want 3", clones)
	}
	// "desktop" sorts before "laptop": deterministic file order.
	if clones[0].Machine != "desktop" || clones[0].ID != "01J9C2" {
		t.Errorf("clones[0] = %+v, want desktop/01J9C2 first", clones[0])
	}
	if clones[1].Machine != "desktop" || clones[1].ID != "01J9C3" {
		t.Errorf("clones[1] = %+v, want desktop/01J9C3 second", clones[1])
	}
	if clones[2].Machine != "laptop" || clones[2].ID != "01J9C1" {
		t.Errorf("clones[2] = %+v, want laptop/01J9C1 third", clones[2])
	}
}

func TestListClones_MalformedClonesJSONCountedAndSkipped(t *testing.T) {
	fx := newJournalFixture(t)
	root := fx.Root()
	fx.Project("widget", Project{ID: "01J9WIDGET", Slug: "widget"})
	fx.Clones("widget", ClonesFile{
		Machine: "laptop",
		Clones:  []Clone{{ID: "01J9C1", GitCommonDir: "/x/.git", Label: "dev"}},
	})

	if err := os.WriteFile(ClonesJSONPath(root, "widget", "desktop"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write garbage clones file: %v", err)
	}

	clones, diags, err := ListClones(root, "widget")
	if err != nil {
		t.Fatalf("ListClones returned a hard error for a malformed clones file: %v", err)
	}
	if len(clones) != 1 || clones[0].Machine != "laptop" {
		t.Errorf("clones = %+v, want only laptop's row", clones)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	if diags[0].Path != ClonesJSONPath(root, "widget", "desktop") {
		t.Errorf("diags[0].Path = %q, want %q", diags[0].Path, ClonesJSONPath(root, "widget", "desktop"))
	}
}

func TestListClones_MachineMismatchCountedAndSkipped(t *testing.T) {
	fx := newJournalFixture(t)
	root := fx.Root()
	fx.Project("widget", Project{ID: "01J9WIDGET", Slug: "widget"})

	// Hand-write a clones file whose own Machine field disagrees with the
	// <machine> its filename names.
	if err := WriteClones(root, "widget", ClonesFile{
		Machine: "laptop",
		Clones:  []Clone{{ID: "01J9C1", GitCommonDir: "/x/.git", Label: "dev"}},
	}); err != nil {
		t.Fatalf("WriteClones: %v", err)
	}
	// Rename it to pretend it was written as "desktop"'s file.
	if err := os.Rename(ClonesJSONPath(root, "widget", "laptop"), ClonesJSONPath(root, "widget", "desktop")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	clones, diags, err := ListClones(root, "widget")
	if err != nil {
		t.Fatalf("ListClones: %v", err)
	}
	if len(clones) != 0 {
		t.Errorf("clones = %+v, want none (machine mismatch)", clones)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
}

func TestHostname_ResolvesThroughTheSharedSeam(t *testing.T) {
	origHostname := hostname
	hostname = func() (string, error) { return "fixture-machine", nil }
	defer func() { hostname = origHostname }()

	got, err := Hostname()
	if err != nil {
		t.Fatalf("Hostname: %v", err)
	}
	if got != "fixture-machine" {
		t.Errorf("Hostname() = %q, want %q", got, "fixture-machine")
	}
}
