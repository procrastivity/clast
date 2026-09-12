package entry

import (
	"errors"
	"os"
	"path/filepath"
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

// TestParse_ErrorSentinels_DistinguishFrontmatterFromTitle asserts that
// errors.Is can tell V14's two validation conditions apart (state-verbs
// step 02: curate's command layer reports them under separate codes,
// validation.entry-frontmatter vs validation.entry-title, because a
// skill acting on the failure wants to know which one).
func TestParse_ErrorSentinels_DistinguishFrontmatterFromTitle(t *testing.T) {
	frontmatterCases := map[string][]byte{
		"missing opening delimiter": []byte("title: no fence\n---\n\nbody\n"),
		"missing closing delimiter": []byte("---\ntitle: unterminated\n\nbody\n"),
		"malformed yaml":            []byte("---\ntitle: [unterminated\n---\n\nbody\n"),
	}
	for name, data := range frontmatterCases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(data)
			if !errors.Is(err, ErrInvalidFrontmatter) {
				t.Errorf("Parse error = %v, want errors.Is(err, ErrInvalidFrontmatter)", err)
			}
			if errors.Is(err, ErrMissingTitle) {
				t.Errorf("Parse error = %v, want NOT errors.Is(err, ErrMissingTitle)", err)
			}
		})
	}

	titleCases := map[string][]byte{
		"empty title":       []byte("---\ntitle: \"\"\ntags: []\n---\n\nbody\n"),
		"missing title key": []byte("---\ntags: []\n---\n\nbody\n"),
	}
	for name, data := range titleCases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(data)
			if !errors.Is(err, ErrMissingTitle) {
				t.Errorf("Parse error = %v, want errors.Is(err, ErrMissingTitle)", err)
			}
			if errors.Is(err, ErrInvalidFrontmatter) {
				t.Errorf("Parse error = %v, want NOT errors.Is(err, ErrInvalidFrontmatter)", err)
			}
		})
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
