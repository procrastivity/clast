---
step: 18
title: docs-and-examples
depends_on: [01, 06, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 17]
size: medium
references:
  - docs/overview.md
  - docs/skill-prompts.md#cron-sample
  - docs/repo-bootstrap.md#repo-layout
  - docs/cli-contract.md
---

# Step 18: docs and examples

## Context

The repo has been functional since step 13 and shippable since step 17.
What is missing for a v1.0 release is the polish layer: the
`examples/` tree (cron snippets, a sample config, a narrative
`/day-wakeup` walkthrough), the `CHANGELOG.md` (still carrying only an
empty `[Unreleased]` stub from step 01), and a once-over of `README.md`
to make sure the install / usage / development sections reflect the
shipped reality and not a half-built draft.

What is already on `main` that this step finishes:

- `examples/cron/`, `examples/config/`, `examples/workflows/` —
  scaffold directories created in step 01, each holding only a
  `.gitkeep`. The `files` array in `package.json` (step 16) already
  includes `examples/`; the Nix package (step 15) and `install.sh`
  (step 14) both copy `examples/` into the install layout. So as soon
  as real files land in `examples/`, they ship through every
  distribution channel without further plumbing.
- `CHANGELOG.md` — header + `[Unreleased]` placeholder. No entries.
  `cliff.toml` is wired up but no release has been cut, so
  `git cliff` has nothing to do yet; the bootstrap entry for v1.0.0
  is the human curation step here.
- `README.md` — already covers snapshot/read/curate/breadcrumb/audit
  CLI usage (steps 06–10), all three install channels (manual / Nix /
  npm — steps 14/15/16), the plugin's two skills (steps 12/13), the
  dev shell, and a brief CI/Release section (step 17). Mostly
  correct; this step's audit catches stale claims (e.g., "marketplace
  install flow is wired up in a future step" — still true, but the
  phrasing should clarify it's deliberate, not pending) and verifies
  every doc link still resolves.
- `docs/` — `overview.md`, `cli-contract.md`, `skill-prompts.md`,
  `repo-bootstrap.md`, `build-steps.md`, and `jsonl-format.md` are
  the durable reference docs. `releasing.md` is referenced in
  `repo-bootstrap.md`'s layout tree but has not yet been written;
  step 18 either writes it (recommended, since step 19's tag flow
  needs a procedure to follow) or explicitly defers it and updates
  the layout doc to say so. Recommend writing — it's three pages of
  runbook, all derivable from the workflows step 17 just landed.

This is **the last step before v1.0.0 is tagged**. Step 19 is the
release flow itself: bump the version literal in `package.json` and
`flake.nix` (the lockstep invariant from step 16 enforces both move
together), tag, push, watch the release workflow ship the npm + GH
release artifacts. Nothing in step 19 is documentation; everything
documentation-shaped lands here.

**Scope discipline.** This step does NOT add new CLI behavior, does
NOT change subcommand contracts, and does NOT introduce a TOML config
parser even though `examples/config/config.toml.sample` exists by
design. The TOML loader is a v1.x feature; the sample file shipped
here documents the **planned** config surface (and the v1 env-var
equivalents that already work). See `## Out of scope`.

## Goal

Land the `examples/` content (`cron/crontab.sample`,
`cron/systemd-timer.sample` as `.service` + `.timer` pair,
`config/config.toml.sample`, `workflows/morning-briefing.md`), write
`docs/releasing.md` describing the tag → release-workflow → npm + GH
release procedure, curate `CHANGELOG.md`'s `[Unreleased]` section into
a real v1.0.0 entry summarizing what steps 01–17 built, and do a
final README pass that fixes any stale claims and ensures every
relative link resolves. Replace every `examples/*/.gitkeep` with a
real file (or remove the `.gitkeep` once a sibling file exists, since
the directory is no longer empty). Verify with `make lint`, `make
test`, and a `make npm-pack-check` that confirms the new
`examples/**` paths are present in the tarball.

## References

Read before starting:

- `docs/overview.md` — the canonical "what clast is" doc. The README
  must agree with this; this step does NOT re-edit `overview.md`.
- `docs/cli-contract.md` — the CLI contract. Every example in the
  README and in `examples/workflows/morning-briefing.md` must use
  flags and output shapes that match this doc.
- `docs/skill-prompts.md#cron-sample` — **the explicit recipe for
  `examples/cron/crontab.sample`.** Copy the recipe; do not invent
  one.
