// Package registry is clast's pure, dependency-free identity core: remote
// URL normalization, the clone-label ladder, git common-dir/worktree
// helpers, and the monotonic ULID generator that mints every Project and
// Clone id (MODEL.md M15-M17). It does no file I/O of its own — no
// projects/*.json reads or writes, that stays internal/journal's job — and
// no resolution or verb logic (locating "the current clone" from a
// directory is a later step, built on top of this package).
//
// The whole package is copy-and-diverge from the sibling wip repo's
// internal/tiers and internal/store/ulid.go (see wip's
// docs/tiers/decisions.md and docs/tiers/worked-examples.md for the
// design history this code is ported from); nothing here imports or
// links wip.
//
// remote.go normalizes a remote URL into the natural key M15 keys a
// Project by (NormalizeRemote). label.go is the label ladder M16 fixes: a
// Clone's default label and the two-tier collision suggestion, refusing
// rather than auto-suffixing (DefaultLabel, SuggestLabel). git.go shells
// out to git for the two raw facts M15/M17 need — a Clone's common-dir
// (its key) and the git-dir a later step compares against it to name a
// linked worktree (CommonDir, GitDir), plus a clone's configured remotes
// (Remotes). ulid.go is the monotonic ULID generator (Source, NewSource)
// and the shape test (IsIdentityShaped) label validation and locator
// dispatch read against.
package registry
