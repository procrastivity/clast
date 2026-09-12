package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CloneEntry is one Clone as enumerated by ListClones, together with the
// machine whose clones.<machine>.json file it was read from. Mirrors
// BreadcrumbEntry's shape (breadcrumbs.go): Clone itself carries no machine
// field of its own — that fact lives only in the file it came from — and
// readers glob across every machine's file rather than picking one (M5).
type CloneEntry struct {
	Clone
	Machine string
}

// ListProjects enumerates every projects/<slug>/project.json under root, in
// deterministic (slug) order. Follows Walk's own tolerant posture exactly: a
// missing projects/ directory (or a missing journal root entirely) is not an
// error — it returns no projects — and a project directory whose
// project.json is missing, malformed, or whose own Slug field disagrees with
// the directory it was found in (the M11 identity-mismatch check, applied
// here to a project's directory name rather than a session's) is skipped and
// counted in diags rather than failing the enumeration.
func ListProjects(root string) (projects []Project, diags []Diagnostic, err error) {
	projectsDir := filepath.Join(root, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("journal: reading %s: %w", projectsDir, err)
	}

	slugs := dirNames(entries)
	sort.Strings(slugs)

	for _, slug := range slugs {
		path := ProjectJSONPath(root, slug)
		project, present, err := readDocument[Project](path)
		if err != nil {
			diags = append(diags, Diagnostic{Path: path, Err: err})
			continue
		}
		if !present {
			diags = append(diags, Diagnostic{Path: path, Err: fmt.Errorf("journal: missing project.json")})
			continue
		}
		if project.Slug != slug {
			diags = append(diags, Diagnostic{Path: path, Err: fmt.Errorf("journal: project.json slug %q does not match its directory name %q", project.Slug, slug)})
			continue
		}
		projects = append(projects, project)
	}
	return projects, diags, nil
}

// ListClones enumerates every clones.<machine>.json for slug under root, in
// deterministic (machine, then registration order within that file) order —
// readers glob across every machine that has registered a clone rather than
// picking one (M5). A missing project directory (or no clones.*.json files
// at all) is not an error — it returns no clones. A clones file that fails
// to parse, or whose own Machine field disagrees with the <machine> its
// filename names, is skipped and counted in diags rather than failing the
// enumeration — the same identity-mismatch posture ListProjects and Walk
// both apply to their own trees.
func ListClones(root, slug string) (clones []CloneEntry, diags []Diagnostic, err error) {
	pattern := filepath.Join(ProjectDir(root, slug), "clones.*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("journal: globbing %s: %w", pattern, err)
	}
	sort.Strings(matches) // deterministic order across machines.

	for _, path := range matches {
		machine := machineFromClonesFilename(path)
		cf, present, err := readDocument[ClonesFile](path)
		if err != nil {
			diags = append(diags, Diagnostic{Path: path, Err: err})
			continue
		}
		if !present {
			diags = append(diags, Diagnostic{Path: path, Err: fmt.Errorf("journal: missing clones file")})
			continue
		}
		if cf.Machine != machine {
			diags = append(diags, Diagnostic{Path: path, Err: fmt.Errorf("journal: clones file machine %q does not match its filename %q", cf.Machine, machine)})
			continue
		}
		for _, c := range cf.Clones {
			clones = append(clones, CloneEntry{Clone: c, Machine: machine})
		}
	}
	return clones, diags, nil
}

// machineFromClonesFilename recovers <machine> from clones.<machine>.json.
func machineFromClonesFilename(path string) string {
	base := filepath.Base(path)
	trimmed := strings.TrimSuffix(base, ".json")
	return strings.TrimPrefix(trimmed, "clones.")
}
