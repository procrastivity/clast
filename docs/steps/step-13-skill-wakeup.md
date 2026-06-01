---
step: 13
title: skill-wakeup
depends_on: [05, 07, 08, 09, 11, 12]
size: small
references:
  - docs/overview.md#plugin-surface-cheatsheet
  - docs/skill-prompts.md#skill-2-wakeup
  - docs/skill-prompts.md#skillmd-format-reminder
  - docs/cli-contract.md#clast-registry
  - docs/cli-contract.md#clast-entries
  - docs/cli-contract.md#clast-breadcrumb
  - docs/cli-contract.md#clast-sessions
  - docs/repo-bootstrap.md#plugin-files
---

# Step 13: `/wakeup` skill (per-project read-only briefing)

## Context

Step 11 created the plugin substrate; step 12 added `.claude-plugin/skills/day-wakeup/SKILL.md` (the curate-yesterday-across-projects flow) and ate the `.gitkeep` under `.claude-plugin/skills/`. This step adds the second of the two v1 skills: `/wakeup`, the per-project read-only briefing the user invokes when starting work on a specific repo today.

`/wakeup` and `/day-wakeup` are complementary, not redundant: `/day-wakeup` writes (curates yesterday's sessions into entries); `/wakeup` reads (synthesizes a 2–5k-token briefing for one project from its recent entries + today's breadcrumbs + any sessions already started today). This step ships only `/wakeup`. The third skill, `/breadcrumb`, is explicitly v1.1 (see `docs/skill-prompts.md#skill-3-breadcrumb-optional-v11`) and is NOT in scope here.

Every CLI surface `/wakeup` calls into is real: `clast registry resolve <path>` (step 05), `clast entries --project <slug> --limit 5 --json` and `clast entries read <entry-path>` (step 08), `clast breadcrumb --read --project <slug> --day today` (step 09), `clast sessions --day today --project <slug> --json` (step 07). The dependency on step 09 is again on the merged *implementation*, not just the merged plan — verify before the smoke test.

The skill is **strictly read-only**. It never invokes `clast entries write`, never invokes `clast breadcrumb` in write mode, never invokes `clast snapshot`. This is the load-bearing invariant — `/wakeup` is the "what was I doing" command, and it's safe to run on autopilot precisely because it cannot mutate the journal.

**Run `direnv allow` (or `nix develop`) before starting** so `jq` is available for the smoke-test step.

## Goal

Create exactly one new file — `.claude-plugin/skills/wakeup/SKILL.md` — containing the YAML frontmatter + body specified in `docs/skill-prompts.md#skill-2-wakeup`; extend the `test/test-clast.sh` plugin-assets block to assert the new SKILL.md's frontmatter shape, trigger phrases, CLI commands, and **read-only invariant** (no `clast entries write` / no `clast breadcrumb '…'` / no `clast snapshot` in the body); do one manual smoke against a populated test journal; and add a short `/wakeup` paragraph to `README.md`.

## References

Read before starting:

