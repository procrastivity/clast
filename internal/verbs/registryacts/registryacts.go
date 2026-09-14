// Package registryacts contains the three deliberately human-only tier-2
// registry mutations. They are top-level commands and are never projected
// into a harness.
package registryacts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
	"github.com/procrastivity/clast/internal/surface"
)

// LabelCommand constructs the top-level `clast label` act.
func LabelCommand(s *iostreams.Streams) *cobra.Command {
	return command(s, "label <new-label>", "rename the registered clone label (human-only)", cobra.ExactArgs(1), label)
}

// AdoptCommand constructs the top-level `clast adopt` act.
func AdoptCommand(s *iostreams.Streams) *cobra.Command {
	return command(s, "adopt", "adopt this project's recorded identity remote (human-only)", cobra.NoArgs, adopt)
}

// RelinkCommand constructs the top-level `clast relink` act.
func RelinkCommand(s *iostreams.Streams) *cobra.Command {
	return command(s, "relink <clone>", "relink a moved registered clone (human-only)", cobra.ExactArgs(1), relink)
}

func label(ctx context.Context, root, dir string, args []string) (string, error) {
	if registry.IsIdentityShaped(args[0]) {
		return "", clasterr.New("validation.label-ulid-shaped", fmt.Sprintf("%q has the shape of a ULID; a label may never take that shape", args[0]))
	}
	view, _, err := registry.Load(root)
	if err != nil {
		return "", err
	}
	machine, err := localMachine()
	if err != nil {
		return "", err
	}
	common, err := registry.CommonDir(ctx, dir)
	if err != nil {
		return "", clasterr.New("validation.not-a-git-repo", err.Error())
	}
	candidates := currentCloneCandidates(view, machine, common)
	if len(candidates) > 1 {
		return "", ambiguousClone("this directory", candidates)
	}
	if len(candidates) == 0 {
		return "", clasterr.New("refusal.unknown-clone", "refused — this directory is not a registered clast clone; run `clast init` here")
	}
	candidate := candidates[0]
	if candidate.Clone.Label == args[0] {
		return candidate.Clone.Label, nil
	}
	taken := make(map[string]bool)
	for _, p := range view.Projects {
		if p.Project.ID != candidate.Project.ID {
			continue
		}
		for _, other := range p.Clones {
			if other.ID != candidate.Clone.ID || other.Machine != candidate.Clone.Machine {
				taken[other.Label] = true
			}
		}
	}
	if taken[args[0]] {
		message := fmt.Sprintf("label %q is already used by another clone of this project", args[0])
		if suggestion, ok := registry.SuggestLabel(filepath.Dir(common), taken); ok {
			message += fmt.Sprintf("; try %q", suggestion)
		}
		return "", clasterr.New("validation.label-collision", message)
	}
	cf, _, err := journal.ReadClones(root, candidate.Project.Slug, machine)
	if err != nil {
		return "", err
	}
	for i := range cf.Clones {
		if cf.Clones[i].ID == candidate.Clone.ID {
			cf.Clones[i].Label = args[0]
		}
	}
	if err := journal.WriteClones(root, candidate.Project.Slug, cf); err != nil {
		return "", err
	}
	return args[0], nil
}

func adopt(ctx context.Context, root, dir string, _ []string) (string, error) {
	view, _, err := registry.Load(root)
	if err != nil {
		return "", err
	}
	machine, err := localMachine()
	if err != nil {
		return "", err
	}
	common, err := registry.CommonDir(ctx, dir)
	if err != nil {
		return "", clasterr.New("validation.not-a-git-repo", err.Error())
	}
	candidates := currentCloneCandidates(view, machine, common)
	if len(candidates) > 1 {
		return "", ambiguousClone("this directory", candidates)
	}
	if len(candidates) == 0 {
		return "", clasterr.New("refusal.unknown-clone", "refused — this directory is not a registered clast clone; run `clast init` here")
	}
	project := candidates[0].Project
	if project.Remote != "" {
		return project.Remote, nil
	}
	remotes, err := registry.Remotes(ctx, dir)
	if err != nil {
		return "", err
	}
	name := project.IdentityRemote
	if name == "" {
		name = "origin"
	}
	raw, ok := remotes[name]
	if !ok {
		return "", clasterr.New("validation.no-identity-remote", fmt.Sprintf("no remote named %q is available for adoption", name))
	}
	remote, err := registry.NormalizeRemote(raw)
	if err != nil {
		return "", clasterr.New("validation.unparseable-remote", err.Error())
	}
	for _, p := range view.Projects {
		if p.Project.Remote == remote && p.Project.ID != project.ID {
			return "", clasterr.New("refusal.repo-key-conflict", fmt.Sprintf("refused — remote %q is already claimed by project %s", remote, p.Project.ID))
		}
	}
	project.Remote = remote
	project.IdentityRemote = name
	if err := journal.WriteProject(root, project.Slug, project); err != nil {
		return "", err
	}
	return remote, nil
}

