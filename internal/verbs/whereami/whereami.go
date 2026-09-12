// Package whereami implements `clast plumbing whereami` (SURFACE V22): the
// debugging face of registry.ResolveCurrentClone. Run inside a registered
// clone, it reports the project, clone, worktree (M17), branch, and
// machine the cwd resolves to; run outside one, it refuses.
//
// Business logic (Run) is kept separate from Cobra wiring (command.go),
// the same split internal/verbs/initverb draws.
package whereami

import (
	"context"
	"errors"
	"fmt"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

// Result is what Run resolved for the cwd.
type Result struct {
	Project journal.Project
	Clone   journal.Clone
	// Worktree is "" for the clone's main worktree, or the linked
	// worktree's own name otherwise (M17).
	Worktree string
	// Branch is the checked-out branch name, or "" for a detached HEAD
	// (registry.Branch's own sentinel, chosen deliberately — see
	// registry/git.go).
	Branch  string
	Machine string
}

// Run resolves dir against root's journal via registry.ResolveCurrentClone,
// then adds the two facts that call does not itself report: the checked-out
// branch (registry.Branch) and this machine's own name (journal.Hostname,
// already resolved once inside ResolveCurrentClone, but not returned —
// cheap enough to resolve again rather than widen that call's return
// shape for one caller).
//
// registry.ErrUnknownClone — dir's common-dir does not match any clone
// registered under this machine's name — maps to refusal.unknown-clone
// (SURFACE V22/V34), naming `clast init` as the fix.
func Run(ctx context.Context, root, dir string) (Result, error) {
	// Pre-flight the one git failure worth a structured answer: a dir that
	// is not inside a git repository at all can never resolve, and the
	// debugging verb is exactly where someone asks from such a place.
	// Same code and shape as init's own check (validation.not-a-git-repo).
	if _, err := registry.CommonDir(ctx, dir); err != nil {
		return Result{}, clasterr.New("validation.not-a-git-repo",
			fmt.Sprintf("%s is not inside a git repository clast can read: %v", dir, err))
	}

	current, err := registry.ResolveCurrentClone(ctx, root, dir)
	if err != nil {
		if errors.Is(err, registry.ErrUnknownClone) {
			return Result{}, unknownClone(dir)
		}
		return Result{}, err
	}

	branch, err := registry.Branch(ctx, dir)
	if err != nil {
		return Result{}, err
	}

	machine, err := journal.Hostname()
	if err != nil {
		return Result{}, fmt.Errorf("whereami: resolving local machine name: %w", err)
	}

	return Result{
		Project:  current.Project,
		Clone:    current.Clone,
		Worktree: current.Worktree,
		Branch:   branch,
		Machine:  machine,
	}, nil
}

// unknownClone is whereami's refusal for a cwd that resolves to no
// registered clone: refusal.unknown-clone (V22/V34), naming `clast init`
// as the fix — adapted from wip's own unknown-clone wording
// (internal/tiers/errors.go), "wip" reworded to "clast" and the concept
// narrowed to what registry.ResolveCurrentClone itself reports (no
// --repo/--project escape hatch: whereami takes no locator to override
// with).
func unknownClone(dir string) error {
	return clasterr.New("refusal.unknown-clone",
		fmt.Sprintf("refused — %s is not a registered clast clone; run `clast init` here to register it", dir))
}
