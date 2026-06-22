---
step: 23
title: entry labels and label/branch-segmented briefing
depends_on: [08, 22]
size: medium
references:
  - docs/overview.md
  - docs/reference/entry-frontmatter.md
  - docs/reference/cli.md#clast-plumbing-entries
  - lib/clast/clast-subcommands/entries.bash
  - lib/clast/clast-porcelain-subcommands/brief.bash
  - lib/clast/prompts/brief-system.md
  - lib/clast/prompts/brief-user.md
---

# Step 23: stamp `label` on entries + segment the brief by label/branch

## Context

Step 22 added a per-directory `label` to registry lines and made one slug
legally span multiple directories. But the briefing still treats a project
as a flat stream: `clast brief` gathers the newest 5 entries for a slug and
hands them to the LLM with no notion of *which directory* each came from.
For a project like `xesapps` that spans `dev`, `performance`, `review`, and
`control` clones, that means unrelated lines of work blur together and the
newest entry — wherever it came from — becomes "the active thread."

There is also a latent bug in `entries write`
(`clast-subcommands/entries.bash`): it resolves the project line by
**slug-first-match** (`map(select(.slug == $s)) | .[0]`), so when a slug
spans multiple directories every entry inherits the *first* line's
`project_path` regardless of which clone the session actually ran in. Real
journals show this — `performance/xesapps` sessions stamped with
`project_path: …/dev/xesapps`.

This step fixes that lookup, stamps the correct `label` onto each entry,
and teaches `clast brief` to segment by label (then branch).

## Goal

Record the originating directory's `label` (and correct `project_path`) on
every curated entry, and render the briefing grouped by workspace label and
branch instead of as one flat newest-first list.

## References

- `docs/reference/entry-frontmatter.md` — frontmatter schema to extend.
- `clast-subcommands/entries.bash` — `_clast_entries_write` project/path
  resolution and frontmatter composition.
- `clast-porcelain-subcommands/brief.bash` — `_clast_brief_gather_entries`,
  `_clast_brief_build_user_prompt`, `clast_cmd_brief`.
- `lib/clast/prompts/brief-system.md` / `brief-user.md` — the prompt the
  briefing renders from.
- `clast-registry-lib.bash` — `clast_decode_candidates`,
  `clast_registry_list_json` (to find the line by path).

## Tasks

1. **`entries.bash` — resolve the registry line by path, not slug:**
   - In `_clast_entries_write`, after deriving `seg` from the snapshot
     path, decode it to candidate filesystem paths
     (`clast_decode_candidates`) and select the registry line whose `path`
     matches a candidate. Use that line for `project_path`, `project_remote`,
     **and** the new `label`. Fall back to the current slug-first-match only
     if no path matches (keeps unregistered/legacy behavior working).
   - Add a `label` frontmatter field (place it next to `project_path`).
     Emit `label: null` when the line has no label.
2. **Frontmatter docs:** add `label` to
   `docs/reference/entry-frontmatter.md` (schema block + field table:
   "source = registry line for the session's directory; distinguishes
   clones of one slug").
3. **`brief.bash` — gather with directory grouping:**
   - `_clast_brief_gather_entries` currently emits a flat newest-first
     concatenation. Change it to group entries by `label` (fall back to
     `branch`, then "default") while preserving newest-first order within
     each group, and annotate each entry block with its `label` and
     `branch` so the model can see the segmentation. Keep the overall
     `--limit` budget but avoid one busy clone starving the others — pull
     the newest few *per group* up to the budget (document the cap in a
     comment; `log()`/comment any truncation).
4. **Prompt updates:**
   - `brief-user.md`: include `label`/`branch` with the entries block.
   - `brief-system.md`: change the "Recent sessions" / "Last session"
     guidance so that, when entries span more than one label, the briefing
     groups by **workspace (label)** and notes the branch — and the
     "Active thread" is chosen from the most recent entry *within the
     workspace the user is currently in* when that is known, otherwise the
     most recent overall. When only one label is present, render exactly as
     today (no behavior change for single-directory projects).
5. **Pass current workspace to the brief (best effort):** in
   `clast_cmd_brief`, resolve the *current directory's* label (via the
   registry line for `$(pwd)`) and pass it to the prompt as
   `{{current_label}}` so "Active thread" can prefer the workspace the user
   actually `cd`'d into. Empty when unknown or when a slug was passed
   positionally.
6. **Tests:**
   - `test/test-entries.sh`: a session whose directory is the 2nd+ line of
     a shared slug gets the **correct** `project_path` and `label`;
     single-line slug still works; unregistered segment still writes.
   - Brief rendering is LLM-driven and not unit-tested; add a non-LLM test
     that `_clast_brief_gather_entries` produces grouped, labeled output
     (the gather function is pure bash and testable in isolation — factor
     it so it can be exercised without an API call, or guard with the
     existing LLM-preflight skip used elsewhere).

## Acceptance criteria

- An entry curated from `~/Workspaces/performance/xesapps` has
  `project_path: …/performance/xesapps` and `label: performance` (not the
  `dev` line's path).
- A single-directory project's entries get `label: <parent-dir>` and the
  briefing for it looks identical to today's (no spurious grouping).
- For a slug spanning ≥2 labels, `clast brief` output groups recent work by
  workspace label and shows branch per group.
- Running `clast brief` from inside one clone prefers that clone's most
  recent entry for "Active thread" when entries from multiple clones exist.
- `make test` and `make lint` pass.

## Out of scope

- Grouping **today's sessions** by directory in the brief — sessions come
  from the manifest, not entries; per-directory session grouping can be a
  follow-up. Note the limitation in a comment.
- Migrating existing entries that lack `label` / have stale `project_path`
  — step 24.
- Any registry schema or `add`/`doctor` change — done in step 22.

## Verification

```bash
test/test-entries.sh
shellcheck lib/clast/clast-subcommands/entries.bash \
  lib/clast/clast-porcelain-subcommands/brief.bash

# Manual: write an entry for a non-first directory of a shared slug,
# confirm label + project_path in the resulting frontmatter.
```

## Notes for the implementer

- The path→line lookup belongs in `clast-registry-lib.bash` as a small
  helper (e.g. `clast_registry_line_for_path <path>` returning the matching
  JSON line) so both `entries write` and `brief`'s current-workspace
  resolution share it. Keep it a single `jq` pass over
  `clast_registry_list_json` (no per-candidate forks — match step 22/21's
  performance posture).
- Do not reorder or rename existing frontmatter fields; only **add**
  `label`. Existing parsers (`_clast_entries_list_consider`) ignore unknown
  fields, so this is backward compatible.
- Keep the single-label path byte-for-byte equivalent to today's briefing
  so the change is a strict superset; this also keeps existing snapshots of
  brief output (if any) stable.
