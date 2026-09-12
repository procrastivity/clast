package entry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_WellFormedDocument(t *testing.T) {
	data := []byte("---\ntitle: fixing the flaky test\ntags: [testing, ci]\n---\n\nThe test flaked because of a race.\n")
	e, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Title != "fixing the flaky test" {
		t.Errorf("Title = %q, want %q", e.Title, "fixing the flaky test")
	}
	if len(e.Tags) != 2 || e.Tags[0] != "testing" || e.Tags[1] != "ci" {
		t.Errorf("Tags = %+v, want [testing ci]", e.Tags)
	}
	if e.Body != "The test flaked because of a race.\n" {
		t.Errorf("Body = %q, want the trailing prose", e.Body)
	}
}

func TestParse_NoTagsStillParses(t *testing.T) {
	data := []byte("---\ntitle: minimal entry\n---\n\nbody\n")
	e, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Title != "minimal entry" {
		t.Errorf("Title = %q, want %q", e.Title, "minimal entry")
	}
	if len(e.Tags) != 0 {
		t.Errorf("Tags = %+v, want none", e.Tags)
	}
}

func TestParse_MissingOpeningDelimiterRefused(t *testing.T) {
	_, err := Parse([]byte("title: no fence\n---\n\nbody\n"))
	if err == nil {
		t.Fatal("Parse: want an error for a document with no opening delimiter")
	}
}

func TestParse_MissingClosingDelimiterRefused(t *testing.T) {
	_, err := Parse([]byte("---\ntitle: unterminated\n\nbody\n"))
	if err == nil {
		t.Fatal("Parse: want an error for a document whose frontmatter never closes")
	}
}

func TestParse_MalformedYAMLRefused(t *testing.T) {
	_, err := Parse([]byte("---\ntitle: [unterminated\n---\n\nbody\n"))
	if err == nil {
		t.Fatal("Parse: want an error for frontmatter that doesn't parse as YAML")
	}
}

func TestParse_EmptyTitleRefused(t *testing.T) {
	_, err := Parse([]byte("---\ntitle: \"\"\ntags: []\n---\n\nbody\n"))
	if err == nil {
		t.Fatal("Parse: want an error for an empty title (V14: title must be present)")
	}
}

func TestParse_MissingTitleKeyRefused(t *testing.T) {
	_, err := Parse([]byte("---\ntags: []\n---\n\nbody\n"))
	if err == nil {
		t.Fatal("Parse: want an error when the title key is absent entirely")
	}
}

func TestRead_MissingFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-written", "entry.md")
	e, ok, err := Read(path)
	if err != nil {
		t.Fatalf("Read on a missing file returned an error: %v", err)
	}
	if ok {
		t.Errorf("Read ok = true, want false for a missing file")
	}
	if e.Title != "" || len(e.Tags) != 0 || e.Body != "" {
		t.Errorf("Read = %+v, want the zero value", e)
	}
}

func TestRead_MalformedFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.md")
	if err := os.WriteFile(path, []byte("not an entry at all"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, ok, err := Read(path)
	if err == nil {
		t.Fatal("Read: want an error for a malformed entry.md, not a silent empty result")
	}
	if !ok {
		t.Errorf("Read ok = false, want true — the file exists, it just doesn't parse")
	}
}

func TestWrite_ThenReadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "2026-09-12", "claude-abc", "entry.md")
	data := []byte("---\ntitle: round trip\ntags: [a, b]\n---\n\nsome body text\n")

	if err := Write(path, data); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("written bytes = %q, want %q", got, data)
	}

	e, ok, err := Read(path)
	if err != nil || !ok {
		t.Fatalf("Read: ok=%v err=%v", ok, err)
	}
	if e.Title != "round trip" {
		t.Errorf("Title = %q, want %q", e.Title, "round trip")
	}
}

func TestWrite_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "entry.md")
	if err := Write(path, []byte("---\ntitle: nested\n---\n\nbody\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat after Write: %v", err)
	}
}

func TestWrite_NoStrayTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.md")
	if err := Write(path, []byte("---\ntitle: clean\n---\n\nbody\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("stray temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "entry.md" {
		t.Errorf("dir entries = %+v, want exactly entry.md", entries)
	}
}
