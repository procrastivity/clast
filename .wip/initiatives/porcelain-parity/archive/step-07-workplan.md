# Workplan — step-07 · the parity drift guard

_What does this step deliver? Anchor to the roadmap entry and the spec/ADR
this step lands or extends._

Started: 2026-07-12.

Delivers BDS-89, the last sub-issue of BDS-82 (porcelain-parity). Adds
`test/parity.tsv` (the manifest) + `test/test-parity.sh` (the guard),
registered in `suites=()` in `test/test-clast.sh` (currently starting at
line 284), so `make test` actually runs it. This is the guard that would
have caught `wake --auto` landing in the CLI without a matching skill
update (the incident that opened this whole initiative).

Source verified directly (not taken from BDS-89's text, which the brief
warns is unreliable):

- `lib/clast/clast-porcelain-subcommands/wake.bash:32` `_clast_wake_usage`
  (invoked on `-h|--help` at line 276). Flags: `--auto`. Env:
  `CLAST_WAKE_SINCE` (default `-14d`), `CLAST_WAKE_AUTO_MIN_CHARS` (default
  `60`). A third env var, `CLAST_WAKE_AUTODISMISS_NOOP`, is read by
  `clast_cmd_wake` (line 308) but **not listed in the usage heredoc** —
  this is a real, pre-existing gap the guard's assertion 1 will surface
  (see Open questions).
- `lib/clast/clast-porcelain-subcommands/brief.bash:196` `_clast_brief_usage`
  (invoked line 214). Flags: none besides `-h|--help`; one optional
  positional (`<project-slug>`), not a flag.
- `lib/clast/clast-porcelain-subcommands/retro.bash:10` `_clast_retrosum_usage`
  (invoked line 138) — this is the **porcelain** `clast retro`, dispatched
  from `bin/clast`'s `retro)` case via `clast_cmd_retro` in this same file.
  Flags: `--from`, `--to`, `--all`, `--window`, `--refresh`, `--json`,
  `-h|--help`. No env vars of its own in the usage block (it inherits
  `CLAST_LLM_*` like wake/brief, and reads `CLAST_JOURNAL_DIR` internally
  for the cache dir — not user-tunable behavior, not listed).
  **Confirmed distinct from** `lib/clast/clast-subcommands/retro.bash:13`
  `_clast_retro_usage` — that one lives under `clast-subcommands/` (not
  `clast-porcelain-subcommands/`), is `clast-plumbing retro` (Round 1,
  deterministic, no LLM, has its own `--bodies` flag CLAST_JSON-only), and
  is out of scope for this guard entirely.
- `lib/clast/clast-porcelain-subcommands/undismiss.bash` — `-h|--help` at
  line 16 prints a usage block **inline in the case statement**, not via a
  named `_clast_X_usage` function. `cli-only` entries are exempt from the
  `--help`-diff (assertion 1 only runs against `mirrored` entries — see
  Chunk 2), so this doesn't block the guard, but the manifest schema must
  say explicitly that `cli-only` rows don't need a `usage_fn` value.
- `skills/` has exactly `brief/`, `retro/`, `wake/`, each with a `SKILL.md`.
  Confirmed by reading all three in full.

## Decisions (made here, feed later steps)

1. **Manifest format:** `test/parity.tsv` is tab-separated,
   `subcommand<TAB>kind<TAB>flag_or_env<TAB>skill_md_or_reason`, where
   `kind` is one of `flag`, `env`. `mirrored` rows point `skill_md_or_reason`
   at the skill file path (e.g. `skills/wake/SKILL.md`); `cli-only` rows
   (only `undismiss`) put the stated reason there instead. One row per
   flag/env var, not per subcommand, so a new flag on an existing mirrored
   subcommand is still individually classified.

