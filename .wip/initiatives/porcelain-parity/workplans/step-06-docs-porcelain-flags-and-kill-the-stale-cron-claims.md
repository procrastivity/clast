# Workplan — step-06 · docs: porcelain flags, and kill the stale cron claims

_What does this step deliver? Anchor to the roadmap entry and the spec/ADR
this step lands or extends._

Tracker: BDS-87. Roadmap: Round 2, step-06. Delivers on the BRIEF's
"AGENTS.md forbids touching docs/ without an explicit request — BDS-87 is
that explicit request" clause: bring `docs/` and `examples/` up to date with
what PR #45 (`--auto`) and step-03 (skill-matches-CLI) already shipped in
code, and stop documenting a `-30d` default that step-03 changed to `-14d`.
Scope is `docs/**` and `examples/**` only — no `skills/*`, no `lib/*`, no
hand-edits to `CHANGELOG.md` (git-cliff generates it).

Started: 2026-07-12.

## Decisions (made here, feed later steps)

- **`docs/reference/cli.md` stays `clast-plumbing`-only.** Its own opening
  line (`docs/reference/cli.md:8`) says "All subcommands are LLM-free" — that
  is the file's stated contract, and `clast wake`/`brief`/`retro` all call an
  LLM by design, so they don't fit under it even in principle. Porcelain flag
  reference lives in `docs/guides/run-without-claude-code.md`, which already
  has one `##` subsection per porcelain subcommand
  (`## clast wake`, `## clast brief`, `## clast retro`) — chunk 1 upgrades
  those subsections from prose-only to prose + a `Flags`/`Env` list per
  command, sourced from each subcommand's own `--auto`/`-h` usage heredoc
  (`wake.bash:32-53`, `brief.bash:196+`) so the guide can't drift from the
  actual usage text. `docs/reference/cli.md` gets nothing new; do not add
  `## clast wake` etc. there. Deviation risk: if a future step wants a single
  canonical reference doc for all four porcelain commands, that's a new
  decision, not an oversight — flag it rather than silently splitting the
  work across two files again.
- **The `-30d` claim in `query-recipes.md` is NOT part of this fix.** Its
  three `-30d` occurrences (lines 19, 29, 42) are literal example command
  args the reader can change to any window they like — they don't assert
  anything about `clast wake`'s *default* scan window, unlike the prose in
  `run-without-claude-code.md:56-57`. Leave them alone. (Restated as an open
  question below in case a reviewer disagrees — lean is "leave as-is.")
- **`examples/config/config.toml.sample` gets a real `[wake]` block**, styled
  like `[paths]`/`[time]`/`[logging]` (commented-out key + `# Env
  equivalent:` comment), even though `[wake]` has no TOML key today —
  matching how `docs/reference/config.md`'s `[wake]` table already documents
  it as a real (if TOML-less) section. This differs from `[llm]`, which is
  omitted from the sample entirely; the reasoning is that `[llm]` carries a
  bearer-token secret that has no business in a config *file* even as a
  documented no-op, whereas `[wake]`'s three vars are plain tunables planned
  for eventual TOML support. State this contrast in the PR/commit body if
  asked, since it's a judgment call, not a fact.

## Chunks

Each chunk = one file, one commit, `docs:` prefix (or `chore:` for the
config sample if that reads more accurately — lean `docs:` since it's a
documentation-only sample file per its own header).

