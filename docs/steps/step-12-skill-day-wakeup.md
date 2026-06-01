---
step: 12
title: skill-day-wakeup
depends_on: [06, 07, 08, 09, 11]
size: medium
references:
  - docs/overview.md#plugin-surface-cheatsheet
  - docs/skill-prompts.md#skill-1-day-wakeup
  - docs/skill-prompts.md#skillmd-format-reminder
  - docs/cli-contract.md#clast-snapshot
  - docs/cli-contract.md#clast-sessions
  - docs/cli-contract.md#clast-show
  - docs/cli-contract.md#clast-breadcrumb
  - docs/cli-contract.md#clast-entries
  - docs/repo-bootstrap.md#plugin-files
---

# Step 12: `/day-wakeup` skill (cross-project end-of-yesterday curation)

## Context

Step 11 created the plugin substrate (`.claude-plugin/plugin.json`, the `SessionStart` snapshot hook, and an empty `.claude-plugin/skills/` directory marked alive only by `.gitkeep`). Every CLI surface `/day-wakeup` calls into is now real: `clast snapshot` (step 06), `clast sessions --day yesterday --json` (step 07), `clast show <id> --full --turns N --json` (step 07), `clast breadcrumb --read --project <slug> --day yesterday` (step 09), and `clast entries write --body-stdin` (step 08). Step 09's `breadcrumb` plan is merged but its implementation may or may not have landed by the time this step runs — the dependency on step 09 here is on the merged *implementation*; the executing agent must verify `clast breadcrumb --read` works against the test journal before generating the skill, not just that the plan file exists.

This step is the first of two skill files (the second is `/wakeup` in step 13). `/day-wakeup` is the **once-per-day curation flow**: snapshot, walk yesterday's uncurated sessions across all projects, generate a draft entry per session via the in-context LLM, present an `AskUserQuestion` for accept/edit/skip/promote, and write accepted entries back via the CLI. It is the only place the journal grows curated content under user supervision; everything else (`/wakeup`, the hook) either reads or auto-captures.

The skill is a **thin LLM layer over the CLI** (`docs/skill-prompts.md` intro). It does not read or write `~/.claude/journal/` directly — every read goes through a `clast` subcommand and every write goes through `clast entries write` on stdin. This invariant is what makes the skill safe to ship: the CLI is the single source of truth for journal mutations, and the skill is constrained to its public surface.

**Run `direnv allow` (or `nix develop`) before starting** so `jq` is available for the smoke-test step at the end.

## Goal