2. **Assertion 1 is bidirectional, and only runs against subcommands that
   have a `usage_fn`** — i.e. `wake`, `brief`, `retro` (porcelain). For each
   such subcommand it checks BOTH directions: every flag/env in the
   `--help` output must appear in the manifest (catches an unclassified
   new flag — the `--auto` incident), AND every manifest `mirrored` row
   for that subcommand must appear in its own `--help` output (catches a
   flag/env that's mirrored-and-documented-elsewhere but silently dropped
   from the CLI's own usage text — see `CLAST_WAKE_AUTODISMISS_NOOP`
   below, Decisions 5/6 and Open questions). This is still "assertion 1"
   — one check, two directions on the same manifest-vs-help comparison —
   not a seventh assertion. It does NOT run against `undismiss`: the
   manifest schema marks `cli-only` rows as `usage_fn: n/a`, so undismiss
   keeps a stated-reason cli-only entry (assertion 3) without needing a
   synthetic `_clast_undismiss_usage` function that BDS-85's decision
   never asked for. `undismiss` has no mirrored surface to check in
   either direction.

3. **Skill-only category: ADDED, not scoped away.** Verified both of
   step-03/BDS-84's findings still hold in current source:
   - Promote flow: `wake.bash:161-168`'s `_clast_wake_prompt_choice` is
     literally `[a] Accept [e] Edit [d] Dismiss [s] Skip [q] Stop here` —
     no promote options. `skills/wake/SKILL.md:216-243` (the per-session
     AskUserQuestion block) offers `Accept + promote decision/common-issue/
     workflow` and *itself* documents this as deliberate skill-only
     capability, explicitly flagging that BDS-89 needs a skill-only
     category (SKILL.md:237-243).
   - Model-call timing: `wake.bash` times `clast_porcelain_llm_chat`
     (`clast-porcelain-lib.bash:142`) with `clast_porcelain_now`/
     `clast_porcelain_elapsed` around the curl subprocess and reports
     `Model time: Ns` (wake.bash:600). `skills/wake/SKILL.md:169-176`
     explicitly notes it has no such subprocess (draft generation *is*
     the model turn) and has no `Model time:` line — again pointing at
     this exact guard needing a skill-only category.

   A `cli-only` entry would misrepresent both as "the CLI has this and
   the skill lags," which is backwards. `test/parity.tsv` therefore gets
   a third `kind: skill_only` row type — `subcommand<TAB>skill_only<TAB>
   capability_name<TAB>stated_reason` — checked only for presence of a
   `stated_reason` (assertion 3's rule, reused: no bare/unexplained
   entries). Two rows: `wake / skill_only / promote-decision-common-issue-
   workflow / …` and `wake / skill_only / model-call-timing / …`, each
   reason a short paraphrase of the SKILL.md notes above. This is scoped
   to what step-03 found; it is not a general "skill has extra features"
   audit — new skill-only capabilities get added to this table as they're
   discovered, same as `cli-only`.

4. **Assertion 6 (AskUserQuestion ≤ 4 options) is IN.** Verified against
   current `skills/wake/SKILL.md`:
   - Triage question (lines 96-105, "Backlog" header): 5 options
     (`Process all`, `Yesterday only`, `Choose days back`,
     `Dismiss older, process recent`, `Quit`).
   - Per-session question (lines 216-231, "Session draft" header): 8
     options (`Accept`, 3× `Accept + promote …`, `Edit`, `Skip`,
     `Dismiss`, `Stop here`).
   Both exceed AskUserQuestion's hard cap of 4 (BDS-100). This guard is
   exactly the mechanism BDS-100's own filing says is missing (existing
   `_assert_skill_*` checks only grep for literal strings, no cap
   awareness). Weakening the assertion to dodge the failure would defeat
   the point of the step, so the ≤4 rule lands exactly as specified —
   these two known violations are handled via baseline (Decision 5), not
   by softening the check itself.

