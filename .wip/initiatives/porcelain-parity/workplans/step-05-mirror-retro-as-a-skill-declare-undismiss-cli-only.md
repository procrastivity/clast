# Workplan — step-05 · mirror `retro` as a skill; declare `undismiss` CLI-only

_What does this step deliver? Anchor to the roadmap entry and the spec/ADR
this step lands or extends._

Anchors: [roadmap.md](../roadmap.md) "Lane coverage-decision" (`[tracker: BDS-85]`);
[BRIEF.md](../BRIEF.md) "Confirmed decisions" — `retro` gets a plugin skill,
`undismiss` does not (Beau, 2026-07-12). This step is implementation, not
deliberation: both halves of the decision are already locked. Do not relitigate
either half.

Started: 2026-07-12.

## Decisions (made here, feed later steps)

_Locked choices that future steps can rely on. Each entry is a sentence the
next step can re-read without ambiguity._

- **`skills/retro/SKILL.md` calls `clast-plumbing` directly (never `clast`),**
  resolved via the same `CLAST_BIN` Step-0 bash block already in
  `skills/wake/SKILL.md` (copied verbatim, not re-derived). The skill *is* a
  porcelain — it plays the role `retro.bash` plays for the CLI, condensing via
  the model instead of shelling out to an LLM endpoint.
- **The skill documents only the substitution contract for the shared
  templates** (`lib/clast/prompts/retro-summary-{system,user}.md`): which
  `{{placeholder}}`s exist and what fills each. It must NOT restate the system
  prompt's own output shape (the "Shipped / Issues / Open" bullet structure,
  the 3-5 bullet cap, the omit-empty-headers rule, etc.) anywhere in the
  SKILL.md body. That restatement is exactly the bug step-01/BDS-83 shipped
  with (a paraphrase of a shared template that silently drifts from the
  template it's paraphrasing) — see BRIEF.md "What triggered this" /
  step-01. If a chunk's draft text is tempted to write such a sentence, cut it
  before committing.
- **No user-approval step.** `retro.bash`'s loop is read-and-render (or
  `--json`) with no accept/edit/skip per session — unlike `/wake`. The skill
  mirrors that: no `AskUserQuestion` anywhere in this skill.
- **Empty bodies are not summarized.** `retro.bash` short-circuits a session
  with an empty `.body` to the literal string `(no body to summarize)` without
  a model call (`retro.bash:205-207`). The skill mirrors this exactly — do not
  invoke the model for a session whose `.body` is empty/absent.
- **`clast undismiss` is declared `cli-only`.** Stated reason (already locked
  in BRIEF.md, restated here for the code-adjacent home — see Chunk 4): it is
  a thin, model-free passthrough to `clast-plumbing sessions undismiss`
  (`undismiss.bash:26`) that exists purely to reverse an accidental `[d]`
  during `clast wake`'s interactive dismiss prompt. There is no synthesis, no
  judgment call, nothing an LLM porcelain adds over the one-line plumbing
  passthrough — recovering from an accidental dismiss is a CLI-session
  gesture, not a task a Claude Code skill session would independently reach
  for.
- **The "cli-only + reason" durable record lives in two places, not one**
  (see Chunk 4 for the "why two" reasoning): BRIEF.md's Confirmed decisions
  (already there) is the near-term source step-07 reads while authoring
  `test/parity.tsv`; a short header comment in `undismiss.bash` is the
  long-term, code-adjacent home that survives if/when this initiative's
  `.wip/` artifacts are archived after ship. Recommendation for the
  Coordinator/Orchestrator to ratify — see "Open questions."
- **`test/test-clast.sh` gets a fourth hand-rolled skill-asset check**
  (`_assert_skill_retro_*`, mirroring the existing `_assert_skill_brief_*` /
  `_assert_skill_wake_*` functions), added in this step — not deferred to
  step-07. Reasoning: this is the existing per-skill static-asset convention
  (frontmatter shape, trigger phrases, CLI-command mentions) that every prior
  skill got when it was created; it is a different, coarser mechanism than the
  flag/env-var-level `test/parity.tsv` guard step-07 builds. See Chunk 5 and
  "Open questions" for the one place this could arguably go the other way.

## Chunks

_Implementation broken into reviewable pieces. Each chunk is small enough to
land in one focused commit._

