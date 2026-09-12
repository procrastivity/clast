package registry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/procrastivity/clast/internal/journal"
)

// Ported-and-diverged from wip's internal/tiers/resolve.go and adopt.go
// (ResolveClone, ResolveRepo, ResolveCurrentClone, CurrentWorktreeName) —
// see wip's docs/tiers/decisions.md and clast-reboot's MODEL.md M15-M17 for
// the design history. Two divergences, both forced by clast's model rather
// than a reinterpretation of wip's:
//
//   - No store, no AdoptIfPossible. wip resolves against a live SQLite store
//     and opportunistically adopts a keyless Repo's remote on every
//     resolution. clast reads a journal snapshot instead (View, loaded via
//     Load) and remote adoption is explicitly deferred, tier-2 (MODEL M15) —
//     nothing here writes anything.
//   - No Worktree rows. wip births a Worktree row for every linked worktree
//     it meets; clast never does (M17/S7) — ResolveCurrentClone reports the
//     worktree name as a plain fact on CurrentClone, nothing is persisted.
//
// Clone-label resolution is also narrower than wip's ResolveClone: wip
// supports an unscoped (cross-repo) search when no repo is given. clast's
// only locator-resolving verb that reaches a clone (SURFACE V23's
// `clones [<project>]`) always resolves the project first, so ResolveClone
// here is scoped to one already-resolved ProjectView; there is no unscoped
// form to port.

// ErrUnknownProject is returned when a project locator (ResolveProject)
// matches nothing in the loaded View.
var ErrUnknownProject = errors.New("registry: no project matches the given locator")

// ErrUnknownClone is returned both when a clone locator (ResolveClone)
// matches nothing, and when ResolveCurrentClone finds no clone registered
// at the resolved common-dir among this machine's own rows. The verb layer
// maps either case onto refusal.unknown-clone (SURFACE V34); the caller
// already knows which of the two it asked for, so one sentinel serves both
// without this package importing clasterr to distinguish them.
var ErrUnknownClone = errors.New("registry: no clone matches the given locator")

// AmbiguousLocatorError reports that a locator legally matched more than
// one candidate — M16's refuse-over-guess: never pick one, name every
// match instead. Candidates are exact-match values (clone labels), sorted
// for a deterministic message.
type AmbiguousLocatorError struct {
	Locator    string
	Candidates []string
}

func (e *AmbiguousLocatorError) Error() string {
	return fmt.Sprintf("registry: %q is ambiguous — matches %s", e.Locator, strings.Join(e.Candidates, ", "))
}

// ProjectView is one project as loaded into a View: project.json's content,
// plus every clone registered for it across every machine that has one
// (journal.ListClones, M5).
type ProjectView struct {
	Project journal.Project
	Clones  []journal.CloneEntry
}

// View is a loaded snapshot of the registry: every project the journal
// knows about, together with each one's clones (M15). Load reads the
// journal exactly once; nothing on View re-reads it afterward, so a caller
// holding a View is looking at one consistent snapshot even if the journal
// changes under it mid-use.
type View struct {
	Projects []ProjectView
}

// Load reads every project and its clones from root into a View.
// Diagnostics accumulated while enumerating (a malformed project.json or
// clones file, M11-style identity mismatches — see journal.ListProjects/
// ListClones) are returned alongside rather than failing the load: the
// same tolerant-read posture journal's own Walk applies to sessions,
// carried through here to the registry side of the tree.
func Load(root string) (View, []journal.Diagnostic, error) {
	projects, diags, err := journal.ListProjects(root)
	if err != nil {
		return View{}, nil, err
	}

	var view View
	for _, p := range projects {
		clones, cdiags, err := journal.ListClones(root, p.Slug)
		if err != nil {
			return View{}, nil, err
		}
		diags = append(diags, cdiags...)
		view.Projects = append(view.Projects, ProjectView{Project: p, Clones: clones})
	}
	return view, diags, nil
}