5. **Ruling (Orchestrator, overrides this workplan's original ship-red
   recommendation): a narrow, self-expiring baseline, not a red build.**
   `make test` must be fully green at landing. Every already-known
   violation this guard would otherwise flag is recorded as one line in a
   new baseline file, `test/parity-known-red.tsv`, kept obviously separate
   from `test/parity.tsv` (the manifest proper). Format:
   `assertion<TAB>scope<TAB>identifier<TAB>ticket` — one row per specific
   violation, e.g.:

   ```
   6	skills/wake/SKILL.md	triage-question:5-options	BDS-100
   6	skills/wake/SKILL.md	per-session-question:8-options	BDS-100
   1	wake	CLAST_WAKE_AUTODISMISS_NOOP-missing-from-help	BDS-101
   5	CLAST_AUTHOR	undocumented	BDS-101
   5	CLAST_MACHINE	undocumented	BDS-101
   5	CLAST_VERBOSE	undocumented	BDS-101
   5	CLAST_RETRO_PROGRESS	undocumented	BDS-101
   ```

   (BDS-101 is being filed by the Orchestrator for the four undocumented
   vars plus the AUTODISMISS_NOOP heredoc gap; BDS-100 already exists for
   the AskUserQuestion overflow.)

   Mechanism (applies to every assertion that supports baselining — 1, 5,
   6; assertions 2/3/4 have no known violations today and get no baseline
   rows unless one is found later):
   - Each assertion computes its violation set as before.
   - For each violation, look it up in `parity-known-red.tsv` by
     `(assertion, scope, identifier)`. A match is a silent pass for that
     one violation (marked "seen" for the self-expiry check below); an
     unmatched violation is still a hard **ERROR** — fail-closed is fully
     preserved for anything not pre-listed and ticketed.
   - **Self-expiry:** after checking all live violations, any baseline row
     that was NOT matched against a live violation (i.e. the underlying
     bug no longer reproduces) is itself a hard **ERROR** —
     `"stale baseline entry <row>: violation no longer reproduces, delete
     this line"`. This is what makes the baseline shrink-only: nobody can
     leave a fixed entry behind, and nobody can add an entry for a
     violation that doesn't actually exist (it would immediately fail
     self-expiry).
   - A baseline row with an empty/missing ticket column is itself a hard
     error (reuses assertion 3's no-bare-entries rule — every baselined
     violation must be traceable to a ticket).
   - `CLAST_WAKE_AUTODISMISS_NOOP-missing-from-help` is only mechanically
     checkable — and thus only baselineable — because of assertion 1's
     bidirectional check (Decision 2). This was the one gap in the
     original design: without bidirectionality there was no code path
     that ever produced this violation, so it couldn't self-expire against
     anything. Bidirectionality closes that gap without adding a seventh
     assertion.
   - Net effect: `make test` is green at landing; PR #46 stays mergeable;
     every known violation is recorded in-tree with a ticket; genuinely
     new/unlisted drift still fails the build immediately; the baseline
     can only shrink as BDS-100/BDS-101 get fixed (fixing the underlying
     bug without deleting the baseline row is itself a build failure,
     which is the intended nudge to delete it).