1. **Scaffold `skills/retro/SKILL.md`: frontmatter + Step 0 binary
   resolution.**
   - Frontmatter: `name: retro`, a `description:` that states what the skill
     does and lists trigger phrases inline (mirror the wake/brief frontmatter
     shape — a single paragraph, `name` + `description` as the only two keys).
     Trigger phrases to cover: `/retro`, `retro`, "work retrospective",
     "what did I get done this week", "summarize my week", "condense my
     sessions" — pick phrasing that reads naturally, but the literal string
     `/retro` must appear (mirrors the wake check's `'/wake'` requirement).
     State explicitly in the description that this is a read-and-render flow
     with **no approval step**, to distinguish it from `/wake` at a glance.
   - Body: copy the "Step 0: Resolve the clast-plumbing binary" section from
     `skills/wake/SKILL.md:16-39` verbatim (same bash block, same fallback
     chain, same "not found" message). Do not re-derive or paraphrase it.

2. **Document the flag surface (mirrors `_clast_retrosum_usage`,
   `retro.bash:10-36`).**
   - `--from DATE`, `--to DATE`, `--all`, `--window work-days|file-dates`
     (default `work-days`), `--refresh`, `--json`. State the same default-window
     behavior as the CLI: with no `--from`/`--to`/`--all`, the window defaults
     to the last 7 days (`retro.bash:147-149`) — call this out explicitly since
     it's a real "one model call per new session" cost consideration, same as
     the CLI usage text warns.
   - `DATE` accepts the same forms the CLI documents: ISO (`YYYY-MM-DD`),
     `today`, `yesterday`, `last-week`, `-Nd`, `-Nw`. Don't invent additional
     forms — whatever `clast-plumbing retro` itself accepts for `--from`/`--to`
     is authoritative; the skill just passes the flag through.

3. **Document the manifest-build + per-session condensation loop.**
   - Manifest: `$CLAST_BIN --json retro --bodies --window <window> [--from
     <from>] [--to <to>]` — the deterministic structure (`days[].projects[]
     .sessions[]`, each session carrying `session_id`, `work_day`, `body`,
     `project_path`, `title`, `interrupted`). Structure is deterministic;
     only the prose condensation is the model's job (mirrors the header
     comment in `retro.bash:1-8`).
   - For each session with a non-empty `.body`: read
     `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/retro-summary-system.md` and
     `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/retro-summary-user.md`, substitute
     the user template's placeholders — **empirically confirmed by grep,
     not assumed**: `{{project}}`, `{{work_day}}`, `{{session_id}}`,
     `{{body}}` (verified again in this research pass against
     `retro-summary-user.md:1-8`, matching `retro.bash`'s
     `_clast_retrosum_build_user`) — then condense following the system
     prompt's instructions. **Do not restate what the system prompt says to
     produce** — see the Decisions section above; the SKILL.md's job is only
     "here are the four placeholders and what session field fills each," full
     stop.
   - For a session with an empty `.body`: use the literal string `(no body to
     summarize)` and skip the model call entirely (mirrors
     `retro.bash:205-207` — do not condense nothing).
   - **Caching is out of scope for this chunk's mechanics but the skill should
     state, in one sentence, that summaries the CLI already cached under
     `<journal>/.retro-summaries/<session_id>.json` are a CLI-side concern**
     — the skill does not need to read/write that cache to behave correctly
     (each skill invocation is already a fresh model call per session, same
     as a CLI `--refresh` run), and should not attempt to reverse-engineer the
     fingerprint scheme. Flagged as an explicit non-goal so no chunk is
     tempted to add file-cache logic to the skill. (If cache-sharing between
     CLI and skill turns out to matter, that is new scope — a future step,
     not this one.)

4. **Document rendering vs `--json`.**
   - Default (no `--json`): render grouped by day → project → session, each
     session showing title, short session id, an `[interrupted]` flag when
     set, and the condensed summary — mirror `_clast_retrosum_render`'s
     grouping and header format (`Retro: <from> -> <to> (<window>)`, `==
     <day> ==`, `[<project>]`, `* <title>  (<shortsid>)`) at the level of
     structure only, not by inlining the render function's exact string
     literals as a spec the model must match byte-for-byte.
   - `--json`: emit the enriched manifest with each session's `.summary`
     populated and `.body` dropped (mirrors `retro.bash:238-247`) — no
     rendering.

5. **Declare `clast undismiss` CLI-only: record the reason in a code-adjacent,
   durable home.**
   - Add a short note to `undismiss.bash`'s existing header comment (lines
     1-6) stating: this subcommand is intentionally CLI-only (no
     `skills/undismiss/SKILL.md`) because it is a thin, model-free passthrough
     to `clast-plumbing sessions undismiss` with no synthesis step — see
     `.wip/initiatives/porcelain-parity/BRIEF.md` for the full decision
     record. This is the "durable, code-adjacent home" — see "Open questions"
     for why BRIEF.md alone was judged insufficient.
   - Do **not** create `test/parity.tsv` or touch `test/test-parity.sh` — both
     are step-07/BDS-89 scope, not this step's.

6. **Add `test/test-clast.sh` static-asset checks for `skills/retro/SKILL.md`.**
   - Add `_assert_skill_retro_frontmatter`, `_assert_skill_retro_triggers`,
     `_assert_skill_retro_cli_commands`, following the exact shape of the
     existing `_assert_skill_wake_*` functions (`test/test-clast.sh:95-159`):
     frontmatter well-formed + exactly two keys, `name: retro` present,
     description length gate, trigger-phrase grep (`/retro` at minimum, plus
     one or two of the phrases chosen in Chunk 1), CLI-command grep (`CLAST_BIN
     --json retro --bodies`, `CLAST_BIN --json retro --bodies --window`, or
     whatever exact substrings Chunk 3's prose actually contains — write the
     assertions against the landed text, not before it exists).
   - Wire the three new checks into `_assert_plugin_assets` alongside the
     existing wake/brief calls (`test/test-clast.sh:183-188` pattern).
   - No read-only-invariant check is needed for retro the way brief has one
     (`_assert_skill_brief_readonly`) — retro has no write path to guard
     against; skip that function, don't add a no-op.