- `docs/skill-prompts.md` (full) — the contract for `/day-wakeup` and
  `/wakeup`. `examples/workflows/morning-briefing.md` is a narrative
  walkthrough of a `/day-wakeup` session; it must reflect the
  documented behavior (turn shape, AskUserQuestion accept/edit/skip
  loop, the entry-promotion path, etc.).
- `docs/repo-bootstrap.md#repo-layout` — confirms the
  `examples/cron/`, `examples/config/`, `examples/workflows/`
  directory layout is the planned shape. The `releasing.md` file is
  also called out in the layout tree; this step writes it.
- `docs/repo-bootstrap.md#cliff.toml` — `git-cliff` is the changelog
  tool. For v1.0.0 the entries are hand-curated (no prior release to
  diff from); step 19 forward will use `git cliff` against the
  v1.0.0 tag.
- `.github/workflows/release.yml` — the release procedure
  `docs/releasing.md` documents. Read fully before writing the
  runbook so you describe what the workflow actually does, not what
  it ought to do.
- `package.json`, `flake.nix` — both at `version = "0.1.0"`. The
  `[1.0.0]` changelog entry below is the marker for the public release that
  step 19 tags after bumping those files.
- Existing examples patterns in adjacent projects (skim only) —
  `xcind` and `solofiber` ship `examples/cron/` and
  `examples/workflows/`; their shape is the de facto reference for
  tone and depth.

## Tasks

