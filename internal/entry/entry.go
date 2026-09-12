// Package entry parses and writes entry.md documents (MODEL §4): minimal
// YAML frontmatter (title, tags) followed by a Markdown body. It is the
// M9-boundary read layer the store package's own Brief keeps out of
// internal/journal on purpose — journal only ever names entry.md's path
// (journal.EntryPath) and stats its presence; parsing the document's
// content, and validating a caller-supplied one before it is written,
// belongs here instead.
//
// Two Matters share this package rather than forking their own copies of
// it (query-verbs step 01): query-verbs' `show` reads a curated session's
// title and body (SURFACE V18), and state-verbs' `curate` validates a
// complete entry.md read from stdin or --file before writing it (SURFACE
// V14 — "validates only that frontmatter parses and title is present").
package entry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// delimiter is the frontmatter fence line, exactly as MODEL §4's own
// entry.md example writes it.
const delimiter = "---"

// Frontmatter is entry.md's YAML frontmatter (MODEL §4): title and tags,
// and nothing that duplicates session.json — the context for those facts
// lives one file over.
type Frontmatter struct {
	Title string   `yaml:"title"`
	Tags  []string `yaml:"tags"`
}

// Entry is one fully parsed entry.md: its frontmatter plus body. Promoted
// items (decision / common issue / workflow, MODEL §9) stay `##` sections
// inside Body — this package never parses the body's own structure.
type Entry struct {
	Frontmatter
	Body string
}

// Parse parses a complete entry.md document and validates it exactly per
// V14's posture: frontmatter must be well-formed YAML delimited by "---"
// lines, and it must carry a non-empty title. Nothing else is validated —
// the body is the caller's, untouched.
func Parse(data []byte) (Entry, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return Entry{}, err
	}

	var front Frontmatter
	if err := yaml.Unmarshal(fm, &front); err != nil {
		return Entry{}, fmt.Errorf("entry: parsing frontmatter: %w", err)
	}
	if strings.TrimSpace(front.Title) == "" {
		return Entry{}, fmt.Errorf("entry: frontmatter must set a non-empty title")
	}

	return Entry{Frontmatter: front, Body: body}, nil
}

// splitFrontmatter divides data into its frontmatter block (the text
// between the opening and closing "---" lines) and everything after the
// closing delimiter, with at most one leading blank line trimmed off the
// body (the conventional blank line separating frontmatter from prose,
// per MODEL §4's own example — "---\ntitle: …\ntags: []\n---\n\nbody\n").
func splitFrontmatter(data []byte) (frontmatter []byte, body string, err error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != delimiter {
		return nil, "", fmt.Errorf("entry: document must start with a %q frontmatter delimiter", delimiter)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != delimiter {
			continue
		}
		fm := strings.Join(lines[1:i], "\n")
		rest := strings.Join(lines[i+1:], "\n")
		rest = strings.TrimPrefix(rest, "\n")
		return []byte(fm), rest, nil
	}
	return nil, "", fmt.Errorf("entry: frontmatter has no closing %q delimiter", delimiter)
}

// Read reads and parses path (typically journal.EntryPath's result). A
// missing file returns (Entry{}, false, nil) — the store package's own
// read posture (a missing document is never an error), extended here
// since query verbs read entry.md the same way they read the small JSON
// documents. A present-but-malformed file is an error, not a silent
// empty result: entry.md is never written except through a path that
// already validated it (Parse), so a malformed one on disk is a real
// problem, not a document consumers should recover from as if absent.
func Read(path string) (Entry, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("entry: reading %s: %w", path, err)
	}
	e, err := Parse(data)
	if err != nil {
		return Entry{}, true, fmt.Errorf("entry: parsing %s: %w", path, err)
	}
	return e, true, nil
}

// Write atomically writes data — a complete entry.md document, already
// validated by Parse — to path via temp-file-and-rename: the same MODEL
// §4 write discipline the store package applies to its own JSON
// documents, applied here instead of there because entry.md's content
// (and so its writing) stays out of internal/journal by the M9 boundary.
// path is typically journal.EntryPath's result; Write creates path's
// parent directory as needed, mirroring the store package's own write
// primitives.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("entry: creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("entry: creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// Deliberate discard: a no-op once the rename below succeeds, and
	// there is nothing more useful to do with a cleanup failure here.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("entry: writing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("entry: closing %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("entry: renaming %s to %s: %w", tmpPath, path, err)
	}
	return nil
}