## Test strategy

_What the tests cover and how. Note any deferred coverage with a reason._

- The only test surface here is the static-asset layer already exercised by
  `test/test-clast.sh` (`_assert_plugin_assets`) — the plugin/skill layer has
  no behavioral surface to integration-test (no process to invoke; the "model"
  is whatever Claude Code session reads the SKILL.md). Chunk 6 is the test
  deliverable.
- No new bash/unit test files. `retro.bash`'s own behavior is already covered
  by `test/test-retro*.sh` (unchanged by this step) — this step only adds a
  skill that documents how to drive the same deterministic core by hand.
- Deferred: any check that the skill's documented flag surface matches
  `retro.bash`'s actual flags at the string level is `test/parity.tsv`'s job
  (step-07/BDS-89), not this step's. `make test`/`make lint` passing is the
  only gate here.
- Deferred: whether `undismiss`'s cli-only status is *enforced* (vs merely
  documented) is also step-07's job — the guard is what turns "documented" into
  "checked."

## Definition of done

_Concrete checks that prove the step is finished. Lean toward observable
behaviour over file-level checklists._

- `skills/retro/SKILL.md` exists, has valid two-key frontmatter (`name:
  retro` + `description:`), documents the full flag surface (`--from`,
  `--to`, `--all`, `--window`, `--refresh`, `--json`), and reads
  `retro-summary-{system,user}.md` for condensation without inlining or
  paraphrasing the system prompt's output structure anywhere in the file.
  (Manually grep the landed file for phrases like "Shipped:", "Issues:",
  "Open:" outside of a direct "these placeholders exist" context — none
  should appear as a restated spec.)
- `undismiss.bash`'s header comment states the cli-only reason and points at
  BRIEF.md.
- `test/test-clast.sh` passes with three new `_assert_skill_retro_*` checks
  wired into `_assert_plugin_assets`.
- `direnv exec . make test` and `direnv exec . make lint` are fully green —
  no tolerated known failures, per this initiative's constraints.
- Commits are Conventional Commits, each ending with
  `Claude-Session: https://claude.ai/code/session_01PyjLYz2GFRVgYS3nAtUoXm`,
  landed on `beau/bds-82-porcelain-parity` (already checked out — no new
  branch, no new PR; PR #46 covers the initiative).

## Open questions to resolve during execution

_Questions whose answers don't block starting but DO block finishing. Each
should have a "lean" so the worker isn't paralyzed._

- **Where should the "`undismiss` is cli-only + reason" record live so
  step-07's guard can consume it durably?** Three options considered:
  (a) a code comment in `undismiss.bash`'s header/usage; (b) BRIEF.md's
  Confirmed decisions alone (already present); (c) something else (e.g. a
  standalone decisions doc).
  **Lean: do both (a) and (b) — this workplan's Chunk 4/5 already implements
  that lean.** BRIEF.md is sufficient for step-07 to *read* (same initiative,
  sequential steps, nothing archived yet), but BRIEF.md is initiative-scoped
  and this initiative's `.wip/` tree is a reasonable candidate for archival
  once the initiative ships — at which point a future reader of
  `undismiss.bash` alone (no initiative context) would have no pointer to why
  there's no matching skill. A one-line code comment costs nothing and
  outlives the initiative doc. **Escalate to the Coordinator/Orchestrator only
  if they'd rather NOT touch `undismiss.bash` at all this step** (e.g. if
  there's a reason to keep this step's diff to `skills/` + `test/` only) — in
  that case BRIEF.md alone becomes the sole record and step-07 should be told
  explicitly to re-derive/relocate it when it builds `test/parity.tsv`.
- **Does `test/test-clast.sh` need retro coverage now, or is that step-07's
  job?** Confirmed by reading `test/test-clast.sh:1-216`: it has zero existing
  checks for `retro` (only `brief` and `wake` have hand-rolled
  `_assert_skill_*` functions, one set added per skill at creation time).
  Nothing structurally blocks deferring this to step-07, since step-07 adds a
  *different* mechanism (`test/parity.tsv` + `test/test-parity.sh`, flag/env-var
  level) rather than extending `_assert_plugin_assets`.
  **Lean: add it now (Chunk 6)** — it's the established per-skill convention,
  cheap, and leaves no gap where `skills/retro/SKILL.md` is the only skill
  file with zero static-asset coverage between this step landing and step-07
  landing (an unknown number of steps later, per the roadmap's Round 2
  sequencing). Escalate only if the Coordinator judges this out of step-05's
  literal scope and wants it folded into step-07 instead — either placement is
  defensible, this workplan just picks one so the Builder isn't blocked.
- **Exact trigger-phrase wording and rendered-report formatting details**
  (beyond the structural requirements in Chunk 1/4) are left to the Builder's
  judgment, mirroring the wake/brief SKILL.md's prose style. Not worth
  pre-deciding here — low cost to adjust in review.