1. **`docs/guides/run-without-claude-code.md` — kill the stale cron/tty
   claims + the `-30d` claim, add flags.**
   - Line 23: `- **An interactive terminal for `clast wake` (it reads choices
     from the tty)**.` → rewrite to state interactive mode is the *default*
     and needs a tty, but `--auto` (added by PR #45) skips that entirely and
     is the unattended/cron path. Cross-reference the `## clast wake`
     subsection below rather than duplicating flag detail here.
   - Lines 56-58 (the "Triage" step-1 bullet under `## clast wake`): replace
     "the last 30 days (`clast-plumbing sessions --since -30d`)" with "the
     scan window set by `CLAST_WAKE_SINCE` (default `-14d`)" — matches
     `wake.bash:44` (`CLAST_WAKE_SINCE (default -14d)`) and
     `docs/reference/config.md:60`, which already agree.
   - `## clast wake` subsection (currently lines 50-69): add a `--auto` bullet
     documenting non-interactive/cron use (mirror `wake.bash:32-53`'s usage
     text: skips triage + per-session prompt, no tty required, sessions whose
     draft generation fails are skipped, honors `CLAST_WAKE_AUTO_MIN_CHARS`
     to suppress trivial drafts, default 60, 0 disables the guard).
   - Line 121 (`## Automating it`): replace "`clast wake` is interactive and
     expects a tty — don't run it from cron" with guidance that
     `clast wake --auto` is cron/systemd-safe and is the intended unattended
     path, while interactive mode (no flag) remains the default for
     hands-on curation. Point at the (to-be-updated in chunk 3/4) cron and
     systemd guides for a worked example.
   - Verify no other file inherits this prose by grepping for "expects a
     tty" and "last 30 days" repo-wide before closing the chunk.

2. **`docs/reference/config.md` — add `CLAST_WAKE_AUTO_MIN_CHARS`, mention
   `--auto`.**
   - `[wake]` table (currently lines 57-60) is missing a
     `CLAST_WAKE_AUTO_MIN_CHARS` row entirely. Add one: default `60`, "In
     `clast wake --auto`, skip (don't write) a draft whose body is shorter
     than this many characters; the session stays uncurated for a later
     interactive pass. `0` disables the guard and writes every draft." —
     matches `wake.bash:365,369`.
   - The `[wake]` section intro (lines 53-55) doesn't mention `--auto` at
     all. Add a sentence: "`--auto` (a `clast wake` flag, not an env var)
     switches the porcelain to non-interactive mode; see
     [`run-without-claude-code.md`](../guides/run-without-claude-code.md#clast-wake--curate-the-day)
     for the flag reference." Confirm `CLAST_WAKE_SINCE`'s existing `-14d`
     default claim (line 60) still matches `wake.bash:44` — it does as of
     this writing, this chunk should not need to change that line, only
     confirm it.

3. **`docs/guides/automate-with-cron.md` — add a `clast wake --auto`
   example.**
   - File currently only automates `clast-plumbing snapshot` (the whole
     point of the file today). Add a new `##` section, e.g. "## Curating
     unattended", with a crontab line like
     `0 6 * * * /usr/local/bin/clast wake --auto >/dev/null 2>&1` and one or
     two sentences: it needs the `CLAST_LLM_*` env vars set in cron's
     environment (cron doesn't source a login shell profile — call this out
     explicitly, it's the #1 way this silently no-ops), and it's safe to run
     on a schedule because `--auto` skips trivial drafts
     (`CLAST_WAKE_AUTO_MIN_CHARS`) and never blocks on a tty.
   - Decide during execution whether to also add a commented example to
     `examples/cron/crontab.sample` — lean **yes**, for consistency with how
     the snapshot example is backed by a real sample file; if the sample
     file's header framing makes this awkward, downgrade to guide-prose-only
     and note the reason in the commit body.

4. **`docs/guides/automate-with-systemd.md` — add a `clast wake --auto`
   example.**
   - Same shape as chunk 3 but systemd units. Either a second service+timer
     pair (`clast-wake.service` / `clast-wake.timer`) or a prose-only
     `ExecStart` example reusing the existing timer's cadence — lean toward
     prose-only unless chunk 3 decides to ship a real crontab.sample entry,
     for consistency between the two guides. Must call out the same
     environment-sourcing caveat (`systemd` user units don't get a login
     shell's env either — `Environment=` or `EnvironmentFile=` in the
     `.service` is the fix).

5. **`examples/config/config.toml.sample` — add a `[wake]` block.**
   - Insert after `[logging]` (which ends the current file). Follow the
     existing style exactly: a `#`-commented key, an `# Env equivalent:`
     comment, one blank line between entries. Cover all three vars:
     `CLAST_WAKE_SINCE` (default `-14d`), `CLAST_WAKE_AUTODISMISS_NOOP`
     (default `1`), `CLAST_WAKE_AUTO_MIN_CHARS` (default `60`). Keep
     descriptions one line each, consistent with the terseness of
     `[paths]`/`[time]`/`[logging]` (the existing blocks are ~2 lines per
     key; don't import the full table prose from `config.md`).

## Test strategy

This step edits only prose/comments in `docs/` and `examples/` — there is no
code path to unit-test. Verification is:

- `direnv exec . make lint` and `direnv exec . make test` green before every
  commit (per initiative constraint) — these won't exercise the new prose
  directly, but must stay green because nothing in this step should touch
  `lib/*`/`skills/*`, and a red run signals an accidental scope leak.
- Manual cross-check per chunk: after editing, re-grep the source of truth
  (`wake.bash`, `brief.bash`, `docs/reference/config.md`) for the specific
  default/flag just documented, confirming the doc text is a verbatim-enough
  match (same default value, same env var name) — not a re-paraphrase that
  could drift again.
- Final pass: `grep -rn "last 30 days\|expects a tty\|don't run it from cron"
  docs/` should return zero hits tied to `clast wake` after chunk 1 (the
  `query-recipes.md` `-30d` example args are expected to remain and are not a
  failure).

## Definition of done

- `docs/guides/run-without-claude-code.md` no longer claims `clast wake`
  can't run unattended/from cron, no longer claims a 30-day default scan
  window, and documents `--auto` and its interaction with
  `CLAST_WAKE_AUTO_MIN_CHARS`.
- `docs/reference/config.md`'s `[wake]` table documents all three
  `CLAST_WAKE_*` vars the `wake.bash` usage heredoc defines, and mentions
  `--auto` exists (even though `--auto` itself isn't an env var and doesn't
  get a table row).
- `docs/guides/automate-with-cron.md` and `automate-with-systemd.md` each
  show a `clast wake --auto` example with the env-sourcing caveat spelled
  out.
- `examples/config/config.toml.sample` has a `[wake]` block covering
  `CLAST_WAKE_SINCE`, `CLAST_WAKE_AUTODISMISS_NOOP`, `CLAST_WAKE_AUTO_MIN_CHARS`.
- `docs/reference/cli.md` is untouched (confirms the "stays plumbing-only"
  decision was actually honored, not just decided).
- `docs/reference/plugin.md` is untouched (step-04 already fixed it; this
  step must not re-touch or re-embed anything there).
- `git diff --stat` for this step shows only files under `docs/` and
  `examples/` (plus the workplan itself and `.wip.yaml`/ledger bookkeeping).
- `direnv exec . make test` and `direnv exec . make lint` green on the final
  commit.

## Open questions to resolve during execution

- Does `examples/cron/crontab.sample` and/or the systemd unit files get a
  real second example (chunk 3/4), or does the `clast wake --auto` example
  stay guide-prose-only? Lean: add to `crontab.sample` for consistency (it
  already has a commented alternate-cadence example for snapshot); keep
  systemd prose-only unless that turns out lopsided once written — call the
  final choice in the chunk-3/4 commit bodies, not silently.
- Should `run-without-claude-code.md`'s per-command subsections adopt a
  uniform `Flags:` / `Env:` sub-list format (mirroring the `--help` heredoc
  shape) for `wake`, `brief`, and `retro` alike, or is it fine for `wake` to
  gain one and `brief`/`retro` to keep their current prose-only shape since
  they're out of scope for BDS-87? Lean: only touch `wake`'s subsection
  structurally; leave `brief`/`retro` alone — full uniformity is a
  nice-to-have, not what BDS-87 asked for, and re-touching working prose
  outside the diff's actual scope risks unrelated churn AGENTS.md would
  flag.
- If a reviewer disagrees with "leave `query-recipes.md`'s `-30d` examples
  alone," swapping them to `-14d` (or a neutral `-7d` already used
  elsewhere in the same file) is a one-line-per-occurrence fix — flag it as
  a fast follow-up rather than blocking this step on a stylistic call.
