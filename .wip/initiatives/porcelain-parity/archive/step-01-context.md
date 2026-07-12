# Step 01 — Rolling Context (archived)

Archived at Step Boundary, 2026-07-12. Original live shared note: Solo scratchpad #121 (`porcelain-parity-step-01-context`).

**Coordinator**: porcelain-parity-step-01-coordinator (process_id 813)
**Researcher**: porcelain-parity-step-01-researcher (process_id 815)
**Workplan**: .wip/initiatives/porcelain-parity/workplans/step-01-brief-read-the-shared-prompt-templates.md
**Build started**: 2026-07-12T04:02:50Z
**Shipped**: 2026-07-12

## Batching plan

| Batch | Tasks | Rationale |
|---|---|---|
| 1 | Task 1 — swap inlined synthesis prompt for shared-template loading + placeholder substitution | Must land first; establishes the read-and-substitute pattern chunk 2/3 build on |
| 2 | Task 2 — align data-gathering (workspace grouping/caps + label) with CLI behavior | Depends on task 1's structure; same file, sequential to avoid merge churn |
| 3 | Task 3 — clarifying-copy polish (only if needed) | Depends on 1+2 landing; smallest, may be a no-op if 1+2 already read cleanly |

All three tasks touch only `skills/brief/SKILL.md`. Builders ran **sequentially**, not in parallel — same file, dependent edits. Each Builder committed directly to `beau/bds-82-porcelain-parity` and pushed after its commit.

## Decisions made during build

- Orchestrator approved the workplan as written (2026-07-12). Both smaller divergences (entry caps, workspace label) are FIXED, not recorded-as-intended.
- On the open question re: inline fallback prompt — Orchestrator's ruling: do NOT keep a maintained inline fallback (recreates the BDS-83 drift surface). A one-line "template missing, stop and tell the user" is acceptable; a second copy of the prompt is not.
- Scope is hard-locked to `skills/brief/SKILL.md` only. Do NOT edit `lib/clast/clast-porcelain-subcommands/brief.bash` (step-02/BDS-86 owns it). Do not touch `docs/`. No new branch, no new PR (#46 covers the initiative).
- **Task 2 retry (1 of 2 allowed)**: Coordinator review caught a correctness bug in Builder 02's first pass (commit 936098e) — Step 1's edit replaced the project-slug resolution with a label-only extraction, deleting `<slug>` capture entirely even though the rest of the skill (Step 2 onward) depends on `<slug>`. Reopened todo 503, sent the Builder a targeted fix request (both slug AND label must be resolved, matching the CLI's two-separate-calls pattern in brief.bash); builder fixed it in place, no respawn needed.

## Escalations

None. All three tasks completed without needing to route to the Orchestrator or the human.

## Per-task outcomes

**Task 1** (todo 502) — Complete. Builder `porcelain-parity-step-01-builder-01` (process 816) replaced skills/brief/SKILL.md's inlined "Synthesis prompt" section with explicit pointers to `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/brief-{system,user}.md` and the five confirmed placeholders; kept only a one-line missing-template stop message, no maintained fallback copy. `make test`: only test-retro-prov's known 2 pre-existing failures (verified independently against the raw log — `test-retro-prov: 11 passed, 2 failed`, the sole failing suite). `make lint`: clean. Commit `bdfc35b` on beau/bds-82-porcelain-parity, pushed.

**Task 2** (todo 503) — Complete, after 1 retry. Builder `porcelain-parity-step-01-builder-02` (process 817) aligned skills/brief/SKILL.md's Step 1/2 with the CLI: workspace grouping (3-per-group/8-total, hoist current workspace, headers only for multi-group) and `registry resolve --json` label capture. First pass (936098e) had a real bug — it replaced project-slug resolution with label-only extraction, silently deleting `<slug>` capture even though the rest of the skill depends on it. Caught on Coordinator review (diffed the file directly, not just the summary), sent back with a specific fix. Builder fixed it correctly on retry, amended the commit to `8cd15e8` (force-pushed with lease, expected/authorized). Independently re-verified: Step 1 now runs both `registry resolve "$(pwd)"` (slug) and `registry resolve "$(pwd)" --json | jq -r '.label // empty'` (current_label), with clear downstream usage instructions. `make test`: only test-retro-prov's known 2 failures. `make lint`: clean.

**Task 3** (todo 504) — Complete, correctly a no-op. Builder `porcelain-parity-step-01-builder-03` (process 818) reviewed skills/brief/SKILL.md after Tasks 1+2 and found it already sufficient: the "Shared prompt templates" pointer section (from Task 1) already covers the "edit there, not here" guidance; Step 1/2 prose is internally consistent; no maintained inline fallback prompt reintroduced. No commit made — correctly avoided a cosmetic/busywork edit. Coordinator independently re-read the full final file end-to-end and confirmed the finding.

Final state on `beau/bds-82-porcelain-parity`: commits `bdfc35b` (Task 1), `8cd15e8` (Task 2, amended after 1 retry). Task 3 added no commit.

## Retro

**What went well**: The workplan (Researcher-produced) was accurate and well-scoped — its factual claims about placeholder names, CLI entry-cap logic, and the `registry resolve --json` label behavior all checked out exactly against the source files, both when the Coordinator reviewed it and independently when the Orchestrator re-verified before approving. Chunking the work into 3 small, sequential, same-file commits kept each Builder's diff reviewable. Task 3 correctly recognized its own no-op rather than forcing a cosmetic commit — the "may be a no-op" instruction in the workplan and ledger entry worked as intended.

**What didn't go well / process note**: Builder 02's first pass on Task 2 introduced a real regression — it read "add workspace-label resolution" and implemented it by *replacing* the existing slug-resolution command rather than adding a second command alongside it, silently breaking every downstream `<slug>` reference in the skill. The Builder's own self-report (test results, decisions) did not surface this — it required the Coordinator to diff the actual file content against the prior version, not just trust the summary comment. This is the one recurring risk pattern worth carrying into future steps: **a Builder's ledger comment describing what it changed is not sufficient verification on its own for edits to porcelain/skill files where correctness depends on cross-references within the same file** (a variable produced in one Step being consumed in a later Step). The Coordinator caught this on the very first review pass here; future Coordinators (and this one, on later steps) should keep doing a direct file read/diff before accepting a Builder's completion, not just parsing its comment.

**Process gap**: No `coordinator-context` ledger (todo) entry was created for this step at Phase 2 kickoff — only the shared note (scratchpad) was tagged `coordinator-context`. Nothing was left dangling because of this (no such entry existed to leave open), but a future Coordinator run should consider whether a dedicated coordinator-context todo is worth creating up front for better ledger-level visibility into Coordinator-owned oversight work, separate from the Builder task entries.

**Carry-forward for step-02** (BDS-86, `brief.bash` `--help`/arg-parsing): step-02 now inherits skills/brief/SKILL.md in its post-step-01 state (shared-template loading, workspace-grouped entries, `registry resolve --json` label). step-02 only touches `lib/clast/clast-porcelain-subcommands/brief.bash`, which step-01 deliberately left untouched — no merge conflict expected, but step-02's Researcher should re-read the current `brief.bash` fresh (not the version referenced in this step-01 workplan) since it's unchanged from before this step started.
