# Workplan — step-03 · wake: close the pre-#45 divergences

_What does this step deliver? Anchor to the roadmap entry and the spec/ADR
this step lands or extends._

Started: 2026-07-12.

This step works BDS-84's drift table row by row. Per BRIEF.md's confirmed
decision ("the CLI is the reference; the skill conforms to it"), every row
defaults to **fix the skill**. Two rows deviate from that default; each
carries a stated reason below, as required. All edits land in
`skills/wake/SKILL.md` only — `lib/clast/clast-porcelain-subcommands/wake.bash`
is the reference and is not touched in this step.

## Divergence table

| # | Row | CLI (reference) | Skill (current) | Resolution |
|---|---|---|---|---|
| 1 | Scan window | `wake.bash:294` `local since="${CLAST_WAKE_SINCE:--14d}"`, used at `wake.bash:296`; documented in `_clast_wake_usage` (`wake.bash:48`: `CLAST_WAKE_SINCE  Scan window (default -14d).`) | `SKILL.md:53,56` hardcodes `$CLAST_BIN --json sessions --since -30d` with a plain-prose "last 30 days" | **Fix the skill.** Honor `CLAST_WAKE_SINCE` with a `-14d` default, same as the CLI. |
| 2 | Promote decision/common-issue/workflow flow | `_clast_wake_prompt_choice` (`wake.bash:161-168`) offers only `[a]ccept [e]dit [d]ismiss [s]kip [q]uit` — no promote options at all | `SKILL.md:198-216` AskUserQuestion offers `Accept + promote decision/common-issue/workflow` alongside Accept/Edit/Skip/Stop, folding promoted items into the entry body (`SKILL.md:248-250`) with an explicit v1.1 TODO to split into `clast-plumbing decisions/common-issues/workflows write` subcommands | **Record as intended** (deviates from the fix-the-skill default; reason below). |
| 3 | Per-session Dismiss | `_clast_wake_prompt_choice` has `[d]` Dismiss, wired at `wake.bash:553-559` to `clast-plumbing sessions dismiss "$sid" --reason "dismissed via clast wake"` (permanent, separate from a plain skip) | `SKILL.md:198-214` has no Dismiss-equivalent — "Skip" (`SKILL.md:136`) just doesn't write, it never dismisses | **Fix the skill.** Add a `Dismiss` option to the per-session AskUserQuestion, wired to `$CLAST_BIN sessions dismiss <session-id> --reason "dismissed via wake"`. |
| 4 | Triage quit | `_clast_wake_triage` (`wake.bash:179-267`) has `[q]` Quit, which returns an empty set — the run ends having triaged nothing | `SKILL.md:96-106` triage AskUserQuestion has 4 options (Process all / Yesterday only / Choose days back / Dismiss older, process recent) — no quit-equivalent; the user can only narrow scope, not abort the whole `/wake` run at triage time | **Fix the skill.** Add a `Quit` option to the triage AskUserQuestion that ends `/wake` immediately with nothing processed (mirrors the CLI's `q` branch). |
| 5 | Model-call timing | Prints `  done in Xs (model total Ys)` after each draft (`wake.bash:499-501`) and `  Model time: Xs` in the final summary (`wake.bash:600`) | `SKILL.md:139-158` Step 4 summary template has no timing line at all | **Record as intended** (deviates from the fix-the-skill default; reason below). |
| 6 | Turn-text cap | Caps each turn's text at `turn_cap=2000` chars (`wake.bash:441-452`) before building the user prompt, independent of the turn *count* limit | `SKILL.md:118-120` Step 3.1 only says the `show --full --turns 8` output is "kept compact" — that bounds turn *count* (8), not turn *length*; no per-turn char cap is mentioned | **Fix the skill.** Add an explicit per-turn ~2000-char cap instruction before the turns are used to build the draft prompt. |
| 7 | Session id + recorded date/time | Prints `  id: $sid` and `  recorded: <date> <start>–<end> <tz>` per session (`wake.bash:419-423`) | `SKILL.md:129` preamble only says "at HH:MM" — no session id, no date, no tz | **Fix the skill.** Update the Step 3 preamble to include the session id and the full recorded date + start–end + tz, matching the CLI's line shape. |

### Row 2 — stated reason for "record as intended"

The CLI's per-session menu has no promote options at all; the skill's
richer menu is not lagging behind the CLI, it is a **skill-only capability**
the CLI never had. Fixing the skill "to match the CLI" here would mean
*removing* a feature, not closing lag — the opposite of what BDS-84 is
for. The promote flow only works because the skill *is* the LLM turn: it
can read the transcript and draft a decision/common-issue/workflow body
inline, something a keystroke-driven CLI menu has no analog for (the CLI's
`--auto` path has no interactive reviewer to prompt, and its interactive
path is deliberately a thin accept/edit/dismiss/skip/quit loop, not a
synthesis engine). Keep the skill's promote options as-is; no code change
to that section. Because this is a menu-behavior difference and not a CLI
flag or `CLAST_*` env var, it does not map onto BDS-89's flag/env parity
manifest the way `cli-only` entries (e.g. `undismiss`) do — flag it as an
**open question for step-07** whether the guard needs a parallel
"skill-only" category, rather than forcing an ill-fitting `cli-only` entry.

### Row 5 — stated reason for "record as intended"

The CLI's timing lines measure a real, separately-clocked event: a `curl`
call to an OpenAI-compatible endpoint (`clast_porcelain_llm_chat`), timed
with `clast_porcelain_now`/`clast_porcelain_elapsed` around `wake.bash:476-501`.
The skill has no equivalent call to time — draft generation *is* the
current model turn, not a subprocess invocation the skill's instructions
dispatch and wait on. There is no clock the skill's own text can start/stop
around an LLM call, so "model total Ys" has no meaningful analog to render.
Record as intended; no skill change for this row. Same step-07 caveat as
Row 2 applies if BDS-89's guard tries to assert over summary-line shape
rather than just flags/env vars.

## Decisions (made here, feed later steps)

- The CLI is the reference for every row except #2 and #5, per BRIEF.md's
  ruling; those two keep the skill's current (richer, or architecturally
  inapplicable) behavior with the stated reasons above.
