package pi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/procrastivity/clast/internal/source"
)

// resolved is the candidate root list plus the diagnostics resolving it
// raised.
type resolved struct {
	roots []string
	diags []source.Diagnostic
	// err is set only when no candidate root could be built at all.
	err error
}

func (s *Source) resolveRoots() resolved {
	if s.fixedRoot != "" {
		return resolved{roots: []string{s.fixedRoot}}
	}
	var out resolved
	home, homeErr := s.env.home()
	if homeErr != nil {
		out.diags = append(out.diags, source.Diagnostic{Path: "$HOME", Err: errors.New("pi: resolving home directory: " + homeErr.Error())})
	}

	// override validates one path override: expanded, must be absolute.
	override := func(origin, v string) (string, bool) {
		p, ok := expandHome(v, home, homeErr == nil)
		if !ok || !filepath.IsAbs(p) {
			out.diags = append(out.diags, source.Diagnostic{
				Path: origin,
				Err:  errors.New("pi: " + origin + ": relative or unresolvable path " + `"` + v + `"` + " ignored (Pi resolves it against its own cwd)"),
			})
			return "", false
		}
		return filepath.Clean(p), true
	}

	defaultAgent := ""
	if homeErr == nil {
		defaultAgent = filepath.Join(home, ".pi", "agent")
	}
	agentDir := defaultAgent
	if v := s.env.getenv("PI_CODING_AGENT_DIR"); v != "" {
		if p, ok := override("$PI_CODING_AGENT_DIR", v); ok {
			agentDir = p
		}
	}

	var cands []string
	haveEffective := false
	if v := s.env.getenv("PI_CODING_AGENT_SESSION_DIR"); v != "" {
		if p, ok := override("$PI_CODING_AGENT_SESSION_DIR", v); ok {
			cands = append(cands, p)
			haveEffective = true
		}
	}
	if !haveEffective && agentDir != "" {
		settings := filepath.Join(agentDir, "settings.json")
		dir, diag := readSettingsSessionDir(settings)
		if diag != nil {
			out.diags = append(out.diags, *diag)
		}
		if dir != "" {
			if p, ok := override(settings+" sessionDir", dir); ok {
				cands = append(cands, p)
			}
		}
	}
	if agentDir != "" {
		cands = append(cands, filepath.Join(agentDir, "sessions"))
	}
	if defaultAgent != "" {
		cands = append(cands, filepath.Join(defaultAgent, "sessions"))
	}

	seen := map[string]bool{}
	for _, c := range cands {
		key := c
		if r, err := filepath.EvalSymlinks(c); err == nil {
			key = r
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out.roots = append(out.roots, c)
	}
	if len(out.roots) == 0 {
		out.err = errors.New("pi: no session root could be resolved")
		if homeErr != nil {
			out.err = homeErr
		}
	}
	return out
}

// expandHome expands "~" and "~/..." like Pi. ok is false when a tilde
// path cannot be expanded.
func expandHome(v, home string, haveHome bool) (string, bool) {
	if v == "~" || strings.HasPrefix(v, "~/") {
		if !haveHome {
			return "", false
		}
		return filepath.Join(home, v[1:]), true
	}
	return v, true
}

// readSettingsSessionDir reads sessionDir from the global settings file.
// A missing file is silent; unreadable or malformed is a diagnostic and
// reads as "no sessionDir".
func readSettingsSessionDir(path string) (string, *source.Diagnostic) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", &source.Diagnostic{Path: path, Err: err}
	}
	// Deliberate discard: read-only file.
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return "", &source.Diagnostic{Path: path, Err: err}
	}
	var doc struct {
		SessionDir *string `json:"sessionDir"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", &source.Diagnostic{Path: path, Err: errors.New("pi: " + path + ": malformed settings.json: " + err.Error())}
	}
	if doc.SessionDir == nil {
		return "", nil
	}
	return *doc.SessionDir, nil
}

// probeDir reports whether path is a readable directory, cheaply.
func probeDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	// Deliberate discard: read-only handle.
	defer func() { _ = f.Close() }()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Present implements source.Presence: nil iff at least one candidate
// root is a readable directory; otherwise the error for the effective
// root (an os.PathError naming path and reason, verbatim).
func (s *Source) Present(_ context.Context) error {
	r := s.resolveRoots()
	if r.err != nil {
		return r.err
	}
	var first error
	for _, root := range r.roots {
		err := probeDir(root)
		if err == nil {
			return nil
		}
		if first == nil {
			first = err
		}
	}
	return first
}