6. **Assertion 5 (`CLAST_*` vars vs `docs/reference/config.md`) is scoped
   to *user-facing* env-var reads, not every token matching `CLAST_[A-Z_]+`.**
   Grepping `lib/clast/**.bash` for that pattern also catches: internal
   locals/globals conventionally prefixed `_CLAST_*` (e.g.
   `_CLAST_ENTRY_ROW_JSON`, `_CLAST_PREFIX`, `_CLAST_REST`,
   `_CLAST_BREADCRUMB_STRIPPED`) — these are never read from the
   environment, just bash variable names that happen to start with
   `CLAST_`; module-sourcing guards (`CLAST_*_LIB_SOURCED`); and
   dispatcher-internal plumbing (`CLAST_LIB`, `CLAST_JSON`,
   `CLAST_COMMAND_MARKER_RE`) set by the dispatcher/internal code itself,
   not meant to be user-set. None of those belong in
   `docs/reference/config.md`, and asserting on them would be
   permanently, uselessly red. Assertion 5 matches only bare (no leading
   underscore) `${CLAST_[A-Z_]+` / `$CLAST_[A-Z_]+` **reads** (i.e.
   `${CLAST_FOO:-...}`, `${CLAST_FOO}`, `"$CLAST_FOO"` — value reads, not
   the handful of lines that assign `CLAST_COMMAND_MARKER_RE=...` or
   `export CLAST_X` as an internal constant).

   **REVISED during execution (Task 3/Builder-03 escalation, resolved by
   the Orchestrator — see the escalation record on ledger todo_id 547 and
   scratchpad 130's Escalations section):** the *original* wording of this
   Decision said the exclusion was "`_SOURCED` family and `CLAST_NOW_EPOCH`"
   only — but that coded rule disagreed with this Decision's own prose,
   which already named `CLAST_LIB`/`CLAST_JSON` as dispatcher-internal.
   Builder-03 caught the contradiction: the literal 2-name exclusion list
   also let `CLAST_LIB`, `CLAST_JSON`, and `CLAST_COMMAND_MARKER_RE`'s read
   site (distinct from its already-excluded assignment) through as
   violations — 7 total, not 4. It correctly refused to force the count by
   silently widening the exclusion on its own authority and escalated.

   **Final ruling: exemptions are DATA, not hardcoded bash.** `test/parity.tsv`
   gets a fifth `kind`: `internal` — `subcommand<TAB>internal<TAB>CLAST_VAR<TAB>
   stated_reason`, subcommand `*` (global, like the `CLAST_LLM_*` rows). Four
   rows: `CLAST_LIB` (dispatcher bootstrap path), `CLAST_JSON` (internal
   carrier for the `--json` flag), `CLAST_COMMAND_MARKER_RE` (internal regex
   constant), and `CLAST_NOW_EPOCH` (test-only time-freeze hook — moved out
   of hardcoded bash into the manifest too, for consistency; no more
   exemptions live in code than have to). Assertion 3's existing
   non-empty-reason check extends to `internal` rows for free (add
   `internal` to its kind case statement) — one rule audits every kind of
   "here is a thing and here is why it's exempt" the manifest carries.
   `internal` rows **self-expire exactly like baseline rows**: assertion 5
   already computes the full read-site set for its scan, so if a named
   `internal` row has **zero** read sites anywhere in `lib/clast/**.bash`,
   that row is itself a hard error ("stale exemption, delete this row") —
   the exemption list is shrink-only, same as the baseline. The `_SOURCED`
   family stays a code-level suffix-pattern rule (it's a family, not an
   enumerable name, so it can't be a manifest row) with a one-line comment
   explaining why it's handled differently.

   Applying the final scoping and checking `docs/reference/config.md`
   today turns up exactly the expected **four already-undocumented real
   env vars**: `CLAST_AUTHOR` (`entries.bash:561`), `CLAST_MACHINE`
   (`entries.bash:562`), `CLAST_VERBOSE` (`projects.bash:159`,
   `sessions.bash:345`, `stats.bash:180`), `CLAST_RETRO_PROGRESS`
   (`retro.bash:44`, porcelain). Per Decision 5, these four go into
   `test/parity-known-red.tsv` against BDS-101 rather than shipping the
   build red or silently widening the scoping to hide them. Do not touch
   `docs/reference/config.md` to "fix" it (out of scope — hard
   constraint); recording the finding in the baseline is the deliverable.

## Chunks

_Implementation broken into reviewable pieces. Each chunk is small enough
to land in one focused commit._

1. **`test/parity.tsv`** — the manifest. Three `kind` rows (`flag`, `env`,
   `skill_only`) plus `cli-only` subcommand-level entries. Seed content:
   - `wake` mirrored: flags `--auto`; env `CLAST_WAKE_SINCE`,
     `CLAST_WAKE_AUTO_MIN_CHARS`, `CLAST_WAKE_AUTODISMISS_NOOP` (the third
     one is real and read by the code even though it's undocumented in
     the usage heredoc — see assertion-1 note above; list it here as
     `mirrored` pointing at `skills/wake/SKILL.md` since the skill *does*
     document it at SKILL.md:71, and let assertion 1 catch the CLI-side
     heredoc gap on its own, which is a legitimate, separate finding).
   - `wake` skill_only: `promote-decision-common-issue-workflow`,
     `model-call-timing` (Decision 3).
   - `brief` mirrored: no flags beyond `-h|--help`; no env vars beyond
     `CLAST_LLM_*` (shared, not brief-specific — see Chunk note below on
     whether to list `CLAST_LLM_*` per-subcommand or once, globally;
     lean: once globally, not per-subcommand, since it's identical across
     all three and repeating it three times adds no signal).
   - `retro` (porcelain) mirrored: flags `--from`, `--to`, `--all`,
     `--window`, `--refresh`, `--json`.
   - `undismiss` cli-only: reason quoting the header comment in
     `undismiss.bash:7-11`.
   - Global env row (not per-subcommand): `CLAST_LLM_BASE_URL`,
     `CLAST_LLM_API_KEY`, `CLAST_LLM_MODEL` — required by all three LLM
     porcelains, already documented in `docs/reference/config.md`.
2. **`test/test-parity.sh`, assertions 1–3** — the bidirectional
   `--help`-vs-manifest diff (Decision 2: manifest ⊆ help AND help ⊆
   manifest for mirrored rows on subcommands with a `usage_fn`), the
   mirrored-flags-in-SKILL.md check, and the cli-only-reason check.
   Function-level style matching `test-wake-auto.sh` (source the
   porcelain libs, call `_clast_wake_usage`/`_clast_brief_usage`/
   `_clast_retrosum_usage` directly rather than subprocessing `clast
   <cmd> --help`, since `CLAST_LIB`/`CLAST_LLM_*` stubbing is already the
   established pattern in this test suite — subprocessing would need the
   same env exported anyway and adds nothing).
3. **`test/test-parity.sh`, assertion 4** — shared-defaults check
   (`CLAST_WAKE_SINCE` default `-14d` matches between `wake.bash:294`'s
   `${CLAST_WAKE_SINCE:--14d}` and `skills/wake/SKILL.md:56`'s
   `${CLAST_WAKE_SINCE:--14d}`). Implementation: grep both files for the
   literal `${CLAST_WAKE_SINCE:-` pattern and assert the captured default
   token matches. This is deliberately narrow (one shared default, the
   one the brief calls out by name) rather than a generic "diff every
   `${VAR:-default}` occurrence across CLI and skill," which would be a
   much bigger and fuzzier undertaking than BDS-89 scopes for.
4. **`test/test-parity.sh`, assertion 5** — `CLAST_*` vars vs
   `docs/reference/config.md`, scoped per Decision 6. Implementation:
   grep `lib/clast/**.bash` for `\$\{?CLAST_[A-Z_]+` reads, strip the
   `_SOURCED` family and the `CLAST_NOW_EPOCH` allowlist entry, then
   assert each survivor's bare name appears somewhere in
   `docs/reference/config.md`.
5. **`test/test-parity.sh`, assertion 6** — AskUserQuestion ≤ 4 options,
   scanning `skills/*/SKILL.md`. Implementation: for each fenced block or
   list under a line matching `**options**:` (the convention all three
   SKILL.md files use — see `wake/SKILL.md:101-105` and `:224-231`),
   count the bullet items until the next non-bullet line, assert ≤ 4.
   Keep the parser simple (bullet-counting, not a markdown AST) since all
   three SKILL.md files use one consistent list style.
6. **`test/parity-known-red.tsv` + baseline-lookup/self-expiry logic in
   `test/test-parity.sh`** — the seven rows from Decision 5 (2× assertion
   6, 1× assertion 1, 4× assertion 5), plus a small shared helper each
   baselineable assertion (1, 5, 6) calls: given a violation's
   `(assertion, scope, identifier)`, check the baseline, mark it seen if
   matched, and only error if unmatched. After all assertions run, a
   final pass errors on any baseline row never marked seen (self-expiry —
   Decision 5). This chunk depends on Chunks 2 and 5 already existing
   (assertions 1 and 6 need to be computing violations before their
   results can be baselined), so it lands after them, not before.
7. **`suites=()` registration** — add `test/test-parity.sh` to
   `test/test-clast.sh`'s `suites=()` array (after `test/test-retro-cmd.sh`,
   matching the file's existing "newest/most-related-first" informal
   ordering — exact position is not load-bearing, just keep it out of the
   plugin-asset-check block above the array).
8. **Fail-closed proof, done and reverted before the final commit** (not a
   shipped code chunk — see Test strategy below for the exact procedure).

## Test strategy

_What the tests cover and how. Note any deferred coverage with a reason._

- `test/test-parity.sh` is itself the test — no separate unit-test-the-
  test-harness layer, consistent with every other suite in `test/`.
- Each assertion (1–6) gets at least one positive case (current tree
  passes/fails as expected) exercised by running `bash test/test-parity.sh`
  directly during Chunks 2–5, before wiring into `suites=()`.
- **Baseline behavior (Chunk 6) gets three explicit cases, since the
  self-expiry mechanism is new machinery, not a straightforward assert:**
  1. **A baselined violation passes silently.** With
     `test/parity-known-red.tsv` containing its real seven rows and the
     tree unmodified, `bash test/test-parity.sh` exits 0 — the two
     AskUserQuestion overflows, the AUTODISMISS_NOOP heredoc gap, and the
     four undocumented env vars all reproduce as live violations, all
     match a baseline row, none error.
  2. **A NEW, unlisted violation still errors.** Temporarily add one more
     bogus/unclassified item not in the baseline (reuse the fail-closed
     proof's bogus flag below, or a fifth undocumented `CLAST_*` var) and
     confirm it still hard-errors even with the baseline file present and
     otherwise matching — i.e. the baseline only suppresses exact,
     pre-listed matches, not "assertion 1/5/6 in general."
  3. **A stale baseline entry errors.** Temporarily comment out or
     fix-in-place one baseline-covered violation (e.g. locally add
     `CLAST_AUTHOR` to `docs/reference/config.md` in a scratch edit, not
     committed) and confirm `test/test-parity.sh` now fails with the
     stale-entry message naming that row — proving self-expiry actually
     fires rather than silently tolerating a baseline that's drifted out
     of sync with reality. Revert the scratch edit after.
  All three are exercised manually during Chunk 6 and don't need to
  persist as fixtures — the point is proving the mechanism once, the same
  spirit as the fail-closed proof below.
- **Fail-closed proof (required, mirrors step-04's `check-version-sync`
  proof: corrupt `plugin.json` → exit 2 → restore → exit 0):**
  1. Add a bogus, unclassified flag to one usage heredoc — e.g. append
     `  --bogus-flag  a flag that isn't in the manifest.` to
     `_clast_wake_usage`'s `Flags:` block in `wake.bash`.
  2. Run `direnv exec . make test` (or `bash test/test-parity.sh`
     directly). Confirm assertion 1 **ERRORs** (nonzero exit, clear
     message naming `--bogus-flag` and `wake`) — not a skip, not a silent
     pass, and NOT suppressed by the baseline (`--bogus-flag` is not a
     baseline row, so this also doubles as baseline test case 2 above).
  3. `git checkout -- lib/clast/clast-porcelain-subcommands/wake.bash` (or
     manually revert the one-line edit) to restore the tree.
  4. Re-run `direnv exec . make test`. Confirm it's fully **green** —
     with the baseline mechanism in place there are no remaining expected
     reds; every previously-red finding is now a matched, ticketed
     baseline row.
  5. Record the before/after exit codes and the assertion-1 error text in
     the chunk's commit message or PR description, the same way step-04
     recorded the corrupt-then-restore proof for `check-version-sync`.
- Deferred: no coverage for a hypothetical `contrib/` pre-commit wiring —
  the initiative's own open question ("does the guard belong in
  test-clast.sh, contrib/, or both") is resolved here as **test-clast.sh
  only** (see Open questions below); a pre-commit hook is out of scope
  unless Beau asks for it later.

## Definition of done

_Concrete checks that prove the step is finished. Lean toward observable
behaviour over file-level checklists._

- `test/parity.tsv`, `test/parity-known-red.tsv`, and `test/test-parity.sh`
  exist, own no files outside those three plus the `suites=()` edit in
  `test/test-clast.sh`.
- `test/test-parity.sh` is present in `test/test-clast.sh`'s `suites=()`
  array and actually runs as part of `make test`, re-verified by reading
  the output of a fresh `direnv exec . make test` run and confirming it
  shows `== test/test-parity.sh ==`.
- `direnv exec . make test` is **fully green** at the final commit — no
  expected reds shipped. `direnv exec . make lint` is fully green.
- `test/parity-known-red.tsv` contains exactly the seven rows listed in
  Decision 5 (2× assertion 6 against `skills/wake/SKILL.md` → BDS-100;
  1× assertion 1 for `CLAST_WAKE_AUTODISMISS_NOOP` on `wake` → BDS-101;
  4× assertion 5 for `CLAST_AUTHOR`/`CLAST_MACHINE`/`CLAST_VERBOSE`/
  `CLAST_RETRO_PROGRESS` → BDS-101), each with a non-empty ticket column.
- Baseline self-expiry is proven, not just implemented: the three test
  strategy cases (baselined violation passes silently; new unlisted
  violation still errors even with the baseline present; a stale/fixed
  baseline entry itself errors) have each been run once and their
  results recorded.
- The fail-closed proof (Test strategy, above) has been performed and its
  result recorded: bogus flag → nonzero exit + clear error naming the
  flag and subcommand, not suppressed by the baseline; reverted → fully
  green (no remaining expected reds — everything real is now a matched
  baseline row).
- `skills/wake/SKILL.md` and `docs/reference/config.md` are both untouched
  by this step (`git diff --stat` shows no changes under `skills/` or
  `docs/`) — the baseline records these findings without fixing them.
- BDS-89 (and therefore BDS-82) is ready to move to **In Review** per the
  brief's lifecycle rule once the last commit here is pushed.

## Open questions to resolve during execution

_Questions whose answers don't block starting but DO block finishing. Each
should have a "lean" so the worker isn't paralyzed._

- ~~Does `CLAST_WAKE_AUTODISMISS_NOOP` belong in `_clast_wake_usage`'s
  `Env:` block?~~ **Resolved by the Orchestrator's ruling.** Assertion 1
  is now bidirectional (Decision 2), so this gap is mechanically caught
  as an assertion-1 violation (`wake` / `CLAST_WAKE_AUTODISMISS_NOOP-
  missing-from-help`) and recorded in `test/parity-known-red.tsv` against
  BDS-101 (Decision 5). No seventh assertion needed. Not something to
  fix by editing `wake.bash`'s heredoc in this step (out of scope — hard
  constraint; BDS-101 owns the actual fix).
- **Exact wording/placement of the fail-closed bogus flag.** Lean: use
  `--bogus-flag` appended to `wake`'s `Flags:` block specifically (not
  brief's or retro's) since wake is the subcommand the whole initiative
  started from (`--auto`), making the proof narratively consistent with
  why this guard exists. Any of the three usage heredocs would work
  equally well mechanically. Also confirm this exact bogus flag isn't
  coincidentally a real baseline identifier before using it (it isn't —
  none of the seven baseline rows are `wake`/flag-kind).
- ~~Final ship-red-vs-allowlist call for Decisions 4 and 6.~~ **Resolved
  by the Orchestrator's ruling:** self-expiring baseline
  (`test/parity-known-red.tsv`), `make test` fully green at landing, not
  a red ship. See Decision 5 for the mechanism.
- **Baseline row `scope`/`identifier` string format is this workplan's
  best guess, not yet exercised against the real implementation.** The
  seven rows in Decision 5 use human-readable identifiers (e.g.
  `triage-question:5-options`, `undocumented`); the builder should keep
  whatever exact strings assertions 1/5/6 naturally produce when they
  detect a violation, adjusting the baseline file's identifier column to
  match verbatim (or normalizing both sides through one small formatting
  helper) rather than inventing a separate string convention that then
  needs manual syncing. Lean: derive the baseline identifier format from
  whatever the assertion's own error message already contains, so the
  two can never drift apart.
