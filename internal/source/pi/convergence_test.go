package pi_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func registerClone(t *testing.T, root, slug, dir string) {
	t.Helper()
	commonDir, err := registry.CommonDir(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := journal.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteProject(root, slug, journal.Project{ID: "01TESTPROJECT0000000000000", Slug: slug}); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteClones(root, slug, journal.ClonesFile{
		Machine: machine,
		Clones:  []journal.Clone{{ID: "01TESTCLONE00000000000000", GitCommonDir: commonDir, Label: slug}},
	}); err != nil {
		t.Fatal(err)
	}
}

// cwdSession is a two-turn session whose header carries cwdField
// verbatim (a full `"cwd":...` fragment, or "" for none).
func cwdSession(id, cwdField string) string {
	h := fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":%q%s}`+"\n", id, sec(0), cwdField)
	t1, last := turn(1, "", 1)
	t2, _ := turn(2, last, 3)
	return h + t1 + t2
}

func cwdOf(p string) string { return fmt.Sprintf(`,"cwd":%q`, p) }

func (r *rig) writeCwd(id, cwdField string) { r.write(id, cwdSession(id, cwdField)) }

func TestConvergenceProjectBackfill(t *testing.T) {
	repo := gitRepo(t)
	r := newRig(t, false)
	id := idPrefix + "0000002a01"
	r.writeCwd(id, cwdOf(repo))
	r.sweep()
	first, dir := r.find(id)
	if first.Project != nil {
		t.Fatalf("unregistered cwd resolved a project: %+v", first.Project)
	}
	art := filepath.Join(dir, "transcript.jsonl")
	artBefore := treeState(t, dir)[art]

	registerClone(t, r.jr, "demo", repo)
	c := r.sweep() // source file untouched
	if len(c) != 1 || !c[0].ProjectBackfilled || c[0].Recaptured {
		t.Fatalf("want a pure backfill, got %+v", c)
	}
	got, _ := r.find(id)
	if got.Project == nil || got.Project.Slug != "demo" || got.Project.Path != repo {
		t.Fatalf("project = %+v", got.Project)
	}
	if got.Counts != first.Counts || got.Transcript != first.Transcript || !got.CapturedAt.Equal(first.CapturedAt) {
		t.Error("backfill changed capture facts")
	}
	// The artifact is untouched: wrong shortcut is a full recapture.
	if treeState(t, dir)[art] != artBefore {
		t.Error("backfill rewrote the artifact")
	}
	if c := r.sweep(); len(c) != 0 {
		t.Errorf("post-backfill sweep: %+v", c)
	}
}

func TestConvergenceProjectlessStaysProjectless(t *testing.T) {
	repo := gitRepo(t)
	r := newRig(t, false)
	none, rel := idPrefix+"0000002b01", idPrefix+"0000002b02"
	r.writeCwd(none, "")
	r.writeCwd(rel, cwdOf("demo/sub"))
	r.sweep()
	registerClone(t, r.jr, "demo", repo)
	for i := 0; i < 2; i++ {
		if c := r.sweep(); len(c) != 0 {
			t.Fatalf("sweep %d backfilled a session without cwd evidence: %+v", i, c)
		}
	}
	for _, id := range []string{none, rel} {
		if s, _ := r.find(id); s.Project != nil {
			t.Errorf("%s gained project %+v", id, s.Project)
		}
	}
}

// TestConvergenceSymlinkedCwd records B10 as observed: the registry
// keys on git's absolute common-dir, which git resolves, so a header
// cwd given through a symlink to a registered checkout resolves to the
// same project. The session's Project.Path stays the literal header
// cwd (no EvalSymlinks in clast).
func TestConvergenceSymlinkedCwd(t *testing.T) {
	repo := gitRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, false)
	id := idPrefix + "0000002c01"
	r.writeCwd(id, cwdOf(link))
	registerClone(t, r.jr, "demo", repo)
	r.sweep()
	s, _ := r.find(id)
	if s.Project == nil || s.Project.Slug != "demo" || s.Project.Path != link {
		t.Fatalf("symlinked cwd: project = %+v", s.Project)
	}
}

func TestConvergenceCurationSurvivesRecapture(t *testing.T) {
	r := newRig(t, false)
	dis, cur := idPrefix+"0000002d01", idPrefix+"0000002d02"
	r.write(dis, twoTurns(dis))
	r.write(cur, twoTurns(cur))
	c := r.sweep()
	shard := map[string]string{}
	for _, x := range c {
		shard[x.Key.NativeID] = x.Shard
	}
	disKey := journal.SessionKey{Harness: "pi", NativeID: dis}
	curKey := journal.SessionKey{Harness: "pi", NativeID: cur}
	reason := "not interesting"
	now := time.Unix(1792000000, 0).UTC()
	if err := journal.WriteCuration(r.jr, shard[dis], disKey, journal.Curation{
		State: journal.StateDismissed, At: now, Machine: "m", Reason: &reason,
	}); err != nil {
		t.Fatal(err)
	}
	s, _ := r.find(cur)
	if err := journal.WriteEntry(r.jr, shard[cur], curKey, []byte("---\ntitle: t\ntags: []\n---\nkept\n")); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteCuration(r.jr, shard[cur], curKey, journal.Curation{
		State: journal.StateCurated, At: now, Machine: "m",
		TranscriptAtCuration: &journal.TranscriptStamp{Lines: s.Transcript.Lines, SHA256: s.Transcript.SHA256},
	}); err != nil {
		t.Fatal(err)
	}
	curation := func(key journal.SessionKey) (string, string) {
		_, sdir := r.find(key.NativeID)
		a, err := os.ReadFile(filepath.Join(sdir, "curation.json"))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(sdir, "entry.md"))
		return string(a), string(b)
	}
	disC, _ := curation(disKey)
	curC, curE := curation(curKey)

	t3, _ := turn(3, "a2", 5)
	steps := []struct {
		name string
		do   func(id string)
	}{
		{"append", func(id string) { r.append(id, t3) }},
		{"rewrite", func(id string) {
			r.write(id, strings.ReplaceAll(r.srcBytes(id), "FIXTURE-OK", "REWRITTEN-LONGER"))
		}},
		{"malformed tail", func(id string) { r.append(id, "{bad line\n"+`{"type":"mess`) }},
	}
	for _, st := range steps {
		st.do(dis)
		st.do(cur)
		c, _, _, err := r.run(t.Context())
		if err != nil || len(c) != 2 {
			t.Fatalf("%s: c=%+v err=%v", st.name, c, err)
		}
		if a, _ := curation(disKey); a != disC {
			t.Errorf("%s: dismissal rewritten", st.name)
		}
		if a, e := curation(curKey); a != curC || e != curE {
			t.Errorf("%s: curated record or entry rewritten", st.name)
		}
		for _, id := range []string{dis, cur} {
			if string(r.artifact(id)) != r.srcBytes(id) || r.turnsOf(id) == 0 {
				t.Errorf("%s: %s not readable/converged", st.name, id)
			}
		}
	}
	// Growth is disclosed, not hidden: the curated session is stale.
	items, _, _ := journal.Walk(r.jr)
	for _, it := range items {
		if it.Key == curKey && !it.Stale() {
			t.Error("curated session not stale after its transcript moved")
		}
		if it.Key == disKey && it.Session.Counts.Assistant == 0 {
			t.Error("dismissed session lost facts")
		}
	}
}