Create exactly one new file — `.claude-plugin/skills/day-wakeup/SKILL.md` — containing the YAML frontmatter + body specified in `docs/skill-prompts.md#skill-1-day-wakeup`; remove `.claude-plugin/skills/.gitkeep` now that the directory has real content; verify the file parses as valid YAML-frontmatter Markdown; and do one manual smoke invocation against a populated test journal to confirm every CLI command the skill names actually runs (no behavior assertion on the LLM output — that is the user's review job, not a CI surface).

## References

Read before starting:

- `docs/overview.md#plugin-surface-cheatsheet` — `/day-wakeup` is "the one curation point"; its complementarity with `/wakeup` (write vs read) is load-bearing.
- `docs/skill-prompts.md#skillmd-format-reminder` — YAML frontmatter shape (`name`, `description`), description content drives Claude's auto-trigger, body becomes in-context instructions when the skill fires.
- `docs/skill-prompts.md#skill-1-day-wakeup` — **canonical content for the SKILL.md.** Use the markdown body block from "### `SKILL.md` content" verbatim except where this step calls out a deviation. Read this section in full, including the "Draft generation prompt — design notes" trailer (not for the SKILL.md itself but useful background).
- `docs/cli-contract.md#clast-snapshot` — silent on no-op, idempotent, what error paths look like (`/day-wakeup`'s "errors here are non-fatal" semantics).
- `docs/cli-contract.md#clast-sessions` — `--day yesterday`, `--json` shape (each row has `curated: bool`), `--project SLUG` filter.
- `docs/cli-contract.md#clast-show` — `--full`, `--turns N`, `--json` shape; transcripts are text-only (no tool calls) in the trimmed form.
- `docs/cli-contract.md#clast-breadcrumb` — `--read --project <slug> --day yesterday` reads the prior day's breadcrumb file; missing file is exit 0 with empty body (not an error).
- `docs/cli-contract.md#clast-entries` — `--body-stdin` flag, `--session`, `--slug`, `--tags`, `--title`, what the slug-collision suffixing rule is, what stdin EOF means.
- `docs/repo-bootstrap.md#plugin-files` — confirms `.claude-plugin/skills/<name>/SKILL.md` is the canonical path; do not invent alternatives.

## Tasks

1. **Create the skill directory.** `mkdir -p .claude-plugin/skills/day-wakeup`. Do NOT create `.claude-plugin/skills/wakeup/` or `.claude-plugin/skills/breadcrumb/` — those are step 13 and v1.1 respectively, and adding empty directories now creates `.gitkeep` noise that the next steps would have to clean up.

2. **Write `.claude-plugin/skills/day-wakeup/SKILL.md`.** The file shape (canonical reference: `docs/skill-prompts.md#skill-1-day-wakeup`):
   - **YAML frontmatter** between `---` fences. Exactly two keys, in this order: `name: day-wakeup`, then `description: <full description text>`. The description is the long-form auto-trigger string from the canonical content — every trigger phrase listed there (`/day-wakeup`, `day wakeup`, `morning briefing`, `catch me up on yesterday`, `what did I work on yesterday`, `review my day`, `process yesterday's sessions`) MUST appear verbatim. The description also explicitly says "Runs `clast snapshot`…" and "This is the once-per-day curation flow; for per-project briefings use /wakeup; for mid-session pivots use session-brief." — keep both byte-identical to the canonical content. **Quote the entire description value** so YAML does not choke on the apostrophes, slashes, and quoted skill names inside it — single quotes around the whole string is the safest form; if the description itself contains a single quote (`don't`, `yesterday's`), use the YAML doubled-single-quote escape (`''`) rather than switching to double quotes (which would force escaping the slashes and the embedded backticks). The frontmatter is NOT optional — the plugin loader rejects skill files without it.
   - **Body** under a single `# Day Wakeup` h1, following the section structure from the canonical content: `## Why this exists`, `## Step 1: Ensure fresh data`, `## Step 2: Enumerate uncurated sessions from yesterday`, `## Step 3: For each session, generate a draft`, `## Step 4: Final summary`, `## Draft generation prompt`, `## AskUserQuestion: promotion options per session`, `## Editing handler`, `## Writing the entry`, `## Edge cases`. **Use the canonical body byte-identical** except for the deviations listed in task 3.

3. **Deviations from the canonical body (apply during the verbatim copy):**
   - **TODO marker for promoted-item subcommands.** The "Writing the entry" section ends with a paragraph noting that `clast decisions write` / `clast common-issues write` / `clast workflows write` are v1.1 work and that promoted items are folded into the entry body for v1. Keep that paragraph verbatim. The user-facing summary in "Step 4: Final summary" already mentions promotion counts; that's correct — do not remove it. The v1 implementation: when an "Accept + promote …" option is chosen, the skill must append a clearly-labeled promotion section to the entry body BEFORE the `clast entries write --body-stdin` invocation. Add one short sentence to "Writing the entry" specifying the section header convention: `## Decision`, `## Common issue`, or `## Workflow` (h2, singular, capitalized), each followed by the prompted-for title (h3) and body. This sentence is the only addition this step makes beyond the canonical content; flag it inline with an HTML comment `<!-- step-12 addition: v1 promotion section convention -->` so a future docs sync can lift it back into `skill-prompts.md` if desired.
   - **No other content changes.** The "Draft generation prompt" code block (the long fenced markdown template) and the `AskUserQuestion` option list are load-bearing — copy them character-for-character. The nested triple-backtick fences inside the draft-prompt block (which itself sits inside another triple-backtick block in the source doc) MUST be preserved; the outer SKILL.md body uses indented code fences or quadruple-backtick fences as needed so the inner triple-backtick block round-trips through Markdown renderers without breaking. Verify by running the file through `pandoc -f markdown -t html` (or any commonmark renderer available in the dev shell) and confirming the inner fenced block is rendered as a code block, not as the surrounding prose.

4. **Remove `.claude-plugin/skills/.gitkeep`.** The directory now has real content (`day-wakeup/SKILL.md`). The `.gitkeep` was retained by step 11 specifically because step 11 left the directory empty; this step displaces it. Step 13 will add `wakeup/SKILL.md` to the same parent directory and does NOT need to recreate the `.gitkeep`.

5. **Add a SKILL.md lint assertion to `test/test-clast.sh`.** Extend the plugin-assets block from step 11 (the `_assert_plugin_assets` helper) with two new assertions for `day-wakeup/SKILL.md`:
   - **Frontmatter parses.** Use a short awk one-liner to extract the lines between the first two `---` fences, pipe to `python3 -c 'import sys,yaml; yaml.safe_load(sys.stdin)'` if Python+yaml are available, OR use a portable bash check: confirm the file begins with `---\n`, that a second `---\n` appears within the first 50 lines, that the slice contains a `name: day-wakeup` line, and that it contains a `description:` line of length > 100 (the auto-trigger string is long; a short description means somebody truncated it). The portable bash check is fine — we are guarding against shape regressions, not validating YAML semantics.
   - **Trigger phrases present.** `grep -q '/day-wakeup'`, `grep -q 'morning briefing'`, and `grep -q "catch me up on yesterday"` against the file. If any returns non-zero, the assertion fails with a clear message naming which trigger went missing. This guards against a future content edit that accidentally deletes the auto-trigger surface.
   - **CLI commands the skill names actually dispatch.** `grep -q 'clast snapshot'`, `grep -q 'clast sessions --day yesterday'`, `grep -q 'clast show'`, `grep -q 'clast breadcrumb --read'`, `grep -q 'clast entries write'` against the file. This is a static check — it catches the case where someone refactors the skill but forgets to update a command path.

   These assertions live in `test-clast.sh` (next to step 11's plugin-asset block), not in a dedicated `test-skills.sh`. There is no behavior to integration-test — the skill body is LLM-driven prose, not executable code.

6. **Smoke-test the skill manually against a populated test journal.** Steps:
   - Build a populated test journal under a `mktemp -d` directory. Reuse `test/fixtures/multi-project/projects-tree` for the source and `test/fixtures/multi-project/journal-seed/projects.json` for the registry (added in steps 06/07).
   - Pre-seed at least one session timestamped "yesterday" (use the same `CLAST_NOW_EPOCH` / `CLAST_DAY_CUTOFF` hooks the test suites use; advance the clock so "yesterday" is a known fixed date).
   - Run each CLI command the SKILL.md names, in the order the skill names them, against this journal. Confirm each exits 0 and produces non-empty output (where the skill expects output) or empty-on-no-op (where the skill expects silence). This is NOT a SKILL.md execution — it is a CLI plumbing check that the commands the skill prose names still exist with the documented flags.
   - This smoke-test is a one-time human-driven check; do NOT bake it into `make test` (the fixture+clock dance is too brittle for CI). Record the exact commands and outputs in the PR description so a reviewer can replay them.
   - **Do NOT** attempt to run the skill end-to-end inside Claude Code as part of this step's acceptance. The plugin loader, the auto-trigger heuristic, and the `AskUserQuestion` rendering are all moving parts that belong to a separate manual QA pass, not to a step that ships a markdown file.

7. **Update `README.md`** with a short paragraph for `/day-wakeup` (3–8 lines). Two pieces: (a) what it does ("once-per-day cross-project curation of yesterday's sessions into journal entries"); (b) how to invoke it (`/day-wakeup` after the plugin is installed). Link to `docs/skill-prompts.md#skill-1-day-wakeup` for the full prompt body and design notes. Do NOT document `/wakeup` here — that's step 13's README touch.

8. **Confirm `make lint` and `make test` pass.** `make lint` is unchanged from step 11 (no new shell scripts shipped here). `make test` runs the extended plugin-assets assertions (task 5) and exits 0.

## Acceptance criteria

- `.claude-plugin/skills/day-wakeup/SKILL.md` exists and begins with a YAML frontmatter block (`---` … `---`) containing exactly two keys: `name: day-wakeup` and a long `description` field.
- The `description` includes every canonical trigger phrase listed in `docs/skill-prompts.md#skill-1-day-wakeup`: `/day-wakeup`, `day wakeup`, `morning briefing`, `catch me up on yesterday`, `what did I work on yesterday`, `review my day`, `process yesterday's sessions`.
- The body covers all canonical section headers in order: `## Why this exists`, `## Step 1: Ensure fresh data`, `## Step 2: Enumerate uncurated sessions from yesterday`, `## Step 3: For each session, generate a draft`, `## Step 4: Final summary`, `## Draft generation prompt`, `## AskUserQuestion: promotion options per session`, `## Editing handler`, `## Writing the entry`, `## Edge cases`.
- The body names every CLI command the skill calls: `clast snapshot`, `clast sessions --day yesterday --json`, `clast show <session-id> --full --turns 8 --json`, `clast breadcrumb --read --project <slug> --day yesterday`, `clast entries write … --body-stdin`.
- The "Writing the entry" section names the v1 in-entry promotion section convention (`## Decision`, `## Common issue`, `## Workflow`) with the inline `<!-- step-12 addition -->` HTML comment marker.
- `.claude-plugin/skills/.gitkeep` is removed.
- `test/test-clast.sh` extends the step-11 `_assert_plugin_assets` block with frontmatter-shape, trigger-phrase, and CLI-command assertions for `day-wakeup/SKILL.md`; the assertions fail loudly with a named-component message on regression.
- A manual smoke test, recorded in the PR description, confirms every CLI command the SKILL.md names dispatches against a populated `multi-project` fixture journal.
- `README.md` has a 3–8 line `/day-wakeup` paragraph; no mention of `/wakeup` yet.
- `make lint` and `make test` both exit 0.

## Out of scope

- **Do not write `wakeup/SKILL.md` or `breadcrumb/SKILL.md`.** `/wakeup` is step 13; `/breadcrumb` is v1.1 (and may be skipped entirely — see `docs/skill-prompts.md#skill-3-breadcrumb-optional-v11`).
- **Do not add a CLI integration test that drives the skill end-to-end.** SKILL.md is LLM-facing prose; the closest thing to a useful test is the static grep-for-commands assertion in task 5. End-to-end skill behavior is verified by a human in a real Claude Code session at QA time, not by CI.
- **Do not implement `clast decisions write` / `clast common-issues write` / `clast workflows write`.** Those are v1.1 work, explicitly carved out in `docs/skill-prompts.md#writing-the-entry`. The v1 promotion mechanism is the in-entry section convention specified in task 3.
- **Do not change any CLI subcommand to add new flags the skill needs.** If the skill calls a flag, it must already exist (steps 06–10 produced them). If something is missing, stop and ask — do not retrofit.
- **Do not edit `docs/skill-prompts.md`.** The canonical content lives there; this step copies it into `SKILL.md` with one small addition (task 3). A future docs-sync step (likely step 18) can lift the addition back into the reference doc if desired.
- **Do not add `--day` parsing variants** (`/day-wakeup last-week`) — `docs/skill-prompts.md#open-questions-about-skills` lists this as a future ergonomic. Default is "yesterday" only.
- **Do not change `bin/clast`, `lib/clast/`, or any existing test suite's behavior.** The only allowed edits outside the new file are `test/test-clast.sh` (task 5) and `README.md` (task 7).
- **Do not modify the hook script** from step 11. `/day-wakeup` runs `clast snapshot` itself for freshness; the hook is the always-on path. They are intentional duplicates with different cadences.
- **Do not run a real `/day-wakeup` against the user's real `~/.claude/journal/`** as part of acceptance. The smoke test uses a `mktemp` journal so accidental write paths are isolated.

## Verification

```bash
# Lint
make lint

# Tests (includes the new SKILL.md assertions)
make test

# YAML frontmatter shape
awk '/^---$/{n++; next} n==1{print}' .claude-plugin/skills/day-wakeup/SKILL.md | head -20

# Trigger phrases all present
for p in '/day-wakeup' 'morning briefing' "catch me up on yesterday" 'process yesterday'; do
  grep -q "$p" .claude-plugin/skills/day-wakeup/SKILL.md && echo "ok: $p" || echo "MISSING: $p"
done

# CLI commands the skill names
for c in 'clast snapshot' 'clast sessions --day yesterday' 'clast show' 'clast breadcrumb --read' 'clast entries write'; do
  grep -q "$c" .claude-plugin/skills/day-wakeup/SKILL.md && echo "ok: $c" || echo "MISSING: $c"
done

# Manual smoke against a populated fixture journal (record output in PR description).
# Uses the multi-project fixture and a pinned "yesterday".
export TZ=UTC
export CLAST_JOURNAL_DIR="$(mktemp -d)"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/multi-project/projects-tree"
cp test/fixtures/multi-project/journal-seed/projects.json "$CLAST_JOURNAL_DIR/projects.json"
export CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T09:00:00Z' +%s)   # "today" is 2026-05-30 → yesterday is 2026-05-29

bin/clast snapshot
bin/clast --json sessions --day yesterday | jq '.[] | {session_id, project, curated}'
# pick a session id from the previous output and run:
# bin/clast --json show <session-id> --full --turns 8 | jq 'keys'
# bin/clast breadcrumb --read --project xesapps --day yesterday
# echo -e "# Test\n\nbody" | bin/clast entries write --session <session-id> --slug test --tags smoke --title "Smoke" --body-stdin

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR CLAST_NOW_EPOCH TZ
```

## Notes for the implementer

- **The description string is the auto-trigger surface.** Claude Code's heuristic matches a user prompt against the `description:` field. Every phrase the user might naturally say to mean "do the morning curation thing" should appear in it. The canonical phrase list is in `skill-prompts.md`; copy it verbatim and resist the urge to "tidy" it. A trimmed description = a skill that doesn't auto-trigger.
- **Frontmatter quoting is fiddly.** The description contains slashes, apostrophes, and embedded backtick-quoted skill names. Single-quoted YAML with `''` escapes for inner single quotes is the most robust form; double-quoted YAML would force backslash escapes for the embedded quoted strings and read worse. If something refuses to parse, default to the YAML block-scalar literal form (`description: |` followed by an indented block), which sidesteps quoting entirely.
- **The draft-prompt block has nested code fences.** The canonical content uses a `\`\`\`markdown` outer fence around a block that itself contains a `\`\`\`` fence. When copying into SKILL.md, use quadruple-backtick fences for the outer wrapper (commonmark allows any run of ≥3 backticks as long as the close matches the open) so the inner triple-backtick block round-trips cleanly. Verify by rendering with `pandoc` or any commonmark tool before committing.
- **`AskUserQuestion` rendering is the LLM's job, not the skill author's.** The SKILL.md describes the *options* and the *handler logic*; the in-context LLM, when the skill fires, actually invokes the `AskUserQuestion` tool. Don't try to encode an `AskUserQuestion` call as code in the SKILL.md — it's prose instructions for the LLM, not a script.
- **`clast snapshot` failure is non-fatal.** The skill explicitly says "errors here are non-fatal — proceed even if it fails, just warn the user." The CLI exits non-zero on real failures; the LLM body of the skill is what owns the "warn and proceed" semantics. Keep that wording verbatim.
- **Why the dependency on step 09.** The skill calls `clast breadcrumb --read --project <slug> --day yesterday`. If step 09's implementation has not merged, the skill body still ships correctly (it's prose), but the task-6 smoke test will fail on the breadcrumb-read invocation. Verify step 09 is merged before running the smoke; if it isn't, stop and ask — do not skip the smoke or stub the breadcrumb read out of the SKILL.md.
- **Conventional commit suggestion**: `feat(skills): add /day-wakeup curation skill`. One commit is fine; the README touch is small enough to fold in.