- Rows #2 and #5 do not get a `cli-only`-style allowlist entry in the BDS-89
  parity manifest, because they aren't CLI flags or `CLAST_*` vars — the
  manifest's unit of parity. Step-07 decides whether it needs a "skill-only"
  category to formally record these two; step-03 only flags the gap.
- All fixes land in `skills/wake/SKILL.md` only. `wake.bash` is not touched —
  it is already the correct reference implementation for rows #1, #3, #4,
  #6, #7.
- No prompt content is inlined — every fix stays at the level of skill
  *instructions* (which CLI command to run, what to display), never a copy
  of `lib/clast/prompts/wake-draft-{system,user}.md` content. That pattern is
  already correct in SKILL.md and this step must not regress it (BDS-83's bug).

## Chunks

_Implementation broken into reviewable pieces. Each chunk is small enough to
land in one focused commit each, in dependency order. Chunks 1-5 are
independent fixes (order shown is drift-table order, not a dependency
requirement) touching disjoint sections of SKILL.md; chunks 6-7 are
documentation-only and land last since they just record the two
"intended" rows._

1. **Row 1 — scan window.** In Step 2 (`SKILL.md:51-58`), change the
   `sessions --since` invocation to honor `CLAST_WAKE_SINCE`, defaulting to
   `-14d`:
   ```bash
   $CLAST_BIN --json sessions --since "${CLAST_WAKE_SINCE:--14d}"
   ```
   Update the surrounding prose (currently "Query all recent sessions (last
   30 days)") to say the scan window defaults to 14 days and is
   configurable via `CLAST_WAKE_SINCE`, matching the CLI's `_clast_wake_usage`
   wording. Keep the literal substring `CLAST_BIN --json sessions --since`
   intact (required by `_assert_skill_wake_cli_commands`).

2. **Row 3 — per-session Dismiss.** In the Step 3 promotion AskUserQuestion
   (`SKILL.md:198-214`) add a `Dismiss` option (distinct from `Skip`) and
   describe its handling in "Handle the response" (`SKILL.md:133-137`):
   dismiss pipes to
   `$CLAST_BIN sessions dismiss <session-id> --reason "dismissed via wake"`
   and does not write an entry. Add a `Dismissed: N session(s)` line to the
   Step 4 summary template (`SKILL.md:143-158`), mirroring the CLI's summary
   shape (`wake.bash:593-595`).

3. **Row 4 — triage quit.** In the triage AskUserQuestion options
   (`SKILL.md:100-104`) add a `Quit` option ("end `/wake` now, nothing
   processed") and describe its handling immediately after (mirrors the
   CLI's `q` branch in `_clast_wake_triage`, `wake.bash:259-261`, which
   returns nothing to process).

4. **Row 6 — turn-text cap.** In Step 3.1 (`SKILL.md:116-120`), add an
   explicit instruction: if any turn's text exceeds ~2000 characters,
   truncate it to 2000 chars (noting how many characters were cut) before
   using it to build the draft prompt — mirrors the CLI's `turn_cap=2000`
   (`wake.bash:441`). Keep this as a plain instruction, not a prompt-template
   change — the cap is applied to the *data fed into* `{{first_turns}}` /
   `{{last_turns}}`, not to the template files themselves.

5. **Row 7 — session id + recorded date/time.** In Step 3 item 4
   (`SKILL.md:129`), change the preamble to include the session id and the
   full recorded window, e.g.: "Here's a draft for the X session in
   `<project>` (id: `<session-id>`, recorded: `<date>` `<start>`–`<end>`
   `<tz>`):" — matching the CLI's `id:` / `recorded:` lines
   (`wake.bash:422-423`).

6. **Row 2 — document as intended.** Add one sentence near the promote
   options section (`SKILL.md:198-216`, or its existing v1.1 TODO at
   `SKILL.md:248`) recording that this is a deliberate skill-only capability
   with no CLI menu equivalent, using the reasoning above. No behavior
   change.

7. **Row 5 — document as intended.** Add one sentence near the Step 4
   summary template (`SKILL.md:139-158`) recording that the skill has no
   "model total" timing line because draft generation is the current model
   turn, not a timed subprocess call, using the reasoning above. No
   behavior change.

## Test strategy

- No new automated test is added: every changed row is skill *prose*
  (AskUserQuestion option lists, preamble text, summary template) that
  `test/test-clast.sh`'s skill-asset checks assert on by substring/token
  presence, not by simulating an actual `/wake` run (the skill has no
  executable path the way `wake.bash` does).
- Existing coverage that every chunk must keep green:
  - `_assert_skill_wake_frontmatter` (`test/test-clast.sh:95`) — untouched
    frontmatter, no chunk here edits it.
  - `_assert_skill_wake_triggers` (`test/test-clast.sh:136`) — untouched
    trigger phrases.
  - `_assert_skill_wake_cli_commands` (`test/test-clast.sh:148`) — asserts
    the literal substrings `CLAST_BIN snapshot`, `CLAST_BIN --json sessions
    --since`, `CLAST_BIN --json show`, `CLAST_BIN breadcrumb --read`,
    `CLAST_BIN entries write`, `CLAST_BIN sessions dismiss`. Chunk 1 keeps
    the `--since` substring intact while adding the env-var default; chunk
    2 keeps using the existing `sessions dismiss` invocation (it already
    appears in Step 2a's auto-dismiss text, so this assert already passes
    today — chunk 2 doesn't need to introduce the substring, just reuse it
    in a new context).
  - **`_assert_skill_wake_auto_mode` (`test/test-clast.sh:164`) — must not
    regress.** It requires the literal strings `Auto mode`, `CLAST_WAKE_AUTO_MIN_CHARS`,
    `clast wake --auto` to remain present, and requires the string
    `not a v1 feature` to stay absent. None of chunks 1-7 touch the "Auto
    mode" section (`SKILL.md:160-187`) — verify after each chunk with:
    `grep -n 'Auto mode\|CLAST_WAKE_AUTO_MIN_CHARS\|clast wake --auto\|not a v1 feature' skills/wake/SKILL.md`.
- `make test` and `make lint` must pass after every chunk (per AGENTS.md /
  BRIEF.md constraints); run both before each commit.
- No test asserts against `wake.bash` here since it is not modified by this
  step — `test/test-wake-auto.sh` and any other `wake.bash`-sourcing suite
  stay unaffected by construction.

## Definition of done

- All 7 divergence-table rows have a recorded resolution: 5 fixed in
  `skills/wake/SKILL.md` (chunks 1-5), 2 recorded as intended with stated
  reasons and a step-07 open question flagged (chunks 6-7).
- `skills/wake/SKILL.md` Step 2 honors `CLAST_WAKE_SINCE` (default `-14d`).
- `skills/wake/SKILL.md` Step 3's per-session options include a Dismiss path
  wired to `clast-plumbing sessions dismiss`, distinct from Skip.
- `skills/wake/SKILL.md`'s triage options include a Quit path that ends the
  run with nothing processed.
- `skills/wake/SKILL.md` Step 3.1 documents the ~2000-char per-turn cap.
- `skills/wake/SKILL.md` Step 3's draft preamble includes session id +
  recorded date/start–end/tz.
- No prompt content is inlined into `SKILL.md` anywhere (still reads
  `lib/clast/prompts/wake-draft-{system,user}.md` only).
- `make test` and `make lint` are green, including
  `_assert_skill_wake_auto_mode` unchanged in outcome.
- `lib/clast/clast-porcelain-subcommands/wake.bash` has zero diff (this step
  is skill-only).

## Open questions to resolve during execution

- Does BDS-89's parity guard need a "skill-only" allowlist category
  (mirroring `cli-only` but for the reverse direction — a skill capability
  with no CLI flag/env analog), to formally record rows #2 and #5? Lean:
  raise it explicitly when step-07 starts rather than deciding here — it's
  a guard-design question, not a step-03 blocker, since step-03's job is
  the drift table, not the manifest schema.
- Should the Dismiss and Quit additions (rows #3, #4) get their own
  AskUserQuestion `header` tweaks, or reuse the existing headers ("Session
  draft", "Backlog")? Lean: reuse the existing headers — the row asks for
  behavior parity, not a UX redesign of the question shape.
