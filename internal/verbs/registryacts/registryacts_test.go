package registryacts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/verbs/initverb"
)

var registryActsContext = context.Background()

func registryActsRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	registryActsGit(t, dir, "init", "-q")
	registryActsGit(t, dir, "config", "user.email", "test@example.com")
	registryActsGit(t, dir, "config", "user.name", "test")
	return dir
}

func registryActsRemote(t *testing.T, dir, name, url string) {
	t.Helper()
	registryActsGit(t, dir, "remote", "add", name, url)
}

func registryActsGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func registryActsRegister(t *testing.T, root, dir string) initverb.Result {
	t.Helper()
	result, err := initverb.Run(registryActsContext, root, dir, initverb.Options{})
	if err != nil {
		t.Fatalf("initverb.Run: %v", err)
	}
	return result
}

func registryActsMachine(t *testing.T) string {
	t.Helper()
	machine, err := journal.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

func registryActsCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", want)
	}
	var structured *clasterr.Error
	if !errors.As(err, &structured) {
		t.Fatalf("error %v (%T) is not structured", err, err)
	}
	if structured.Code != want {
		t.Fatalf("error code = %q, want %q", structured.Code, want)
	}
}

func TestLabel_RenamesCurrentClone(t *testing.T) {
	root := t.TempDir()
	dir := registryActsRepo(t, "widget")
	registryActsRemote(t, dir, "origin", "git@github.com:acme/widget.git")
	registered := registryActsRegister(t, root, dir)

	got, err := label(registryActsContext, root, dir, []string{"development"})
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if got != "development" {
		t.Fatalf("label result = %q, want %q", got, "development")
	}
	machine := registryActsMachine(t)
	clones, ok, err := journal.ReadClones(root, registered.Project.Slug, machine)
	if err != nil || !ok {
		t.Fatalf("ReadClones: ok=%v err=%v", ok, err)
	}
	if len(clones.Clones) != 1 || clones.Clones[0].Label != "development" {
		t.Fatalf("clones = %+v, want the registered clone relabeled", clones.Clones)
	}
}

func TestLabel_CollisionRefusesWithoutChangingJournal(t *testing.T) {
	root := t.TempDir()
	first := registryActsRepo(t, "first")
	registryActsRemote(t, first, "origin", "git@github.com:acme/widget.git")
	registryActsRegister(t, root, first)

	second := registryActsRepo(t, "second")
	registryActsRemote(t, second, "origin", "https://github.com/acme/widget.git")
	registered := registryActsRegister(t, root, second)

	_, err := label(registryActsContext, root, second, []string{"first"})
	registryActsCode(t, err, "validation.label-collision")

	clones, ok, err := journal.ReadClones(root, registered.Project.Slug, registryActsMachine(t))
	if err != nil || !ok {
		t.Fatalf("ReadClones: ok=%v err=%v", ok, err)
	}
	if len(clones.Clones) != 2 || clones.Clones[1].Label != "second" {
		t.Fatalf("clones = %+v, want the refused relabel to leave the second clone unchanged", clones.Clones)
	}
}

func TestAdopt_RecordsKeylessProjectRemote(t *testing.T) {
	root := t.TempDir()
	dir := registryActsRepo(t, "widget")
	registered := registryActsRegister(t, root, dir)
	registryActsRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	got, err := adopt(registryActsContext, root, dir, nil)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if got != "github.com/acme/widget" {
		t.Fatalf("adopt result = %q, want normalized remote", got)
	}
	project, ok, err := journal.ReadProject(root, registered.Project.Slug)
	if err != nil || !ok {
		t.Fatalf("ReadProject: ok=%v err=%v", ok, err)
	}
	if project.Remote != got || project.IdentityRemote != "origin" {
		t.Fatalf("project = %+v, want adopted remote and identity_remote origin", project)
	}
}

func TestRelink_ByIDPreservesCloneIdentityAfterMove(t *testing.T) {
	root := t.TempDir()
	original := registryActsRepo(t, "widget")
	registryActsRemote(t, original, "origin", "git@github.com:acme/widget.git")
	registered := registryActsRegister(t, root, original)
	moved := filepath.Join(filepath.Dir(original), "widget-moved")
	if err := os.Rename(original, moved); err != nil {
		t.Fatalf("move clone: %v", err)
	}

	got, err := relink(registryActsContext, root, moved, []string{registered.Clone.ID})
	if err != nil {
		t.Fatalf("relink: %v", err)
	}
	if got != registered.Clone.ID {
		t.Fatalf("relink result = %q, want original clone id %q", got, registered.Clone.ID)
	}
	machine := registryActsMachine(t)
	clones, ok, err := journal.ReadClones(root, registered.Project.Slug, machine)
	if err != nil || !ok {
		t.Fatalf("ReadClones: ok=%v err=%v", ok, err)
	}
	if clones.Clones[0].GitCommonDir == registered.Clone.GitCommonDir {
		t.Fatalf("GitCommonDir stayed %q after moving and relinking", clones.Clones[0].GitCommonDir)
	}
}

func TestRelink_ByLabelDeduplicatesRemoteAliases(t *testing.T) {
	root := t.TempDir()
	original := registryActsRepo(t, "widget")
	registryActsRemote(t, original, "origin", "git@github.com:acme/widget.git")
	registered := registryActsRegister(t, root, original)
	moved := filepath.Join(filepath.Dir(original), "widget-moved")
	if err := os.Rename(original, moved); err != nil {
		t.Fatalf("move clone: %v", err)
	}
	registryActsRemote(t, moved, "upstream", "https://github.com/acme/widget.git")

	got, err := relink(registryActsContext, root, moved, []string{registered.Clone.Label})
	if err != nil {
		t.Fatalf("relink by label: %v", err)
	}
	if got != registered.Clone.ID {
		t.Fatalf("relink result = %q, want original clone id %q", got, registered.Clone.ID)
	}
}
