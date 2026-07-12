# Workplan — step-04 · plugin hygiene

_What does this step deliver? Anchor to the roadmap entry and the spec/ADR
this step lands or extends._

Started: 2026-07-12.

## Decisions (made here, feed later steps)

_Locked choices that future steps can rely on. Each entry is a sentence the
next step can re-read without ambiguity._

- **`plugin.json` bumps *down* to `0.0.6`, not the other way.** `0.0.6` is the
  latest git tag and matches `package.json` and `flake.nix` (already in sync
  with each other) and the latest `CHANGELOG.md` entry (`## [0.0.6] -
  2026-07-08`). There is no `v0.1.0` tag anywhere in `git tag`. `plugin.json`'s
  `0.1.0` is therefore the drifted value, not the other three files — reconcile
  it to `0.0.6`.
- **`contrib/check-version-sync.sh` grows a third check: `plugin.json`'s
  `.version` (via `jq`) must equal `package.json`'s, added as a third
  independent comparison alongside the existing package.json↔flake.nix check
  (not folded into one string), each with its own clear mismatch message.**
  This target is wired into CI (`.github/workflows/{test,release}.yml` both
  run `make check-version-sync`) but is **not** part of `make test` or `make
  lint` — it's invoked directly. Builders must run `make check-version-sync`
  explicitly, not rely on `make test`/`make lint` to catch a regression here.
- **BDS-88's "stray wip artifacts" claim does not match current repo state —
  confirmed independently, not just trusted from the Coordinator's report.**
  Verified from `/Users/beausimensen/Code/clast` (confirmed via `pwd`, not a
  subdirectory or the wip plugin's own checkout under `~/.claude/plugins/`):
  `ls -la` at repo root shows no `commands/` or `agents/` directory and no
  `.claude-plugin/README.md` (only `.claude-plugin/plugin.json` exists).
  `git ls-files` under `commands/`, `agents/`, `.claude-plugin/` returns only
  `.claude-plugin/plugin.json`. `git status --ignored --porcelain` shows no
  untracked or ignored `commands/`/`agents/`/`.claude-plugin/README.md` paths
  (only `.direnv/`, `.wip/tracker-cache.json`, `result`, and this workplan
  file itself are untracked/ignored). `git log --oneline --all -- commands
  agents .claude-plugin/README.md` returns empty — these paths have never
  existed in this repo's history, tracked or untracked, on any branch, ever.
  **This specific claim in BDS-88 is false for the current repo state.** The
  defensive `.gitignore` fix still ships anyway (see Chunks) as a no-regret
  guard against this class of drift recurring, but there is nothing to
  *remove* — the Coordinator should surface this discrepancy to the
  Orchestrator (see Open questions) rather than the Researcher silently
  reinterpreting BDS-88's scope.
- **`docs/reference/plugin.md`'s embedded SKILL.md copies are confirmed stale
  by direct diff, not just by the Coordinator's characterization.** The
  embedded `wake` copy (plugin.md lines 25–178) is missing, relative to the
  real 228-line `skills/wake/SKILL.md`: the **Step 0 `clast-plumbing` binary
  resolver** (real file's Step 0, lines 16–39 — searches
  `$CLAUDE_PLUGIN_ROOT`, `PATH`, then a `find` fallback under `~/.claude`),
  the **`stale: true` session filter** in Step 2 (real line 59 — embedded copy
  only filters `curated: false`), the **Step 2a no-op auto-dismissal**
  entirely (real lines 61–80, using the `substantive` flag and
  `CLAST_WAKE_AUTODISMISS_NOOP`), and the **multi-day triage step** (real
  lines 82–106 — embedded copy has a much shorter, differently-worded
  triage). It also lacks the `<!-- step-12 addition -->` promotion-body
  convention (real lines 219). The embedded `brief` copy is *also* stale in
  the same way (diffed independently) — missing brief's own Step 0 binary
  resolver and other content added since plugin.md was last synced, so
  staleness is not unique to wake. The "Skill 3: breadcrumb (optional, v1.1)"
  section documents a skill that was **never shipped** — `ls skills/` shows
  only `wake/` and `brief/`. Fix direction (already decided in the ticket, not
  re-litigated here): replace both embedded full bodies with links to the
  real files, delete the breadcrumb section, delete the stale "Open questions
  about skills" section (plugin.md lines 431–439 — these are pulled-forward
  v1-planning questions, now moot or answered by the shipped skills).

