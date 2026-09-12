package registry

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CommonDir runs `git rev-parse --git-common-dir` in dir, absolute-path
// form, and returns what it reports — a Clone's natural key (M15). It
// resolves identically from a subdirectory and from a linked worktree,
// which is the load-bearing property the whole registry model rests on.
//
// Ported from wip's internal/tiers/git.go gitCommonDir.
func CommonDir(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// GitDir runs `git rev-parse --git-dir` in dir, absolute-path form. It
// differs from CommonDir exactly when dir is a linked worktree: GitDir
// then names that worktree's own `<main>/.git/worktrees/<name>` directory,
// while CommonDir still names the main clone's `.git`. Comparing the two
// is how a caller tells "this is the main worktree (or an ordinary clone
// with none)" from "this is a linked worktree, and its name is GitDir's
// own basename" — the name M17 records as a session's worktree fact.
// Deriving that comparison, and birthing whatever row it implies, is a
// later step's job; this function only supplies the raw fact.
//
// Ported from wip's internal/tiers/git.go gitDir.
func GitDir(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir")
}

// Branch runs `git branch --show-current` in dir and returns the current
// branch name — "" when HEAD is detached, the same "no name to report"
// sentinel M17 already gives the main worktree (empty, not the literal
// string a linked worktree would otherwise be confused for).
//
// This deliberately does not use `git rev-parse --abbrev-ref HEAD`, which
// reports the literal string "HEAD" for a detached checkout: a caller
// would then have to know to treat that one string as a magic sentinel
// rather than a real branch name (git refuses to let a real branch be
// named "HEAD", but the special-case string is still ours to invent or
// avoid). `--show-current` reports "" instead, so the empty string is
// this package's one sentinel for "no fact to report" across both Branch
// and CurrentClone.Worktree. It also resolves on a brand-new repo with no
// commits yet (an "unborn HEAD", still on its default branch) — rev-parse
// errors there instead, which would make Branch fail on a directory init
// itself succeeds against.
//
// No wip prior art exists for this: wip's model keys only on Repo/Clone/
// Worktree (git-common-dir/git-dir) and never asked what the current
// branch is (see internal/registry/resolve.go's top comment for the
// other wip-vs-clast divergences this package already carries).
func Branch(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "branch", "--show-current")
}

// Remotes lists dir's configured remotes by name, each mapped to its
// fetch URL (`git remote get-url <name>`) — the raw input a later
// adoption/identity step feeds through NormalizeRemote.
//
// Ported from wip's internal/tiers/git.go gitRemotes.
func Remotes(ctx context.Context, dir string) (map[string]string, error) {
	out, err := runGit(ctx, dir, "remote")
	if err != nil {
		return nil, err
	}
	remotes := map[string]string{}
	if out == "" {
		return remotes, nil
	}
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		u, err := runGit(ctx, dir, "remote", "get-url", name)
		if err != nil {
			return nil, err
		}
		remotes[name] = u
	}
	return remotes, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("registry: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
