package curate

import (
	"errors"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
)

func TestValidate_WellFormedDocument(t *testing.T) {
	data := []byte("---\ntitle: fixing the flaky test\ntags: [testing, ci]\n---\n\nThe test flaked.\n")
	e, err := validate(data)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if e.Title != "fixing the flaky test" {
		t.Errorf("Title = %q, want %q", e.Title, "fixing the flaky test")
	}
}

func TestValidate_InvalidFrontmatter_ReportsEntryFrontmatterCode(t *testing.T) {
	cases := map[string][]byte{
		"no opening delimiter": []byte("title: no fence\n---\n\nbody\n"),
		"no closing delimiter": []byte("---\ntitle: unterminated\n\nbody\n"),
		"malformed yaml":       []byte("---\ntitle: [unterminated\n---\n\nbody\n"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validate(data)
			assertCode(t, err, "validation.entry-frontmatter")
		})
	}
}

func TestValidate_MissingTitle_ReportsEntryTitleCode(t *testing.T) {
	cases := map[string][]byte{
		"empty title":       []byte("---\ntitle: \"\"\ntags: []\n---\n\nbody\n"),
		"missing title key": []byte("---\ntags: []\n---\n\nbody\n"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validate(data)
			assertCode(t, err, "validation.entry-title")
		})
	}
}

// assertCode fails t unless err is a *clasterr.Error carrying exactly code.
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error = %v (%T), want a *clasterr.Error", err, err)
	}
	if cerr.Code != code {
		t.Errorf("code = %q, want %q", cerr.Code, code)
	}
}
