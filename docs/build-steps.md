# `clast` — Build Steps Meta-Doc

> Meta-doc. Defines the **format and conventions** for self-executing `step-NN.md` prompts used to build `clast`. The actual steps live in `docs/steps/step-NN-<short-description>.md` and are generated separately following the conventions here.

Read [`overview.md`](./overview.md) first for context. Then this doc tells you how the step files work; the step files themselves tell you what to build.

---

## Why this exists

The plan docs (`overview.md`, `cli-contract.md`, `skill-prompts.md`, `repo-bootstrap.md`) describe **what** to build. They're reference material, organized for human reading, navigable top-down by topic.

The step files describe **how** to build it, one self-contained chunk at a time. Each step:

- Is sized to fit one Claude Code session without context exhaustion.
- Produces something committable and shippable in isolation (the repo is never left in a broken state).
- References the reference docs rather than restating them.
- Has explicit acceptance criteria, so completion is unambiguous.
- Names its dependencies on previous steps, so they execute in order.

You invoke a step like:

```
execute @docs/steps/step-03-snapshot-subcommand.md
```

Claude Code reads the step, reads the referenced docs, does the work, and stops. The user reviews, commits, and moves to the next step.

---

## Step file structure

Every step file follows this exact shape:

```markdown
---
step: 03
title: snapshot subcommand
depends_on: [01, 02]
size: medium                   # small | medium | large
references:
  - docs/overview.md            # always
  - docs/cli-contract.md#clast-snapshot
  - docs/repo-bootstrap.md#libclastclast-manifest-libbash
---

# Step 03: Implement `clast snapshot`

## Context

One paragraph: what was built in prior steps, what state the repo is in now,
what this step assumes is already true.

## Goal

One sentence: what this step accomplishes.

## References

Bulleted list of doc sections to read before starting. Be specific
(`cli-contract.md#clast-snapshot`, not just `cli-contract.md`).

## Tasks

Numbered list of concrete tasks. Each task is testable.

1. Create `lib/clast/clast-manifest-lib.bash` with the functions
   listed in `repo-bootstrap.md` under "lib/clast/clast-manifest-lib.bash".
2. Create `lib/clast/clast-subcommands/snapshot.bash` implementing the
   contract in `cli-contract.md#clast-snapshot`.
3. Add the `snapshot` case to the dispatcher in `bin/clast`.
4. Create `test/test-snapshot.sh` using the `simple/` and `empty/` fixtures.
5. Update README.md with a usage example.

## Acceptance criteria

Testable conditions that prove the step is done. Be specific.

- `clast snapshot` exits 0 with no output when run against the `empty/` fixture.
- `clast snapshot` against the `simple/` fixture creates two files under
  `transcripts/<date>/` and writes two lines to `.manifest.jsonl`.
- `clast snapshot --dry-run` against `simple/` prints the planned actions
  and writes nothing.
- `clast snapshot --json` against `simple/` produces valid JSON matching
  the schema in `cli-contract.md#clast-snapshot`.
- `test/test-snapshot.sh` passes.
- `shellcheck` passes on all modified files.

## Out of scope

What this step explicitly does NOT do, to prevent scope creep.

- Do not implement `clast doctor`'s manifest-rebuild path; that's step 09.
- Do not handle Windows path encoding; that's step 02's responsibility.
- Do not optimize for large `~/.claude/projects/` trees (1000+ sessions);
  that's a separate performance step in v1.1.

## Verification

Concrete commands to run that prove acceptance criteria are met.

```bash
# Run the test suite
test/test-snapshot.sh

# Lint
shellcheck bin/clast lib/clast/clast-subcommands/snapshot.bash \
  lib/clast/clast-manifest-lib.bash

# Manual smoke test against the simple fixture
CLAST_PROJECTS_DIR=test/fixtures/simple \
CLAST_JOURNAL_DIR=$(mktemp -d) \
  bin/clast snapshot
```

## Notes for the implementer

Anything tricky, gotcha-y, or worth flagging that isn't captured in the
reference docs.

- The dash-substitution decoder from step 02 is required here; do not
  duplicate logic.
- The manifest's "most recent line wins" semantics matter for the
  re-snapshot case (a session grew, capture it again). Test this path
  explicitly.
- Atomic write: never leave a partial JSONL in `transcripts/`. Compose
  to a temp file in the same directory, then rename.
