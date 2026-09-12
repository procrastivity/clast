// Package claude is the claude source (M12): Claude Code sessions as
// JSONL transcripts under ${CLAUDE_CONFIG_DIR:-~/.claude}/projects/.
// File-tail posture (M13): the transcript is copied verbatim, subagent
// sidecars included, and recapture re-copies grown files. The parsing
// here is ported-and-diverged from duo's claude runtime and
// transcript-tail's claude adapter (S5, HANDOFF §7): same envelope
// fields, same tolerant posture — an unparseable line is counted and
// skipped, never fatal — but this package extracts capture facts (M10)
// rather than conversation turns, so message text is never materialized.
//
// The unconsumed neighbors stay unconsumed (M12): sessions-index.json,
// history.jsonl, and the live ~/.claude/sessions/<pid>.json registry are
// not evidence this source reads.
package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/procrastivity/clast/internal/source"
)

// Name is the registry key and M11 identity prefix.
const Name = "claude"

// Source implements source.Source over one Claude Code config directory.
type Source struct {
	// configDir is the explicit root (the fixture seam); "" means
	// resolve ${CLAUDE_CONFIG_DIR:-~/.claude} at call time, so the
	// registry table's static New() can never fail — only a duty call
	// can, and it reports the failure as a duty error. There is no env
	// seam of clast's own here: CLAUDE_CONFIG_DIR is the harness's
	// convention, read the same way the harness reads it.
	configDir string
}

// New returns the claude source rooted at the real config directory:
// $CLAUDE_CONFIG_DIR when set, ~/.claude otherwise, resolved lazily.
func New() *Source {
	return &Source{}
}

// NewAt returns the claude source rooted at an explicit config
// directory — the fixture seam.
func NewAt(configDir string) *Source {
	return &Source{configDir: configDir}
}

// root resolves the config directory per Source.configDir's contract.
func (s *Source) root() (string, error) {
	if s.configDir != "" {
		return s.configDir, nil
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude: resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// Name implements source.Source.
func (s *Source) Name() string { return Name }

// Model implements source.Source: claude is file-tail (M13).
func (s *Source) Model() source.StorageModel { return source.FileTail }

// Discover enumerates every session transcript under
// <configDir>/projects/<cwd-slug>/<uuid>.jsonl. The native id is the
// file's base name — Claude Code names the file by its session uuid, and
// the id is also on every entry as sessionId, but discovery must not
// depend on parsing (M12 separates the duties), so the filename is the
// enumeration-time id. A missing projects/ directory means the harness
// has no sessions here: nil, nil, nil. Subagent sidecars live under a
// <uuid>/ directory, not as loose .jsonl files, so they never enumerate
// as sessions.
func (s *Source) Discover(_ context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	root, err := s.root()
	if err != nil {
		return nil, nil, err
	}
	projectsDir := filepath.Join(root, "projects")
	slugEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, []source.Diagnostic{{Path: projectsDir, Err: err}}, nil
	}

	var found []source.Discovered
	var diags []source.Diagnostic
	for _, slug := range slugEntries {
		if !slug.IsDir() {
			continue
		}
		slugDir := filepath.Join(projectsDir, slug.Name())
		files, err := os.ReadDir(slugDir)
		if err != nil {
			diags = append(diags, source.Diagnostic{Path: slugDir, Err: err})
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(slugDir, f.Name())
			info, err := f.Info()
			if err != nil {
				diags = append(diags, source.Diagnostic{Path: path, Err: err})
				continue
			}
			found = append(found, source.Discovered{
				NativeID: strings.TrimSuffix(f.Name(), ".jsonl"),
				Path:     path,
				ModTime:  info.ModTime(),
			})
		}
	}
	return found, diags, nil
}