## Chunks

_Implementation broken into reviewable pieces. Each chunk is small enough to
land in one focused commit._

1. **Version reconciliation.** Change `.claude-plugin/plugin.json`'s
   `"version"` from `"0.1.0"` to `"0.0.6"`. Extend
   `contrib/check-version-sync.sh` with a third check comparing
   `.claude-plugin/plugin.json`'s `.version` (via `jq`) against
   `package.json`'s, with its own mismatch message (mirror the existing
   package.json/flake.nix check's shape — don't collapse three files into one
   ad hoc comparison). Commit type: `fix:` (this is a real drift fix plus a
   guard, not a chore).
2. **Defensive `.gitignore` entries for the stray-artifact class.** Add
   `/commands/`, `/agents/`, and `/.claude-plugin/README.md` (root-anchored,
   so they don't shadow legitimate paths elsewhere in the tree) to
   `.gitignore`, as insurance against a `wip`-plugin-style checkout artifact
   ever being loaded as clast's own commands/agents. This is a no-op today —
   nothing currently matches — and that's fine; it's a guard against
   recurrence, not a cleanup. Commit type: `chore:`.
3. **Single-source `docs/reference/plugin.md`.** Replace the "Skill 1: wake"
   and "Skill 2: brief" embedded full-body code blocks with short summaries
   (2-3 sentences each, paraphrasing the existing "Purpose" line already in
   the doc — do not invent new behavioral claims) plus a link to the real
   `skills/wake/SKILL.md` / `skills/brief/SKILL.md` as the source of truth.
   Delete the "Skill 3: breadcrumb (optional, v1.1)" section entirely (lines
   317–360) — it documents vaporware. Delete the "Open questions about
   skills" section entirely (lines 431–439) — stale v1-planning questions,
   answered or moot now that wake/brief have shipped. Update the doc's intro
   line ("Three skills total: `wake`, `brief`, and (optional, deferred to
   v1.1) `breadcrumb`.") to drop the breadcrumb reference since the section
   documenting it is gone. Do **not** touch anything else in `docs/` (AGENTS.md
   restriction; BDS-88 only authorizes this one file). Do **not** describe
   `--auto` or any other PR #45 (`wake --auto`, still open/unmerged) behavior
   as current. Commit type: `docs:`.

## Test strategy

_What the tests cover and how. Note any deferred coverage with a reason._

- **Chunk 1**: after editing, run `direnv exec . make check-version-sync`
  directly and confirm it prints a three-way sync success (or equivalent) and
  exits 0 — this target is CI-wired but not part of `make test`/`make lint`,
  so it must be exercised explicitly, not assumed covered by the standard
  gates. Also deliberately break one field locally (e.g. edit a scratch copy)
  to confirm the new check actually fails closed before reverting — don't
  trust an assertion that's never been proven to fail.
- **Chunk 2**: no automated test exists for `.gitignore` correctness (there's
  nothing to ignore right now). Verify manually: `git status --ignored
  --porcelain` still shows the same set of ignored/untracked paths as before
  the change (i.e., the new entries don't accidentally start ignoring
  something tracked or intended-to-be-tracked).
- **Chunk 3**: no automated test covers doc content. Verify manually: the
  rewritten "Skill 1"/"Skill 2" sections' links resolve
  (`../../skills/wake/SKILL.md`, `../../skills/brief/SKILL.md` — check the
  relative path from `docs/reference/plugin.md`), and grep the file afterward
  for `breadcrumb` and `--auto` to confirm neither survives.
- **All chunks**: `direnv exec . make test` and `direnv exec . make lint`
  must be fully green (zero failures) before each commit lands — this is a
  hard constraint from the initiative BRIEF, not optional per-chunk judgment.
  `make lint` is `shellcheck` over a fixed file list (see `Makefile:7`); none
  of these three chunks touch a shellcheck-covered file except
  `contrib/check-version-sync.sh` (chunk 1), so `make lint` mostly guards
  against a bash syntax slip there. `make test` runs `test/test-clast.sh`,
  which doesn't currently assert anything about `plugin.json`, `.gitignore`,
  or `docs/reference/plugin.md` — expect it to pass trivially for chunks 2
  and 3 (nothing to regress), and to pass for chunk 1 as long as the
  `check-version-sync.sh` edit doesn't break `shellcheck`.

## Definition of done

_Concrete checks that prove the step is finished. Lean toward observable
behaviour over file-level checklists._

- `.claude-plugin/plugin.json`'s `.version` reads `0.0.6` and matches
  `package.json` and `flake.nix`.
- `direnv exec . make check-version-sync` exits 0 and its output confirms all
  three files agree.
- `.gitignore` contains `/commands/`, `/agents/`, and
  `/.claude-plugin/README.md`.
- `docs/reference/plugin.md` contains no embedded SKILL.md body copies for
  wake or brief — only summaries + links to the real files — and no
  breadcrumb section, no "Open questions about skills" section, and no
  mention of `--auto`.
- `direnv exec . make test` and `direnv exec . make lint` both pass with zero
  failures on the final commit.
- No file outside `.claude-plugin/plugin.json`, `contrib/check-version-sync.sh`,
  `.gitignore`, and `docs/reference/plugin.md` was touched by this step.
- All three chunks landed as separate commits with conventional prefixes on
  `beau/bds-82-porcelain-parity` — no new branch, no new PR (PR #46 covers
  the whole initiative).

## Open questions to resolve during execution

_Questions whose answers don't block starting but DO block finishing. Each
should have a "lean" so the worker isn't paralyzed._

- **BDS-88's "stray wip artifacts" claim doesn't match repo reality (see
  Decisions) — is this a Researcher/Coordinator misunderstanding, or does
  BDS-88 need its problem statement corrected in Linear?** Not resolvable by
  the Builder — the Researcher does not touch Linear. **Lean: ship the
  `.gitignore` entries anyway as cheap insurance (chunk 2 is a no-regret
  guard regardless of whether the original claim was ever true), and the
  Coordinator escalates the discrepancy to the Orchestrator for awareness —
  not a blocking decision for this step's completion.** Do not have a Builder
  attempt to "find" the stray artifacts harder or search
  `~/.claude/plugins/` — that's the live `wip` plugin this orchestration runs
  on; it must not be read-with-intent-to-modify, moved, or deleted under any
  circumstance (see the hard safety constraint in this step's brief).
- **How much summary to keep for wake/brief in the rewritten plugin.md
  (chunk 3)?** The ticket says "plus maybe a short 'what this skill does'
  summary if useful." Lean: keep it short — reuse/paraphrase the existing
  "Purpose" one-liner already present in plugin.md for each skill (e.g. wake's
  "The primary curation point. Snapshot fresh transcripts, iterate
  yesterday's sessions...") rather than writing new prose, so there's nothing
  new to go stale.
- **Does dropping the breadcrumb section require touching the doc's opening
  "Three skills total..." sentence (plugin.md line 5)?** Lean: yes — leaving
  it as-is would itself become a stale claim the moment the breadcrumb
  section is deleted. Update it to say "Two skills total: `wake` and
  `brief`." (or equivalent) as part of chunk 3, since it's inside the one file
  BDS-88 authorizes touching.