```

---

## YAML frontmatter fields

| Field | Required | Purpose |
|---|---|---|
| `step` | yes | Two-digit number, matches filename prefix. |
| `title` | yes | Short human-readable title; matches the rest of the filename. |
| `depends_on` | yes | Array of step numbers this step requires complete. `[]` for the first step. |
| `size` | yes | `small` (~30 min) / `medium` (~1 hr) / `large` (~2 hr). If a step would be larger than `large`, split it. |
| `references` | yes | Array of doc paths (with optional `#anchor`) to read before starting. |

---

## Step sizing guidelines

- **Small step**: 1–3 files touched, no cross-component changes. Example: "add `--json` flag to `clast projects`."
- **Medium step**: 4–8 files, one component end-to-end. Example: "implement `clast snapshot`."
- **Large step**: 8–15 files, spans multiple components but is naturally one unit. Example: "scaffold the entire test framework + first three fixtures."
- **Larger than large**: split. If you can't fit it in one session, the step is too big.

Each step must leave the repo committable. No "TODO: finish in next step" placeholders that would break tests.

---

## Reference conventions

Always reference the planning docs rather than restating them. The step file says "implement per `cli-contract.md#clast-snapshot`" — it does not paste the contract inline.

Cross-step references use the step number: "step 03's snapshot lib." Avoid filename references between steps (the title can change).

Doc anchors follow Markdown auto-generated anchor rules: header text lowercased, spaces to hyphens, punctuation stripped. `## `clast snapshot`` becomes `#clast-snapshot`.

---

## Naming conventions

```
step-NN-short-description.md
```

- `NN` is two digits (`01`, `02`, ... `99`). Pad with zero.
- `short-description` is kebab-case, 3–6 words, descriptive of the step's goal.
- Examples:
  - `step-01-repo-scaffold.md`
  - `step-02-core-libs-and-decoder.md`
  - `step-03-snapshot-subcommand.md`

---

## Execution guidance

When a user runs `execute @docs/steps/step-NN-foo.md`, the executing agent should:

1. **Read the step file fully** before doing anything.
2. **Read every doc in `references:`** before doing anything.
3. **Verify dependencies are met** — if `depends_on: [01, 02]`, check that the
   tasks from those steps appear complete. If not, **stop and ask the user**
   rather than attempting to redo prior work.
4. **Execute tasks in order**, committing only after acceptance criteria pass.
5. **Run verification commands** before declaring done.
6. **If anything is ambiguous or out of scope**, stop and ask. Do not improvise
   scope expansions; that's what `out of scope` lists are for.
7. **On completion**, suggest a commit message in conventional-commits form
   (e.g., `feat(snapshot): implement clast snapshot subcommand`).

---

## Planned steps for `clast` v1

Suggested step list. Order satisfies dependencies. Each step has a one-line
goal; full content of each step file is generated separately.

| # | Title | Size | Depends on | Goal |
|---|---|---|---|---|
| 01 | repo-scaffold | small | — | Create top-level files: README stub, LICENSE, .gitignore, .editorconfig, .envrc, **flake.nix (dev shell only)**, Makefile, package.json, CHANGELOG.md, AGENTS.md, CLAUDE.md. Set up GH Actions skeleton. No code yet. Dev shell provides `bash`, `jq`, `shellcheck`, `git`, `pre-commit` so step 02+ has a working environment. |
| 02 | core-libs-and-decoder | medium | 01 | Implement `lib/clast/clast-lib.bash` and `lib/clast/clast-decode-lib.bash` with full test coverage. The decoder handles ambiguous segments (with `--full` regression fixtures). Set up `test/helpers.sh`. |
| 03 | dispatcher-and-whereami | small | 02 | Implement `bin/clast` dispatcher + `clast whereami` (simplest subcommand, good first integration test). |
| 04 | manifest-lib | small | 02 | Implement `lib/clast/clast-manifest-lib.bash` with append/lookup/rebuild functions. Tests against `corrupt-manifest/` fixture. |
| 05 | registry-lib-and-subcommand | medium | 02, 04 | Implement `lib/clast/clast-registry-lib.bash` + `clast registry list/add/resolve/remove`. Tests against multi-project fixture. |
| 06 | snapshot-subcommand | medium | 02, 03, 04 | Implement `clast snapshot`. Tests against `simple/`, `multi-project/`, `empty/` fixtures. |
| 07 | query-subcommands | medium | 06 | Implement `clast projects`, `clast sessions`, `clast show`. All with `--json` output. |
| 08 | entries-subcommand | medium | 05, 06 | Implement `clast entries list/read/write`. Tests for frontmatter generation and slug collision suffixing. |
| 09 | breadcrumb-subcommand | small | 05 | Implement `clast breadcrumb` (write + read + list modes). |
| 10 | stats-and-doctor | medium | 04, 05, 06, 08 | Implement `clast stats` and `clast doctor` (including `--fix` for safe operations). |
| 11 | plugin-scaffold-and-hook | small | 03 | Create `.claude-plugin/plugin.json`, `hooks/hooks.json`, `hooks/snapshot.sh`. Make hook executable. |
| 12 | skill-day-wakeup | medium | 06, 07, 08, 11 | Write `SKILL.md` for `/day-wakeup` per `skill-prompts.md`. Test by manually invoking against a populated test journal. |
| 13 | skill-wakeup | small | 08, 09, 11 | Write `SKILL.md` for `/wakeup`. |
| 14 | install-scripts | small | all CLI | Write `install.sh` / `uninstall.sh`. Verify on a temporary prefix. |
| 15 | nix-flake-package | medium | 14 | Add `packages.default` and `overlays.default` to the existing `flake.nix` (dev shell from step 01 stays). Verify `nix build` and `nix run`. Package output bundles `bin/`, `lib/`, plugin, hooks, examples. |
| 16 | npm-publish-prep | small | 14 | Finalize `package.json`, add `prepublishOnly` script, do a dry-run pack. |
| 17 | ci-workflows | medium | all | Write `.github/workflows/test.yml`, `nix.yml`, `release.yml`. Verify they pass on a dummy PR. |
| 18 | docs-and-examples | medium | all | Finalize README, write `examples/cron/`, write `examples/workflows/morning-briefing.md`, polish CHANGELOG. |
| 19 | v1.0-release | small | all | Tag v1.0.0, run release workflow, verify npm + nix install paths work for an external user. |