// ResolveProject resolves a project locator against view: a ULID-shaped
// locator (IsIdentityShaped) dispatches by id, anything else by slug
// (M16's shape rule). Both are exact-match only — no prefix matching, no
// walk-ups (wip's resolve.go posture, kept) — and neither can legally
// match more than once (a project's id and its directory-derived slug are
// each unique by construction, journal.ListProjects' own identity check),
// so there is no ambiguous case to report here, unlike ResolveClone.
func ResolveProject(view View, locator string) (ProjectView, error) {
	if IsIdentityShaped(locator) {
		for _, p := range view.Projects {
			if p.Project.ID == locator {
				return p, nil
			}
		}
		return ProjectView{}, ErrUnknownProject
	}
	for _, p := range view.Projects {
		if p.Project.Slug == locator {
			return p, nil
		}
	}
	return ProjectView{}, ErrUnknownProject
}

// ResolveClone resolves a clone locator among project's own registered
// clones (across every machine that has one, M5): a ULID-shaped locator
// dispatches by id, anything else by label (M16). Both are exact-match
// only — no prefix matching, no walk-ups. A label collision across two
// machines' clones of the SAME project is legal (each machine registers
// independently and cannot see another machine's labels at registration
// time) but unresolvable by label alone: ambiguous, never guessed at
// (M16, refuse over guess).
func ResolveClone(project ProjectView, locator string) (journal.CloneEntry, error) {
	if IsIdentityShaped(locator) {
		for _, c := range project.Clones {
			if c.ID == locator {
				return c, nil
			}
		}
		return journal.CloneEntry{}, ErrUnknownClone
	}

	var candidates []journal.CloneEntry
	for _, c := range project.Clones {
		if c.Label == locator {
			candidates = append(candidates, c)
		}
	}
	switch len(candidates) {
	case 0:
		return journal.CloneEntry{}, ErrUnknownClone
	case 1:
		return candidates[0], nil
	default:
		labels := make([]string, len(candidates))
		for i, c := range candidates {
			labels[i] = fmt.Sprintf("%s (%s)", c.ID, c.Machine)
		}
		return journal.CloneEntry{}, &AmbiguousLocatorError{Locator: locator, Candidates: labels}
	}
}

// CurrentClone is what ResolveCurrentClone resolves: the project and clone
// dir belongs to, plus the worktree fact M17 records — "" for dir's main
// worktree, or the linked worktree's own name (GitDir's basename)
// otherwise. clast never registers a worktree as a row of its own (M17/S7,
// diverging from wip's Worktree tier): this name is carried only as this
// call's own fact, never persisted.
type CurrentClone struct {
	Project  journal.Project
	Clone    journal.Clone
	Worktree string
}

// ResolveCurrentClone resolves the clone at dir's git-common-dir: it
// computes CommonDir (the pure core's worktree-invariant key, M15), then
// looks it up among THIS machine's own registered clones only — a clone is
// host-scoped (M15/S6): another machine's clones.<machine>.json rows never
// match a local path, even when their recorded common-dir happens to
// coincide (e.g. two machines that both keep a checkout at the same
// absolute path). ErrUnknownClone means the common-dir is not registered
// under this machine's name at all.
//
// The worktree fact follows M17: dir's GitDir is compared against the same
// CommonDir — equal means dir is inside the clone's main worktree (name
// ""); different means a linked worktree, named by GitDir's own basename.
//
// Ported from wip's adopt.go ResolveCurrentClone + CurrentWorktreeName,
// collapsed into one call (clast has no store transaction or opportunistic
// remote adoption to interleave between them, see this file's top comment).
func ResolveCurrentClone(ctx context.Context, root, dir string) (CurrentClone, error) {
	commonDir, err := CommonDir(ctx, dir)
	if err != nil {
		return CurrentClone{}, fmt.Errorf("registry: resolving common-dir for %s: %w", dir, err)
	}

	machine, err := journal.Hostname()
	if err != nil {
		return CurrentClone{}, fmt.Errorf("registry: resolving local machine name: %w", err)
	}

	view, _, err := Load(root)
	if err != nil {
		return CurrentClone{}, err
	}

	for _, p := range view.Projects {
		for _, c := range p.Clones {
			if c.Machine != machine || c.GitCommonDir != commonDir {
				continue
			}

			gitDir, err := GitDir(ctx, dir)
			if err != nil {
				return CurrentClone{}, fmt.Errorf("registry: resolving git-dir for %s: %w", dir, err)
			}
			worktree := ""
			if gitDir != commonDir {
				worktree = filepath.Base(gitDir)
			}
			return CurrentClone{Project: p.Project, Clone: c.Clone, Worktree: worktree}, nil
		}
	}
	return CurrentClone{}, ErrUnknownClone
}