1. **Write `examples/cron/crontab.sample`.** Single file, the recipe
   from `docs/skill-prompts.md#cron-sample` (the hourly invocation),
   plus 2–4 commented alternatives (every-15-minutes for active
   users, once-daily for hands-off users, a SessionStart-only "no
   cron needed" reminder). Top of file: 3 lines explaining what the
   file is and how to install (`crontab -l | { cat; cat
   examples/cron/crontab.sample; } | crontab -`). Bottom: 1 line
   noting the hook from step 11 covers most of this use case
   already, and cron is only useful when you want capture without
   ever opening Claude.

2. **Write `examples/cron/systemd-timer.sample`.** Strictly speaking
   this is two files; ship them as one combined sample file with
   both halves clearly labeled, or as two files
   (`clast-snapshot.service` + `clast-snapshot.timer`) next to each
   other. **Preferred: two files** — that matches how systemd reads
   units and lets a user copy them directly into
   `~/.config/systemd/user/`. Both with leading comments explaining
   install (`systemctl --user enable --now clast-snapshot.timer`).
   The `.service` runs `/usr/local/bin/clast snapshot` (parameterize
   if `install.sh` was given a different prefix; the user is
   expected to edit the path). The `.timer` fires hourly with
   `OnUnitActiveSec=1h`, `OnBootSec=5min`, `Persistent=true`.

3. **Write `examples/config/config.toml.sample`.** This documents
   the **planned** TOML surface (the v1.x feature) **and** the env
   var equivalent that works in v1.0 today. File shape:

   ```toml
   # examples/config/config.toml.sample
   #
   # NOTE: TOML config loading is planned for v1.x; v1.0 reads only
   # the equivalent environment variables. Set these in your shell
   # profile to get the same effect today.
   #
   # ~/.config/clast/config.toml

   [paths]
   # Override where clast reads JSONL transcripts from.
   # Env equivalent: CLAST_PROJECTS_DIR
   # projects_dir = "/Users/you/.claude/projects"

   # Override where clast writes the journal.
   # Env equivalent: CLAST_JOURNAL_DIR
   # journal_dir = "/Users/you/.claude/journal"

   [time]
   # The "day" cutoff used by `clast stats`, `clast sessions`, and
   # the /day-wakeup window. Sessions started before this time on
   # date D count as belonging to date D-1.
   # Env equivalent: CLAST_DAY_CUTOFF
   # day_cutoff = "04:00"

   [logging]
   # Silence info logs (matches the global --quiet flag).
   # Env equivalent: CLAST_QUIET=1
   # quiet = false
   ```

   The file is intentionally commented out everywhere — uncommenting
   anything is a no-op in v1.0 (the loader does not exist yet), so
   the file's job is **documentation**. Mention this explicitly in
   the leading comment block.

4. **Write `examples/workflows/morning-briefing.md`.** Narrative
   walkthrough of a single `/day-wakeup` invocation: a short
   "yesterday" scenario (e.g., the user worked on two projects, one
   feature merged, one mid-flight), then a step-by-step transcript
   showing what `/day-wakeup` does (lists candidates, drafts
   entries, presents `AskUserQuestion` choices, writes accepted
   entries, marks skipped ones for tomorrow). 60–150 lines is the
   target — enough to feel realistic, short enough to read in two
   minutes. Use realistic-but-fake project slugs (`xesapps`,
   `notes`, `infra-tools`). End with a 3-line "What this changes on
   disk" summary: which `entries/*.md` files were written, what was
   appended to manifest, etc. **Cross-check every transcript line
   against `docs/skill-prompts.md#skill-1-day-wakeup` and
   `docs/cli-contract.md`** — do not invent commands or flags.

5. **Write `docs/releasing.md`.** Runbook for cutting a release.
   Sections:
   - **Pre-release checklist**: tests green on `main`, version bumped
     in both `package.json` and `flake.nix` (lockstep — the
     `check-version-sync` script makes the failure mode loud if
     missed), `CHANGELOG.md` updated, no `[Unreleased]` items left
     un-curated.
   - **Cut the release**: `git tag -a v$(jq -r .version
     package.json) -m "v$(...)"`, `git push origin v…`, watch the
     release workflow.
   - **What the workflow does**: 1:1 walkthrough of
     `.github/workflows/release.yml` (re-runs gates, verifies tag
     matches package.json version, builds Nix, packs npm tarball,
     publishes with provenance, creates GH release).
   - **If something fails**: how to recover from a failed publish
     (delete the tag, fix, re-tag) and a failed GH release (manual
     `gh release create` against the existing tag).
   - **NPM_TOKEN setup**: where to mint the token (npmjs.com →
     Access Tokens → Automation), what scope (publish for
     `@procrastivity/clast`), how to install (repo Settings →
     Secrets and variables → Actions → `NPM_TOKEN`).
   - **Post-release**: bump `package.json` to the next `-pre`
     version (e.g., `0.1.1-pre`) and `flake.nix` to match, commit
     to `main`. Optional.
   2–4 pages. Cross-link from `repo-bootstrap.md#ci`.

6. **Curate `CHANGELOG.md`'s `[1.0.0]` entry.** Replace
   `## [Unreleased]` with a `## [1.0.0]` heading (date undecided —
   leave as `## [1.0.0] - YYYY-MM-DD` placeholder for step 19 to
   stamp), then add a real `### Added` section enumerating what
   shipped (one bullet per major capability — not per step). Target
   shape:

   ```markdown
   ## [Unreleased]

   ## [1.0.0] - YYYY-MM-DD

   First public release.

   ### Added
   - `clast` CLI: snapshot, sessions, projects, show, entries,
     breadcrumb, stats, doctor, registry, whereami.
   - Manifest-backed JSONL → entry curation pipeline.
   - Claude Code plugin shipping `/day-wakeup` and `/wakeup` skills.
   - SessionStart hook for zero-effort capture.
   - Three install channels: manual `install.sh`, Nix flake
     (`packages.default` + `overlays.default`), npm
     (`@procrastivity/clast`).
   - `examples/cron/`, `examples/config/`, `examples/workflows/`
     reference material.
   - CI: lint + test + version sync + npm-pack-check + nix-smoke
     on every PR; tag-triggered release workflow with npm provenance
     + GitHub Release.
   ```

   Keep the `[Unreleased]` heading above `[1.0.0]` as the next-entry
   landing pad (empty body). Date placeholder is intentional;
   step 19 stamps it.

7. **README pass.** Read README.md top-to-bottom, then:
   - Verify every relative link (`./docs/...`) resolves to an
     existing file. (One-liner check: `grep -oE
     '\./docs/[a-zA-Z0-9_./#-]+' README.md | sed 's/#.*//' | sort
     -u | xargs -I{} test -f {} && echo ok`.)
   - Remove the "🚧 Pre-release. APIs may change before v1.0." line.
     v1.0 is what this release sequence delivers; the warning was
     for the pre-step-18 state. Replace with a one-sentence
     stability statement: "v1.0 — CLI contract is stable.
     Configuration TOML loading is planned for v1.x."
   - Tighten the "Install as a Claude Code plugin" section's
     "marketplace install flow is wired up in a future step" line
     — replace with the truthful statement: "The plugin can be
     installed from any local checkout, or via `npm install -g`
     (which puts `.claude-plugin/` under `npm root -g`); a
     centralized marketplace listing is a separate distribution
     channel deliberately not pursued for v1."
   - Add a one-line pointer to the new `examples/` directory in the
     "Documentation" section: `- [examples/](./examples/) — cron,
     systemd-timer, and workflow samples.`
   - Add a pointer to `docs/releasing.md` in the same section.
   - Verify every command in the README compiles against the
     current CLI contract (spot-check `clast sessions --since -7d`,
     `clast snapshot --dry-run --json`, `clast entries write
     --session ... --slug ... --body-stdin`, `clast breadcrumb
     --project ... '...'`).

8. **Drop the `.gitkeep` files** in `examples/cron/`,
   `examples/config/`, `examples/workflows/` once each directory
   has a real sibling file. Git no longer needs them to track the
   directory. Single `git rm` per file.

9. **Update `package.json`'s `files` array** ONLY if the new
   `examples/**` paths require it. The current array has
   `"examples/"`, which globs the whole directory; no edit needed.
   This task is a no-op pending verification — explicitly confirm
   `npm-pack-check` still passes with the new file set.

10. **Update `contrib/npm-pack-check.sh`'s `required` list** to
    include the new `examples/` paths. The current script
    enumerates `bin/`, `lib/`, `.claude-plugin/`, `hooks/`,
    `README.md`, `LICENSE`, `package.json` but does NOT enumerate
    `examples/*`. Add at minimum:
    - `examples/cron/crontab.sample`
    - `examples/cron/clast-snapshot.service`
    - `examples/cron/clast-snapshot.timer`
    - `examples/config/config.toml.sample`
    - `examples/workflows/morning-briefing.md`
    so a future contributor who accidentally removes one notices
    via CI rather than via a confused npm install user.

11. **Run the gates locally.** `make lint`, `make test`, `make
    check-version-sync`, `make npm-pack-check` (if npm is on PATH;
    no-op otherwise), and `make nix-smoke` (if nix is on PATH) all
    exit 0 after the edits.

12. **Verify `docs/repo-bootstrap.md`'s layout tree** still matches
    reality. Specifically, the `examples/` subtree and the
    `docs/releasing.md` entry should both now be backed by real
    files. If you discover a layout-doc claim that no longer
    matches, fix the layout doc (this is a documentation step;
    aligning the layout tree is in scope).

## Acceptance criteria

- `examples/cron/crontab.sample` exists, is non-empty, and matches
  the recipe in `docs/skill-prompts.md#cron-sample`.
- `examples/cron/clast-snapshot.service` and
  `examples/cron/clast-snapshot.timer` both exist, both pass
  `systemd-analyze verify` if systemd tools are on PATH (no-op
  otherwise — most macOS dev hosts won't have them), and together
  describe a working hourly snapshot timer.
- `examples/config/config.toml.sample` exists, parses as valid TOML
  via a `python3 -c 'import tomllib; tomllib.loads(open(...).read())'`
  smoke check, and includes a clear note that v1.0 reads env vars,
  not the TOML file.
- `examples/workflows/morning-briefing.md` exists, is 60–150 lines,
  and exclusively uses CLI flags and skill behaviors documented in
  `cli-contract.md` and `skill-prompts.md` (no invented surface).
- `docs/releasing.md` exists and walks through the tag-driven release
  flow that `.github/workflows/release.yml` implements.
- `CHANGELOG.md` has a curated `## [1.0.0] - YYYY-MM-DD` entry above
  the `## [Unreleased]` placeholder, with bullet-list `### Added`
  enumerating the v1.0 capability set.
- `README.md`:
  - No longer carries the "🚧 Pre-release. APIs may change before
    v1.0." banner.
  - Includes a stability statement saying v1.0 is the CLI contract
    floor and TOML config is v1.x.
  - "Documentation" section lists both `examples/` and
    `docs/releasing.md`.
  - Every relative link resolves to an existing file.
- `contrib/npm-pack-check.sh`'s `required` array enumerates the new
  `examples/**` files; `make npm-pack-check` passes.
- The `.gitkeep` placeholder files in `examples/cron/`,
  `examples/config/`, and `examples/workflows/` are removed.
- `make lint`, `make test`, `make check-version-sync`, and
  `make npm-pack-check` (when npm is available) all exit 0.
- `docs/repo-bootstrap.md`'s layout tree still accurately describes
  the on-disk state.

## Out of scope

- **Tagging v1.0.0.** Step 19. This step does not run
  `git tag`, does not bump versions, does not invoke the release
  workflow.
- **Implementing the TOML config loader.** Planned for v1.x. The
  `examples/config/config.toml.sample` documents the planned shape;
  the code path is not added.
- **Editing `docs/overview.md`, `docs/cli-contract.md`, or
  `docs/skill-prompts.md`.** These are the canonical reference docs;
  the README and examples conform to them, not vice versa. If the
  README disagrees, the README is wrong.
- **Adding new examples beyond the three categories planned.**
  `examples/macros/`, `examples/notion-export/`, etc. are all
  legitimate future additions but not in v1.0's scope.
- **Editing `cliff.toml` or generating a changelog from git
  history.** v1.0's changelog is hand-curated for clarity; v1.1+
  uses `git cliff` against the v1.0 tag.
- **Cleaning up `docs/build-steps.md`** to mark steps as done /
  rewriting the planned-steps table. The meta-doc captures the
  plan; the actual step files are the record of what shipped.
- **Marketplace listing for the Claude Code plugin.** Distinct
  distribution channel; npm + Nix + manual install cover v1.0.
- **Localizing the README** or adding non-English documentation.
- **Adding a `CONTRIBUTING.md` or `CODE_OF_CONDUCT.md`.** Worth
  doing eventually; out of scope for the v1.0 cut.
- **Restructuring `docs/`.** The current layout is the docs
  layout. Renames or splits belong to a separate step.
- **Adding screenshots or asciinema casts to the README.** Worth
  considering for v1.1; v1.0 ships text-only docs.

## Verification

```bash
# Lint
make lint

# Tests
make test

# Version sync (untouched, but still must hold)
make check-version-sync

# npm pack — now includes the new examples/** files
make npm-pack-check

# Nix smoke (skipped if nix unavailable)
make nix-smoke

# Confirm every relative doc link in the README resolves
grep -oE '\./(docs|examples|LICENSE|CHANGELOG\.md)[a-zA-Z0-9_./#-]*' README.md \
  | sed 's/#.*//' \
  | sort -u \
  | while read -r p ; do
      [ -e "$p" ] || { echo "MISSING: $p" ; exit 1 ; }
  done && echo "ok: all README links resolve"

# Confirm the TOML sample parses
python3 -c 'import tomllib; tomllib.loads(open("examples/config/config.toml.sample").read())'

# Confirm the systemd units parse (if systemd is around)
if command -v systemd-analyze >/dev/null 2>&1 ; then
  systemd-analyze verify examples/cron/clast-snapshot.{service,timer}
fi

# Confirm the .gitkeep files are gone
test ! -e examples/cron/.gitkeep
test ! -e examples/config/.gitkeep
test ! -e examples/workflows/.gitkeep

# Confirm the changelog has the v1.0.0 entry
grep -q '^## \[1.0.0\]' CHANGELOG.md
```

## Notes for the implementer

- **The `examples/` tree is a contract.** Once a path is in
  `contrib/npm-pack-check.sh`'s `required` list, every future PR
  that removes it fails CI. Add only files you intend to keep
  shipping; experimental drafts go elsewhere or stay uncommitted.
- **`config.toml.sample` is aspirational, not load-bearing.** The
  file's job is to document the planned config surface so users can
  start writing one against v1.x. v1.0 reads env vars exclusively.
  Be loud about this in the file's leading comments; quiet
  documentation drift is how "examples/config exists but does
  nothing" surprises bite people six months later.
- **`morning-briefing.md` is the only place the `/day-wakeup` UX
  gets a narrative.** The skill prompt itself is procedural; this
  file shows what it feels like end to end. Treat it as the
  flagship example. If it's too dry, it fails its job; if it
  invents UX, it'll be cited as a bug. Write it twice if needed.
- **The README pass is a real audit, not a typo sweep.** Read it
  with fresh eyes from the perspective of someone who's never used
  `clast`. Does the install section explain why three channels
  exist? Does the plugin section explain what installing it adds
  to a Claude Code session? Cut sentences that don't earn their
  word count. The README is the first impression for npm,
  GitHub, and Nix users alike.
- **`docs/releasing.md` is a runbook, not a story.** Bullet lists
  beat prose. Each step is a command. Recovery paths are
  explicit. Future-Beau (or anyone with merge rights) reads this
  doc in the middle of a release that didn't go to plan; clarity
  matters more than narrative flow.
- **Lockstep version bumps stay deferred.** Both `package.json`
  and `flake.nix` are still `0.1.0`. Do NOT bump either here —
  the bump is step 19's first action.
- **`make npm-pack-check` after editing
  `contrib/npm-pack-check.sh`.** The script asserts files exist
  IN the tarball; verify it green-lights the new `examples/**`
  paths. If `make npm-pack-check` reports MISSING but the file
  is on disk, `package.json`'s `files` array isn't including the
  path — debug there.
- **`systemd-analyze verify`** is a nice-to-have, not load-bearing.
  Most dev hosts (macOS in particular) lack it. Don't add it to
  `make lint`; the unit files are tiny and shellcheck-irrelevant.
- **Conventional commit suggestion**: `docs: ship v1.0 examples,
  changelog, and release runbook`. One commit covering all the
  documentation work is fine.