- `docs/overview.md#plugin-surface-cheatsheet` — `/wakeup` is "per-project briefing synthesized from recent entries + today's breadcrumbs. Fast, read-only."
- `docs/skill-prompts.md#skillmd-format-reminder` — frontmatter shape; description drives auto-trigger.
- `docs/skill-prompts.md#skill-2-wakeup` — **canonical content for the SKILL.md.** Use the markdown body block from "### `SKILL.md` content" verbatim except where this step calls out a deviation. Read the "Synthesis prompt — internal" trailer too — it goes into the SKILL.md as the "Step 3" prompt template.
- `docs/cli-contract.md#clast-registry` — `clast registry resolve <path-or-segment>` shape; what happens on a miss (`/wakeup`'s "not in a registered project" handling).
- `docs/cli-contract.md#clast-entries` — `--project <slug> --limit 5 --json` filter shape; the body of `entries read <path>` is the entry's markdown including frontmatter.
- `docs/cli-contract.md#clast-breadcrumb` — `--read --project <slug> --day today` (read-only mode); missing file is exit 0 with empty body.
- `docs/cli-contract.md#clast-sessions` — `--day today --project <slug> --json` shape; may be empty if the user has not yet started a session today.
- `docs/repo-bootstrap.md#plugin-files` — `.claude-plugin/skills/<name>/SKILL.md` is canonical.

## Tasks

1. **Create the skill directory.** `mkdir -p .claude-plugin/skills/wakeup`. Do NOT touch `.claude-plugin/skills/day-wakeup/` (step 12's territory) and do NOT create `.claude-plugin/skills/breadcrumb/` (v1.1).

2. **Write `.claude-plugin/skills/wakeup/SKILL.md`.** Canonical reference: `docs/skill-prompts.md#skill-2-wakeup`. File shape:
   - **YAML frontmatter** between `---` fences. Exactly two keys, in this order: `name: wakeup`, then `description: <full description text>`. The description is the long-form auto-trigger string from the canonical content — every trigger phrase listed there (`/wakeup`, `wakeup`, `wake up`, `catch me up`, `where was I`, `what was I working on`, `load last session`, `resume`) MUST appear verbatim. The description also explicitly says "Optionally accepts a project slug like '/wakeup xesapps'" and "This is the per-project read flow; for cross-project daily curation use /day-wakeup; for mid-session pivots use session-brief." — keep both byte-identical. Apply the same single-quote-with-`''`-escape YAML quoting strategy step 12 used; fall back to a `description: |` block scalar if the inline form fights you.
   - **Body** under a single `# Wakeup` h1, following the section structure from the canonical content: `## Why this exists`, `## Step 1: Resolve the project`, `## Step 2: Gather data`, `## Step 3: Synthesize the briefing`, `## Step 4: Don't write anything`, `## Edge cases`. Plus a final section `## Synthesis prompt` containing the internal prompt template from the "Synthesis prompt — internal" trailer of the canonical doc. **Use the canonical body byte-identical** except for the small adjustments in task 3.

3. **Deviations from the canonical body (apply during the verbatim copy):**
   - **Promote the "Synthesis prompt — internal" trailer into the SKILL.md as a real section.** In `docs/skill-prompts.md#skill-2-wakeup` the synthesis prompt sits OUTSIDE the SKILL.md fenced block as design commentary. The actual SKILL.md needs the prompt in-band (it's what the LLM uses to produce the briefing). Add a `## Synthesis prompt` h2 at the end of the body and inline the prompt template verbatim. Where the canonical trailer says `[same structure as in SKILL.md step 3]`, replace that placeholder with an explicit re-statement of the Step-3 briefing structure (a short bulleted shape — do not duplicate the full prose narrative, just the section list: `Active thread`, `Last session`, `Recent sessions`, `Today's breadcrumbs`, `Today's sessions`, `Suggested next step`). This is the only non-trivial content change; flag it inline with `<!-- step-13 addition: inlined synthesis prompt with explicit structure re-statement -->`.
   - **No other content changes.** The "Don't write anything" section is load-bearing — copy it character-for-character. The "Edge cases" bullet list is the contract for empty-project / no-entries / heavy-day-truncation behavior; do not paraphrase.

4. **Extend `test/test-clast.sh`'s plugin-assets block** with assertions for `wakeup/SKILL.md`:
   - **Frontmatter shape.** Same portable bash check as step 12: starts with `---\n`, second `---\n` within first 50 lines, contains `name: wakeup`, contains `description:` of length > 100.
   - **Trigger phrases present.** `grep -q '/wakeup'`, `grep -q 'wake up'`, `grep -q 'where was I'`, `grep -q 'resume'`. Named-component error message on regression.
   - **CLI commands the skill names actually dispatch.** `grep -q 'clast registry resolve'`, `grep -q 'clast entries --project'`, `grep -q 'clast entries read'`, `grep -q 'clast breadcrumb --read'`, `grep -q 'clast sessions --day today'`. (Note: `clast entries read` is the per-entry read form, distinct from the list form `clast entries --project … --limit 5 --json`.)
   - **Read-only invariant.** This is the new shape of assertion for this skill — the previous skill ships writes, so the invariant only matters here. Three `grep` checks that MUST return non-zero (i.e. the assertion *fails* if any matches):
     - `grep -E 'clast entries write' .claude-plugin/skills/wakeup/SKILL.md` → must not match.
     - `grep -E "clast breadcrumb [^-]" .claude-plugin/skills/wakeup/SKILL.md` → must not match. (Matches `clast breadcrumb 'text'` write form but NOT `clast breadcrumb --read` / `--list`.)
     - `grep -E 'clast snapshot' .claude-plugin/skills/wakeup/SKILL.md` → must not match. `/wakeup` is read-only; `clast snapshot` is `/day-wakeup`'s territory.
     Wrap these in a clear `_assert_skill_wakeup_readonly` helper that names which forbidden command was found if any. The negative-grep semantics matter: a future content edit that "helpfully" adds a `clast snapshot` to freshen data before reading would silently turn `/wakeup` from read-only to read-write — the test must catch that.

5. **Do NOT remove any `.gitkeep`.** Step 12 already removed `.claude-plugin/skills/.gitkeep`; this step has nothing to clean up there. There is no `.gitkeep` under `.claude-plugin/skills/wakeup/` to begin with (the directory is created by task 1 and immediately populated by task 2).

6. **Smoke-test the skill manually against a populated test journal.** Steps:
   - Build a populated test journal under a `mktemp -d` directory, same shape as step 12's smoke: `multi-project/projects-tree` for the source, `multi-project/journal-seed/projects.json` for the registry. Pin `CLAST_NOW_EPOCH` so "today" is a known fixed date.
   - Pre-seed at least one curated entry under the journal's `entries/` directory for one project (use a hand-composed file matching the `entries/YYYY-MM-DD-HHMM-<slug>-<session-slug>.md` shape from `docs/overview.md#filesystem-reference`; or run a `clast entries write` once to produce one and reuse).
   - Pre-seed at least one breadcrumb under `breadcrumbs/YYYY-MM-DD-<slug>.md` for "today" so the synthesis has something to surface.
   - Run each CLI command the SKILL.md names, in the order the skill names them, with `pwd` set to a directory the registry resolves (i.e. `cd` into the fixture's source tree). Confirm each exits 0 and produces sensible output (`registry resolve` prints the slug, `entries --json` returns an array, `entries read <path>` returns the markdown body, `breadcrumb --read` returns the breadcrumb body, `sessions --day today --project <slug> --json` returns `[]` or an array).
   - Record commands + outputs in the PR description so a reviewer can replay.
   - Do NOT bake into `make test`; do NOT run a real `/wakeup` end-to-end in Claude Code as part of acceptance.

7. **Update `README.md`** with a short `/wakeup` paragraph (3–8 lines) immediately after step 12's `/day-wakeup` paragraph. Two pieces: (a) what it does ("per-project read-only briefing synthesized from recent entries + today's breadcrumbs + any sessions started today"); (b) how to invoke it (`/wakeup` from inside a registered project's directory, or `/wakeup <slug>` from anywhere). Link to `docs/skill-prompts.md#skill-2-wakeup`. Mention the read-only invariant explicitly: "`/wakeup` never writes — it only reads."

8. **Confirm `make lint` and `make test` pass.** No new shell scripts ship in this step; `make lint` is unchanged from step 11. `make test` runs the extended plugin-assets assertions (task 4) and exits 0.

## Acceptance criteria

- `.claude-plugin/skills/wakeup/SKILL.md` exists and begins with a YAML frontmatter block (`---` … `---`) containing exactly two keys: `name: wakeup` and a long `description` field.
- The `description` includes every canonical trigger phrase listed in `docs/skill-prompts.md#skill-2-wakeup`: `/wakeup`, `wakeup`, `wake up`, `catch me up`, `where was I`, `what was I working on`, `load last session`, `resume`. The description also mentions the optional `/wakeup <slug>` form.
- The body covers all canonical section headers in order: `## Why this exists`, `## Step 1: Resolve the project`, `## Step 2: Gather data`, `## Step 3: Synthesize the briefing`, `## Step 4: Don't write anything`, `## Edge cases`, plus the inlined `## Synthesis prompt` section with explicit briefing-structure re-statement and the `<!-- step-13 addition -->` marker.
- The body names every CLI command the skill calls: `clast registry resolve`, `clast entries --project <slug> --limit 5 --json`, `clast entries read <entry-path>`, `clast breadcrumb --read --project <slug> --day today`, `clast sessions --day today --project <slug> --json`.
- The body does NOT contain any write-form CLI invocation: no `clast entries write`, no `clast breadcrumb '<text>'` (write form), no `clast snapshot`.
- `test/test-clast.sh` extends the plugin-assets block with frontmatter-shape, trigger-phrase, CLI-command-present, AND read-only-invariant (negative-grep) assertions for `wakeup/SKILL.md`; all fail loudly with named-component messages on regression.
- A manual smoke test, recorded in the PR description, confirms every CLI command the SKILL.md names dispatches against a populated `multi-project` fixture journal.
- `README.md` has a 3–8 line `/wakeup` paragraph that explicitly states the read-only invariant.
- `make lint` and `make test` both exit 0.

## Out of scope

- **Do not write `breadcrumb/SKILL.md`.** `/breadcrumb` is v1.1 (`docs/skill-prompts.md#skill-3-breadcrumb-optional-v11`); may be skipped entirely. Do NOT create `.claude-plugin/skills/breadcrumb/`.
- **Do not change `day-wakeup/SKILL.md`** from step 12. If you find a bug in it, fix it in a separate commit/PR; this step is `/wakeup`-only.
- **Do not implement a `clast briefing` CLI command** (the file-output alternative listed in `docs/skill-prompts.md#open-questions-about-skills`). That's a v1.1 ergonomic; the v1 surface is "skill prints briefing to chat."
- **Do not add a `--day` argument to `/wakeup`.** The canonical content does not parameterize the day window; `/wakeup` is always "today + recent." Multi-day windows are a future ergonomic.
- **Do not add a CLI integration test that drives the skill end-to-end.** Same reasoning as step 12 — static grep assertions + manual smoke are the verification surface; live skill behavior is a human QA task.
- **Do not change any CLI subcommand to add new flags the skill needs.** Every flag the skill calls must already exist (steps 05–10 produced them). If something is missing, stop and ask — do not retrofit.
- **Do not edit `docs/skill-prompts.md`.** Same as step 12: canonical content lives there; this step copies it. A future docs-sync pass can lift the synthesis-prompt re-inlining back into the reference doc if desired.
- **Do not change `bin/clast`, `lib/clast/`, or any existing test suite's behavior.** Only allowed edits outside the new file are `test/test-clast.sh` (task 4) and `README.md` (task 7).
- **Do not weaken the read-only invariant** to accommodate a future "freshen data first" suggestion. If the user wants fresh snapshots, they run `/day-wakeup` (which calls `clast snapshot`) or the `SessionStart` hook (step 11) catches it. `/wakeup` stays pure-read.
- **Do not run `/wakeup` against the user's real `~/.claude/journal/`** as part of acceptance. The smoke test uses a `mktemp` journal.

## Verification

```bash
# Lint
make lint

# Tests (includes the new SKILL.md assertions + read-only invariant)
make test

# YAML frontmatter shape
awk '/^---$/{n++; next} n==1{print}' .claude-plugin/skills/wakeup/SKILL.md | head -20

# Trigger phrases all present
for p in '/wakeup' 'wake up' 'where was I' 'resume'; do
  grep -q "$p" .claude-plugin/skills/wakeup/SKILL.md && echo "ok: $p" || echo "MISSING: $p"
done

# CLI commands the skill names (all positive — must match)
for c in 'clast registry resolve' 'clast entries --project' 'clast entries read' 'clast breadcrumb --read' 'clast sessions --day today'; do
  grep -q "$c" .claude-plugin/skills/wakeup/SKILL.md && echo "ok: $c" || echo "MISSING: $c"
done

# Read-only invariant (all negative — MUST NOT match)
for c in 'clast entries write' "clast breadcrumb '" 'clast snapshot'; do
  if grep -q "$c" .claude-plugin/skills/wakeup/SKILL.md; then
    echo "INVARIANT VIOLATION: found '$c' in read-only skill"
  else
    echo "ok (absent): $c"
  fi
done

# Manual smoke (record in PR description).
export TZ=UTC
export CLAST_JOURNAL_DIR="$(mktemp -d)"
export CLAST_PROJECTS_DIR="$PWD/test/fixtures/multi-project/projects-tree"
cp test/fixtures/multi-project/journal-seed/projects.json "$CLAST_JOURNAL_DIR/projects.json"
export CLAST_NOW_EPOCH=$(date -u -d '2026-05-30T09:00:00Z' +%s)

# Pre-seed: one curated entry + one breadcrumb for xesapps today.
mkdir -p "$CLAST_JOURNAL_DIR/entries" "$CLAST_JOURNAL_DIR/breadcrumbs"
# (hand-compose or use clast entries write / clast breadcrumb to produce these)

pushd "$CLAST_PROJECTS_DIR/-home-beau-code-xesapps" >/dev/null
bin/clast registry resolve "$(pwd)"                                   # → xesapps
bin/clast --json entries --project xesapps --limit 5 | jq 'length'
# bin/clast entries read <path-from-above>
bin/clast breadcrumb --read --project xesapps --day today
bin/clast --json sessions --day today --project xesapps | jq 'length'
popd >/dev/null

rm -rf "$CLAST_JOURNAL_DIR"
unset CLAST_JOURNAL_DIR CLAST_PROJECTS_DIR CLAST_NOW_EPOCH TZ
```

## Notes for the implementer

- **Read-only is the load-bearing invariant.** Everything else about `/wakeup` is taste; the read-only guarantee is what makes it safe to auto-trigger on any `/wakeup`-ish phrase. The negative-grep assertions in task 4 are not pedantry — they are the only mechanism preventing a future "helpful" edit from silently turning the skill into a read-write surface.
- **`/wakeup` and `/day-wakeup` are not interchangeable.** The auto-trigger heuristic uses the descriptions; both descriptions explicitly cross-reference each other ("for cross-project daily curation use /day-wakeup"; "for per-project briefings use /wakeup") so Claude can route correctly. Preserve those cross-references when copying the canonical content.
- **`pwd` resolution is the default; the `<slug>` argument is the override.** The skill's Step 1 establishes this ordering — try the argument first if given, else `clast registry resolve "$(pwd)"`. Keep that order in the copied body; flipping it would surprise users who run `/wakeup xesapps` from inside an unrelated checkout.
- **No `clast snapshot` in `/wakeup`.** This is a deliberate non-feature. The hook covers the snapshot path. If a user invokes `/wakeup` and the journal is stale, they'll see slightly old data — that's fine; freshness is `/day-wakeup`'s job. Wiring snapshot into `/wakeup` would make every "where was I" prompt block on disk I/O for no human-perceptible improvement.
- **Synthesis prompt inlining vs reference-by-link.** The canonical doc presents the synthesis prompt as design commentary because the doc is for humans. The actual SKILL.md needs the prompt in-band — when the skill fires, the LLM has the body of the SKILL.md in context and uses the prompt directly. A SKILL.md that says "see `docs/skill-prompts.md` for the prompt" would never reach the doc. Inline it.
- **Why the dependency on step 09.** Same as step 12: the skill calls `clast breadcrumb --read`. If step 09's implementation has not merged, the smoke fails on the breadcrumb-read invocation. Verify step 09 is merged before running the smoke; if it isn't, stop and ask.
- **Conventional commit suggestion**: `feat(skills): add /wakeup read-only briefing skill`. One commit is fine.
