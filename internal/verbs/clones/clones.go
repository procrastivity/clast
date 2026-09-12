// Package clones implements `clast plumbing clones [<project>]` (SURFACE
// V23): a read-only listing of registered clones. With a project locator
// it lists that project's clones (resolved via registry.ResolveProject);
// with none, it lists the current project's clones when the cwd resolves
// to a registered clone, or every project's clones otherwise. It never
// mutates the journal.
package clones

import (
	"context"
	"errors"
	"fmt"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

// Row is one clone as Run lists it.
type Row struct {
	ProjectSlug  string
	ID           string
	Label        string
	GitCommonDir string
	// Machine names which machine registered this clone (a clone is
	// host-scoped, M15) — journal.CloneEntry's own field, carried through
	// unchanged.
	Machine string
	// Current marks the row identifiable as dir's own clone: same machine,
	// same git-common-dir. Best-effort — false whenever dir isn't inside a
	// git repo at all, never an error (V23: "nice-to-have, keep cheap").
	Current bool
}

// Run lists clones per V23's three cases:
//
//   - locator != "": that project's clones (ResolveProject; unregistered
//     locator maps to validation.unknown-locator, not a refusal — there is
//     no clone locator here for AmbiguousLocatorError to reach, so
//     registry.ErrUnknownProject is the only sentinel this call can see).
//   - locator == "" and dir resolves to a registered clone (this machine,
//     by common-dir): that clone's own project's clones.
//   - locator == "" and dir does not resolve (not a registered clone, or
//     not inside a git repo at all): every project's clones.
//
// Every row is checked against dir's own (best-effort) identity and
// marked Current when it matches — cheap since the identity is resolved
// once, up front, regardless of which of the three cases applies.
func Run(ctx context.Context, root, dir, locator string) ([]Row, error) {
	view, _, err := registry.Load(root)
	if err != nil {
		return nil, err
	}

	currentCommonDir, currentMachine, err := currentIdentity(ctx, dir)
	if err != nil {
		return nil, err
	}

	if locator != "" {
		pv, err := registry.ResolveProject(view, locator)
		if err != nil {
			if errors.Is(err, registry.ErrUnknownProject) {
				return nil, unknownProject(locator)
			}
			return nil, err
		}
		return rowsFor(pv, currentCommonDir, currentMachine), nil
	}

	if currentCommonDir != "" {
		for _, pv := range view.Projects {
			for _, c := range pv.Clones {
				if c.Machine == currentMachine && c.GitCommonDir == currentCommonDir {
					return rowsFor(pv, currentCommonDir, currentMachine), nil
				}
			}
		}
	}

	// No locator, and dir did not resolve to any registered clone (either
	// it isn't inside a git repo at all, or it is one nobody has
	// registered yet): every project's clones (V23).
	var rows []Row
	for _, pv := range view.Projects {
		rows = append(rows, rowsFor(pv, currentCommonDir, currentMachine)...)
	}
	return rows, nil
}

// currentIdentity resolves dir's own (common-dir, machine) pair,
// best-effort: "", "" when dir is not inside a git repository at all —
// not an error, since `clones` with no argument must still list
// everything from outside any project (V23's "all otherwise").
func currentIdentity(ctx context.Context, dir string) (commonDir, machine string, err error) {
	cd, cerr := registry.CommonDir(ctx, dir)
	if cerr != nil {
		return "", "", nil
	}
	m, err := journal.Hostname()
	if err != nil {
		return "", "", fmt.Errorf("clones: resolving local machine name: %w", err)
	}
	return cd, m, nil
}

// rowsFor converts pv's own clones into Rows, marking whichever one
// matches (currentCommonDir, currentMachine) — a no-op when
// currentCommonDir is "" (dir resolved to nothing).
func rowsFor(pv registry.ProjectView, currentCommonDir, currentMachine string) []Row {
	rows := make([]Row, 0, len(pv.Clones))
	for _, c := range pv.Clones {
		rows = append(rows, Row{
			ProjectSlug:  pv.Project.Slug,
			ID:           c.ID,
			Label:        c.Label,
			GitCommonDir: c.GitCommonDir,
			Machine:      c.Machine,
			Current:      currentCommonDir != "" && c.Machine == currentMachine && c.GitCommonDir == currentCommonDir,
		})
	}
	return rows
}

// unknownProject is clones' refusal-free not-found for an explicit project
// locator that resolves to nothing: validation.unknown-locator (wip's
// spelling for an unresolved locator, internal/tiers/resolve.go, ported
// verbatim per V34 — "unknown-harness" and "ambiguous-locator" are this
// codebase's own siblings of the same family). Exit 1 (validation), not a
// refusal: there is nothing clast declines to do on principle here, the
// argument just names nothing.
func unknownProject(locator string) error {
	return clasterr.New("validation.unknown-locator",
		fmt.Sprintf("%q matches no registered project, by id or slug", locator))
}
