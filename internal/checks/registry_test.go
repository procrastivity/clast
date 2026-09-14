package checks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/verbs/initverb"
)

func checksRegistryRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	checksRegistryGit(t, dir, "init", "-q")
	checksRegistryGit(t, dir, "config", "user.email", "test@example.com")
	checksRegistryGit(t, dir, "config", "user.name", "test")
	return dir
}

func checksRegistryRemote(t *testing.T, dir, name, url string) {
	t.Helper()
	checksRegistryGit(t, dir, "remote", "add", name, url)
}

func checksRegistryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func checksRegistryRegister(t *testing.T, root, dir string) initverb.Result {
	t.Helper()
	result, err := initverb.Run(context.Background(), root, dir, initverb.Options{})
	if err != nil {
		t.Fatalf("initverb.Run: %v", err)
	}
	return result
}

func TestRegistryFindings_KeylessProjectOffersAdoptWithoutWriting(t *testing.T) {
	root := t.TempDir()
	dir := checksRegistryRepo(t, "widget")
	registered := checksRegistryRegister(t, root, dir)
	checksRegistryRemote(t, dir, "origin", "git@github.com:acme/widget.git")

	findings, err := RegistryFindings(context.Background(), root, dir)
	if err != nil {
		t.Fatalf("RegistryFindings: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != RegistryAdoptionCode {
		t.Fatalf("findings = %+v, want one %s finding", findings, RegistryAdoptionCode)
	}
	if !strings.Contains(findings[0].Message, "clast adopt") {
		t.Errorf("message = %q, want it to offer clast adopt", findings[0].Message)
	}
	project, ok, err := journal.ReadProject(root, registered.Project.Slug)
	if err != nil || !ok {
		t.Fatalf("ReadProject: ok=%v err=%v", ok, err)
	}
	if project.Remote != "" {
		t.Fatalf("doctor changed project remote to %q; report-only check must not write", project.Remote)
	}
}

func TestRegistryFindings_MovedCloneNamesRelinkCandidate(t *testing.T) {
	root := t.TempDir()
	dir := checksRegistryRepo(t, "widget")
	checksRegistryRemote(t, dir, "origin", "git@github.com:acme/widget.git")
	registered := checksRegistryRegister(t, root, dir)
	moved := filepath.Join(filepath.Dir(dir), "widget-moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatalf("move clone: %v", err)
	}

	findings, err := RegistryFindings(context.Background(), root, moved)
	if err != nil {
		t.Fatalf("RegistryFindings: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != RegistryMovedCloneCode {
		t.Fatalf("findings = %+v, want one %s finding", findings, RegistryMovedCloneCode)
	}
	message := findings[0].Message
	if !strings.Contains(message, "clast relink "+registered.Clone.ID) {
		t.Errorf("message = %q, want concrete relink candidate %q", message, registered.Clone.ID)
	}
	if !strings.Contains(message, "clast init") {
		t.Errorf("message = %q, want init alternative", message)
	}
	machine, err := journal.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	clones, ok, err := journal.ReadClones(root, registered.Project.Slug, machine)
	if err != nil || !ok {
		t.Fatalf("ReadClones: ok=%v err=%v", ok, err)
	}
	if len(clones.Clones) != 1 || clones.Clones[0].GitCommonDir != registered.Clone.GitCommonDir {
		t.Fatalf("doctor changed clone registry: %+v", clones.Clones)
	}
}

func TestRegistryFindings_ReportsEveryKnownRemote(t *testing.T) {
	root := t.TempDir()
	first := checksRegistryRepo(t, "first")
	checksRegistryRemote(t, first, "origin", "git@github.com:acme/first.git")
	checksRegistryRegister(t, root, first)
	second := checksRegistryRepo(t, "second")
	checksRegistryRemote(t, second, "origin", "git@github.com:acme/second.git")
	checksRegistryRegister(t, root, second)

	unknown := checksRegistryRepo(t, "unknown")
	checksRegistryRemote(t, unknown, "origin", "git@github.com:acme/first.git")
	checksRegistryRemote(t, unknown, "upstream", "https://github.com/acme/second.git")

	findings, err := RegistryFindings(context.Background(), root, unknown)
	if err != nil {
		t.Fatalf("RegistryFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want one finding for each known remote", findings)
	}
	for _, finding := range findings {
		if finding.Code != RegistryMovedCloneCode {
			t.Errorf("finding code = %q, want %q", finding.Code, RegistryMovedCloneCode)
		}
	}
}
