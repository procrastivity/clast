// Package initverb implements the `clast init` verb (SURFACE V26):
// init-or-register, one verb, top level. Run inside a clone, it resolves
// the normalized identity remote (M15): a known project registers this
// clone into clones.<machine>.json; an unknown one first creates
// projects/<slug>/project.json. Re-running init in an already-registered
// clone reports `current` and writes nothing.
//
// Business logic (Run) is kept separate from Cobra wiring (Command) so it
// can be exercised directly, the same split wip's internal/tiers (logic)
// and internal/verbs/init (thin CLI) drew — but folded into one package
// here since, unlike doctor/install's internal/checks and internal/manifest,
// nothing else in clast shares this verb's logic.
//
// Ported-and-diverged from wip's internal/tiers/init.go — see this file's
// and Run's own comments for the specific divergences (no Worktree rows,
// current-not-refused re-run, the slug axis wip's Repo had no equivalent
// of).
package initverb

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
)

// Outcome is what Run did: created a new project (and registered this
// clone into it), registered this clone into an already-known project, or
// found this clone already registered (current — nothing written).
type Outcome string

const (
	// OutcomeCreated means no project matched this clone's identity remote
	// (or it is a remoteless, keyless clone); a new project was created
	// and this clone registered into it.
	OutcomeCreated Outcome = "created"
	// OutcomeRegistered means a project already matched this clone's
	// identity remote; this clone was registered into it.
	OutcomeRegistered Outcome = "registered"
	// OutcomeCurrent means this clone (by common-dir, on this machine)
	// was already registered; nothing was written.
	OutcomeCurrent Outcome = "current"
)

// Options carries init's three flag overrides.
type Options struct {
	// Slug overrides a newly-created project's slug (default: the owning
	// clone directory's basename). Ignored when this clone joins an
	// already-known project — there is no project to name.
	Slug string
	// Label overrides this clone's registered label (default: the M16
	// ladder).
	Label string
	// IdentityRemote names which configured remote provides a newly-
	// resolved project's identity (default: "origin").
	IdentityRemote string
}

// Result is what Run produced.
type Result struct {
	Project        journal.Project
	ProjectCreated bool
	Clone          journal.Clone
	Outcome        Outcome
	// Worktree is "" when dir is the clone's main worktree, or the linked
	// worktree's own name otherwise (M17) — carried for informational
	// (-v) use only. clast never births a worktree row for it (S7).
	Worktree string
}

// Run implements `clast init` against root's journal: resolve dir's
// git-common-dir (the clone's key, worktree-invariant, M15); if it is
// already registered under this machine's name, report OutcomeCurrent and
// write nothing. Otherwise resolve dir's identity remote, find or create
// the project it belongs to, resolve this clone's label through the M16
// ladder, and register it.
func Run(ctx context.Context, root, dir string, opts Options) (Result, error) {
	commonDir, err := registry.CommonDir(ctx, dir)
	if err != nil {
		return Result{}, notAGitRepo(dir, err)
	}
	gitDir, err := registry.GitDir(ctx, dir)
	if err != nil {
		return Result{}, notAGitRepo(dir, err)
	}
	worktree := ""
	if gitDir != commonDir {
		worktree = filepath.Base(gitDir)
	}

	machine, err := journal.Hostname()
	if err != nil {
		return Result{}, fmt.Errorf("initverb: resolving local machine name: %w", err)
	}

	// Loaded once, up front: the "already registered" check below and the
	// project-lookup-by-remote/slug logic further down both read this same
	// snapshot. registry.ResolveCurrentClone (the resolution layer's own
	// current-clone lookup) is not reused here because on a miss it only
	// returns ErrUnknownClone — this call needs the loaded View itself to
	// keep going, not just a yes/no.
	view, _, err := registry.Load(root)
	if err != nil {
		return Result{}, err
	}

	for _, p := range view.Projects {
		for _, c := range p.Clones {
			if c.Machine == machine && c.GitCommonDir == commonDir {
				return Result{Project: p.Project, Clone: c.Clone, Outcome: OutcomeCurrent, Worktree: worktree}, nil
			}
		}
	}

	// Not yet registered. The clone directory that names this registration
	// (for both a new project's default slug and this clone's default
	// label) is the OWNING clone's directory — commonDir's parent — not
	// dir itself: from inside a linked worktree those differ, and a
	// worktree never registers under its own name (S7/M17).
	cloneDir := filepath.Dir(commonDir)

	remoteURL, identityName, err := resolveIdentityRemote(ctx, dir, opts.IdentityRemote)
	if err != nil {
		return Result{}, err
	}

	var project journal.Project
	projectFound := false
	if remoteURL != "" {
		if p, ok := findProjectByRemote(view, remoteURL); ok {
			project, projectFound = p, true
		}
	}

	projectCreated := false
	src := registry.NewSource()

	if !projectFound {
		slug, err := resolveSlug(cloneDir, opts.Slug)
		if err != nil {
			return Result{}, err
		}
		if _, collides := findProjectBySlug(view, slug); collides {
			return Result{}, clasterr.New("validation.slug-collision",
				fmt.Sprintf("a project already exists at slug %q with a different remote; pass --slug <name> to register this clone under a different slug", slug))
		}

		project = journal.Project{
			ID:             src.Next(), // project id minted at creation
			Slug:           slug,
			Remote:         remoteURL,
			IdentityRemote: identityName,
		}
		if err := journal.WriteProject(root, project.Slug, project); err != nil {
			return Result{}, err
		}
		projectCreated = true
	}

	label, err := resolveLabel(cloneDir, opts.Label, clonesOfProject(view, project.ID))
	if err != nil {
		return Result{}, err
	}

	newClone := journal.Clone{ID: src.Next(), GitCommonDir: commonDir, Label: label} // clone id minted at registration

	cf, _, err := journal.ReadClones(root, project.Slug, machine)
	if err != nil {
		return Result{}, err
	}
	cf.Machine = machine
	cf.Clones = append(cf.Clones, newClone)
	if err := journal.WriteClones(root, project.Slug, cf); err != nil {
		return Result{}, err
	}

	outcome := OutcomeRegistered
	if projectCreated {
		outcome = OutcomeCreated
	}
	return Result{
		Project:        project,
		ProjectCreated: projectCreated,
		Clone:          newClone,
		Outcome:        outcome,
		Worktree:       worktree,
	}, nil
}