func relink(ctx context.Context, root, dir string, args []string) (string, error) {
	view, _, err := registry.Load(root)
	if err != nil {
		return "", err
	}
	machine, err := localMachine()
	if err != nil {
		return "", err
	}
	common, err := registry.CommonDir(ctx, dir)
	if err != nil {
		return "", clasterr.New("validation.not-a-git-repo", err.Error())
	}
	var candidates []locatedClone
	if registry.IsIdentityShaped(args[0]) {
		for _, p := range view.Projects {
			for _, c := range p.Clones {
				if c.ID == args[0] && c.Machine == machine {
					candidates = append(candidates, locatedClone{Project: p.Project, Clone: c})
				}
			}
		}
	} else {
		remotes, e := registry.Remotes(ctx, dir)
		if e != nil {
			return "", e
		}
		normalized := make(map[string]bool)
		for _, raw := range remotes {
			r, e := registry.NormalizeRemote(raw)
			if e == nil {
				normalized[r] = true
			}
		}
		remoteKeys := make([]string, 0, len(normalized))
		for remote := range normalized {
			remoteKeys = append(remoteKeys, remote)
		}
		sort.Strings(remoteKeys)
		for _, r := range remoteKeys {
			for _, p := range view.Projects {
				if p.Project.Remote == r {
					for _, c := range p.Clones {
						if c.Machine == machine && c.Label == args[0] {
							candidates = append(candidates, locatedClone{Project: p.Project, Clone: c})
						}
					}
				}
			}
		}
	}
	if len(candidates) == 0 {
		return "", clasterr.New("validation.unknown-locator", fmt.Sprintf("no registered clone matches %q", args[0]))
	}
	if len(candidates) > 1 {
		return "", ambiguousClone(args[0], candidates)
	}
	target := candidates[0]
	for _, p := range view.Projects {
		for _, c := range p.Clones {
			if c.Machine == machine && c.ID != target.Clone.ID && c.GitCommonDir == common {
				return "", clasterr.New("refusal.clone-key-conflict", fmt.Sprintf("refused — git common-dir %q is already registered to clone %s", common, c.ID))
			}
		}
	}
	cf, _, err := journal.ReadClones(root, target.Project.Slug, machine)
	if err != nil {
		return "", err
	}
	for i := range cf.Clones {
		if cf.Clones[i].ID == target.Clone.ID {
			cf.Clones[i].GitCommonDir = common
		}
	}
	if err := journal.WriteClones(root, target.Project.Slug, cf); err != nil {
		return "", err
	}
	return target.Clone.ID, nil
}

type locatedClone struct {
	Project journal.Project
	Clone   journal.CloneEntry
}

func localMachine() (string, error) {
	machine, err := journal.Hostname()
	if err != nil {
		return "", fmt.Errorf("registryacts: resolving local machine name: %w", err)
	}
	return machine, nil
}

func currentCloneCandidates(view registry.View, machine, common string) []locatedClone {
	var candidates []locatedClone
	for _, p := range view.Projects {
		for _, c := range p.Clones {
			if c.Machine == machine && c.GitCommonDir == common {
				candidates = append(candidates, locatedClone{Project: p.Project, Clone: c})
			}
		}
	}
	return candidates
}

func ambiguousClone(locator string, candidates []locatedClone) error {
	ids := make([]string, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.Clone.ID
	}
	sort.Strings(ids)
	return clasterr.New("refusal.ambiguous-locator", fmt.Sprintf("refused — %s matches multiple registered clones: %s", locator, strings.Join(ids, ", ")))
}

func command(s *iostreams.Streams, use, short string, args cobra.PositionalArgs, run func(context.Context, string, string, []string) (string, error)) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short, Args: args, RunE: func(c *cobra.Command, a []string) error {
		cfg, e := config.Load()
		if e != nil {
			return e
		}
		root, e := journal.Root(cfg)
		if e != nil {
			return e
		}
		dir, e := os.Getwd()
		if e != nil {
			return e
		}
		out, e := run(c.Context(), root, dir, a)
		if e != nil {
			return e
		}
		if cliflags.FromContext(c.Context()).JSON {
			b, marshalErr := json.Marshal(struct {
				Result string `json:"result"`
			}{Result: out})
			if marshalErr != nil {
				return marshalErr
			}
			_, e = fmt.Fprintln(s.Out, string(b))
			return e
		}
		_, e = fmt.Fprintln(s.Out, out)
		return e
	}}
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}