**Suggested grouping for review checkpoints:**

- After step 06: capture flow works end-to-end. Smoke test by snapshotting Beau's real `~/.claude/projects/`.
- After step 10: full CLI complete. Beau can use it standalone without the plugin.
- After step 13: plugin complete. Full UX of `/day-wakeup` + `/wakeup` works.
- After step 17: shippable. CI is green, install paths work.
- After step 19: released.

---

## How to generate the actual step files

Two paths:

**Path A: Generate them all up front.** Ask the planning agent (or another tool) to read the four reference docs and this meta-doc, then produce all ~19 step files. Beau reviews the batch, then executes them in order.

**Path B: Generate as you go.** When ready to do step N, ask the agent to generate `step-NN-<title>.md` based on the planned list above. Faster feedback loop; lower risk of drift from earlier steps' actual outputs.

Recommendation: **Path B**. The planned step list above is a forecast, not a contract. Earlier steps may shift the design slightly, and later steps should be generated against the actual repo state, not the planned one.

When generating a step file, the prompt to the agent should be approximately:

```
Generate docs/steps/step-NN-<title>.md following docs/build-steps.md format.

Step goal (one sentence): <from the planned list above>

Current repo state: <what exists in the repo right now; the agent can inspect>

Dependencies: <which prior steps are complete>

Reference docs to point to: <relevant sections of overview/cli-contract/skill-prompts/repo-bootstrap>

Acceptance criteria: derive from the contract docs.

Out of scope: explicitly carve out anything that belongs to a future step.
```

---

## Examples to bootstrap from

The full step-01.md and step-02.md should be generated next as exemplars; later
steps can mimic their shape. Suggested approach:

1. Generate step-01 using this meta-doc.
2. Beau reviews it for shape and tone.
3. Generate step-02 using the same conventions.
4. Beau confirms the pattern is right.
5. Mass-generate the rest (or generate as-you-go per Path B above).

---

## Anti-patterns to avoid in step files

- **Restating reference content.** "The snapshot command should take new
  files and copy them..." — no. Say "implement `clast snapshot` per
  `cli-contract.md#clast-snapshot`" and link.
- **Acceptance criteria that aren't testable.** "Code should be clean" — no.
  "shellcheck passes with no warnings" — yes.
- **Implicit dependencies.** If step 06 needs step 04's manifest lib, declare
  it in `depends_on: [..., 04, ...]`. Don't assume the executor inferred it.
- **Scope creep escape hatches.** "Also fix any bugs you notice" — no. The
  step does the step; bugs get their own step or issue.
- **Open-ended verification.** "Verify it works" — no. List the exact commands.

---

## When to update this meta-doc

- A step in practice runs significantly larger than `large`. → Refine sizing guidance.
- A common ambiguity pattern emerges across multiple steps. → Add to anti-patterns.
- A reference convention proves brittle (e.g., header text changes break anchors). → Document the workaround.
- A new step type emerges (e.g., refactor steps, dependency-update steps). → Add a section.

This doc is meant to evolve as the build progresses. v1 of this doc is the starting convention; expect a v2 after the first few steps run.