func notAGitRepo(dir string, err error) error {
	return clasterr.New("validation.not-a-git-repo",
		fmt.Sprintf("%s is not inside a git repository clast can read: %v", dir, err))
}

// resolveIdentityRemote implements M15's identity-remote rule, ported from
// wip's internal/tiers/init.go initNewClone: zero remotes at all is the
// unambiguous local-only case (decisions.md item 3) — no refusal, "" comes
// back regardless of identityFlag, and identityName is still recorded
// (matches wip's TestInit_LocalOnly: IdentityRemote is stamped even with no
// remote to match it against). Remotes exist but the named one (origin, or
// identityFlag) is absent: refuse. wip's two distinct refusals for that
// case are both kept — the default-missing spelling is wip's own verbatim
// (task instruction), "Repo" reworded to "project" for clast's vocabulary.
func resolveIdentityRemote(ctx context.Context, dir, identityFlag string) (remoteURL, identityName string, err error) {
	remotes, err := registry.Remotes(ctx, dir)
	if err != nil {
		return "", "", err
	}

	identityName = identityFlag
	if identityName == "" {
		identityName = "origin"
	}

	if len(remotes) == 0 {
		return "", identityName, nil
	}

	raw, present := remotes[identityName]
	if !present {
		if identityFlag != "" {
			return "", "", clasterr.New("validation.unresolvable-identity-remote",
				fmt.Sprintf("--identity-remote %s names no remote on this clone", identityFlag))
		}
		return "", "", clasterr.New("validation.no-identity-remote",
			"no origin remote and no --identity-remote given; pass --identity-remote <name> to choose which remote provides this project's identity")
	}

	normalized, err := registry.NormalizeRemote(raw)
	if err != nil {
		return "", "", clasterr.New("validation.unparseable-remote", err.Error())
	}
	return normalized, identityName, nil
}

// resolveSlug validates and defaults a new project's slug: cloneDir's own
// basename by default, refusing a ULID-shaped override the same way a
// label is refused (M16 shape rule, so a locator can always dispatch on
// shape alone).
func resolveSlug(cloneDir, slugFlag string) (string, error) {
	if slugFlag == "" {
		return filepath.Base(cloneDir), nil
	}
	if registry.IsIdentityShaped(slugFlag) {
		return "", clasterr.New("validation.slug-ulid-shaped",
			fmt.Sprintf("%q has the shape of a ULID; a slug may never take that shape, so addressing can dispatch on shape alone", slugFlag))
	}
	return slugFlag, nil
}

// resolveLabel validates and defaults this clone's label against existing
// (the M16 ladder, run silently on the default path; an explicit --label
// collision is a hard refusal, never auto-resolved, mirroring wip's
// relabel.SetLabel).
func resolveLabel(cloneDir, labelFlag string, existing []journal.CloneEntry) (string, error) {
	taken := map[string]bool{}
	for _, c := range existing {
		taken[c.Label] = true
	}

	if labelFlag != "" {
		if registry.IsIdentityShaped(labelFlag) {
			return "", clasterr.New("validation.label-ulid-shaped",
				fmt.Sprintf("%q has the shape of a ULID; a label may never take that shape, so addressing can dispatch on shape alone", labelFlag))
		}
		if taken[labelFlag] {
			msg := fmt.Sprintf("label %q is already used by another clone of this project", labelFlag)
			if suggestion, ok := registry.SuggestLabel(cloneDir, taken); ok {
				msg += fmt.Sprintf("; try %q", suggestion)
			}
			return "", clasterr.New("validation.label-collision", msg)
		}
		return labelFlag, nil
	}

	label := registry.DefaultLabel(cloneDir)
	if !taken[label] {
		return label, nil
	}
	suggestion, ok := registry.SuggestLabel(cloneDir, taken)
	if !ok {
		return "", clasterr.New("validation.label-collision",
			fmt.Sprintf("clone label %q is already used in this project, and both fallback names are also taken", label))
	}
	return suggestion, nil
}

func findProjectByRemote(view registry.View, remote string) (journal.Project, bool) {
	for _, p := range view.Projects {
		if p.Project.Remote == remote {
			return p.Project, true
		}
	}
	return journal.Project{}, false
}

func findProjectBySlug(view registry.View, slug string) (journal.Project, bool) {
	for _, p := range view.Projects {
		if p.Project.Slug == slug {
			return p.Project, true
		}
	}
	return journal.Project{}, false
}

// clonesOfProject returns projectID's own clones as loaded in view. A
// brand-new project (not yet present in view, since view was loaded before
// this Run wrote it) has none, which is exactly right — a fresh project's
// label ladder starts from an empty taken set.
func clonesOfProject(view registry.View, projectID string) []journal.CloneEntry {
	for _, p := range view.Projects {
		if p.Project.ID == projectID {
			return p.Clones
		}
	}
	return nil
}
