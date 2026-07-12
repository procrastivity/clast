# Workplan — step-01 · brief: read the shared prompt templates

_Rewrite `skills/brief/SKILL.md` so `/brief` reads the shared `lib/clast/prompts/brief-{system,user}.md` templates the same way `skills/wake/SKILL.md` already does, while also settling the audited entry-cap and workspace-label divergences against the CLI reference in `lib/clast/clast-porcelain-subcommands/brief.bash`._

Started: 2026-07-11.

## Decisions (made here, feed later steps)

_Locked choices that future steps can rely on. Each entry is a sentence the
next step can re-read without ambiguity._

- `skills/brief/SKILL.md` should stop inlining its own synthesis prompt and instead explicitly read `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/brief-system.md` and `$CLAUDE_PLUGIN_ROOT/lib/clast/prompts/brief-user.md`, mirroring the read-and-substitute pattern already used by `skills/wake/SKILL.md`.
- The skill should align to the shared brief prompt templates as the source of truth rather than preserving a second plugin-local copy; any fallback text left in the skill should be framed only as a missing-template safety net, not as a maintained alternate prompt.
- The entry-cap divergence should be FIXED, not preserved: the skill should gather enough entry metadata to enforce the same `3 per workspace group / 8 total` selection policy that the CLI uses, instead of the current flat `--limit 5`.
- The workspace-label divergence should be FIXED, not preserved: when `/brief` resolves the project from `pwd`, it should call `registry resolve --json`, extract the active workspace label, and pass that label into prompt construction so the active thread can prefer the current workspace exactly as the shared prompt expects.
- The Builder must keep step-01 scoped to `skills/brief/SKILL.md` (and only any minimal adjacent support if truly unavoidable), leaving `lib/clast/clast-porcelain-subcommands/brief.bash` structurally free for step-02's later `--help` / arg-parsing work.
- Guardrails for execution: work directly on `beau/bds-82-porcelain-parity`; do not create a branch or PR; do not touch `docs/`; run `direnv exec . make test` and `direnv exec . make lint` before every commit; treat `test/test-retro-prov.sh`'s known two failures as pre-existing/non-blocking only if they are the sole failures; use conventional commit prefixes and end every commit message with `Claude-Session: https://claude.ai/code/session_01PyjLYz2GFRVgYS3nAtUoXm`.

## Chunks

_Implementation broken into reviewable pieces. Each chunk is small enough to
land in one focused commit._

1. **Replace the inlined `/brief` synthesis prompt with shared-template loading.**
   - Edit `skills/brief/SKILL.md` so its Step 3 references the installed brief prompt files, matching the wake skill's pattern: read `brief-system.md`, read `brief-user.md`, and substitute the template placeholders the brief user prompt expects (`{{project}}`, `{{current_label}}`, `{{entries}}`, `{{breadcrumbs}}`, `{{sessions}}`).
   - Remove the inlined prompt block as maintained content.
   - If a fallback is retained for missing prompt files, keep it minimal and explicitly secondary to the shared files.
   - Commit scope: prompt-sharing only.

2. **Align `/brief` skill data-gathering guidance with CLI entry grouping and workspace-label behavior.**
   - Rewrite the skill's Step 1/2 instructions so the no-arg path uses `registry resolve --json` and captures both slug and current workspace label.
   - Update the entry-gathering instructions from `--limit 5` to the CLI's two-stage policy: fetch the project entries list, group by workspace label (fallback branch/default), hoist the current workspace when present, read up to 3 entries per group and 8 total, and render workspace headers when multiple groups exist.
   - Keep the today's-breadcrumbs and today's-sessions reads aligned with current CLI behavior.
   - Commit scope: data-shape / behavior parity only.

3. **Polish the skill text so the new shared-template contract is explicit and future drift is less likely.**
   - Add a short note in `skills/brief/SKILL.md` that the brief prompt lives in `lib/clast/prompts/brief-{system,user}.md` and should be changed there, not copied into the skill.
   - Verify the final wording still reads naturally as operator guidance and does not silently preserve either audited divergence.
   - Commit scope: clarifying copy only, if needed after the functional edits.

## Test strategy

_What the tests cover and how. Note any deferred coverage with a reason._

- Run `direnv exec . make test` after each chunk is ready to commit; accept the run only if the sole failures are the known two assertions in `test/test-retro-prov.sh` tied to the dated fixtures.
- Run `direnv exec . make lint` before each commit.
- Manually diff `skills/brief/SKILL.md` against:
  - `skills/wake/SKILL.md` for the shared-template loading pattern,
  - `lib/clast/prompts/brief-system.md` and `lib/clast/prompts/brief-user.md` for placeholder names and prompt structure,
  - `lib/clast/clast-porcelain-subcommands/brief.bash` for the `3 per group / 8 total` and `registry resolve --json` behavior.
- No new automated coverage is required in this step; the value here is removing a prompt-copy drift source and making the skill instructions match the already-tested CLI behavior. Any parity guard coverage belongs to step-07.

## Definition of done

_Concrete checks that prove the step is finished. Lean toward observable
behaviour over file-level checklists._

- `skills/brief/SKILL.md` no longer contains a maintained inline copy of the brief synthesis prompt and instead tells the model to load `lib/clast/prompts/brief-system.md` and `lib/clast/prompts/brief-user.md` from `$CLAUDE_PLUGIN_ROOT`.
- The skill text names the exact placeholder substitution contract used by `brief-user.md`.
- The skill's project-resolution instructions capture the current workspace label via `registry resolve --json` when resolving from `pwd`.
- The skill's entry-selection instructions now match the CLI's `3 per workspace group / 8 total` behavior rather than a flat `--limit 5`.
- The two audited smaller divergences (entry caps, workspace label) are no longer silent: both are explicitly fixed in the skill text.
- `direnv exec . make test` and `direnv exec . make lint` pass under the allowed known-failure rule.
- No files under `docs/` are changed, and no step-02 `brief.bash` work is pulled into this step.

## Open questions to resolve during execution

_Questions whose answers don't block starting but DO block finishing. Each
should have a "lean" so the worker isn't paralyzed._

- **Should the skill spell out the grouping algorithm in detail or describe it more declaratively?** Lean: keep enough concrete detail (group key, hoist current workspace, 3-per-group, 8-total) that a future editor can see the parity contract without re-deriving it from `brief.bash`.
- **Should the skill retain any inline fallback prompt text if the shared template files are missing?** Lean: only if the existing skill conventions or runtime expectations require it; otherwise prefer a hard requirement on the installed shared files to avoid recreating the drift surface.
- **Should the skill mention that today's sessions are not workspace-grouped the way entries are?** Lean: only if that nuance materially helps the operator; otherwise keep the skill aligned with current CLI behavior without over-explaining an internal limitation.
