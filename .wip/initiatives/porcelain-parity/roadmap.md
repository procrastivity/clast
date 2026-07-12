# Roadmap — porcelain-parity

The plan of record for Porcelain parity: keep the clast CLI and the Claude plugin skills in sync. Brief: [`BRIEF.md`](./BRIEF.md). Locked
decisions graduate to `engineering/decisions/` (ADRs) as soon as they lock;
this roadmap holds the plan; each Step gets a
`workplans/step-NN-<slug>.md` when it starts.

Started: 2026-07-11.

---

## Round 1 — Close the audited divergences

Every divergence the parity audit found is either fixed or consciously recorded
as intended.

The four lanes touch disjoint files and run in parallel. The brief lane is
sequential internally: both of its steps edit
`lib/clast/clast-porcelain-subcommands/brief.bash`, so they cannot be split
across lanes.

### Lane brief
- **step-01 — brief: read the shared prompt templates** ✅ shipped 2026-07-11 — rewrite the prompt section of `skills/brief/SKILL.md` to read `lib/clast/prompts/brief-{system,user}.md` the way `skills/wake/SKILL.md` already does, and settle the two smaller divergences (entry caps 3-per-group/8-total vs a flat `--limit 5`; `registry resolve --json` and the workspace label) as fixed or intended. `[tracker: BDS-83]`
- **step-02 — brief: `--help` and an arg loop** ✅ shipped 2026-07-12 — add `_clast_brief_usage` plus flag parsing to `brief.bash`, following the shape PR #45 set for wake: `-h`/`--help` exits 0, `--` ends flags, an unknown flag exits 2 instead of being silently ignored, `$1` stays the project slug. Prerequisite for the guard in step-07. `[tracker: BDS-86]`

### Lane wake
- **step-03 — wake: close the pre-#45 divergences** — work the drift table row by row (scan window `-14d` vs `-30d`, the promote decision/common-issue/workflow flow, per-session Dismiss, triage quit, model-call timing, the 2000-char turn cap, session id + recorded date). **Direction settled (BRIEF, 2026-07-12): make the SKILL match the CLI.** The CLI is the reference — the two surfaces started in sync and the skill fell behind because development focused on the CLI, so these divergences are lag, not design. Change the skill row by row; deviating from that default needs a stated reason plus a `cli-only` allowlist entry in the step-07 guard. `[tracker: BDS-84]`

### Lane plugin-hygiene
- **step-04 — plugin hygiene** ✅ shipped 2026-07-12 — reconcile `.claude-plugin/plugin.json` (`0.1.0`) with `package.json` (`0.0.6`) and extend `contrib/check-version-sync.sh` to cover the plugin manifest; get the stray `wip` `commands/`, `agents/`, and `.claude-plugin/README.md` artifacts out of the plugin root so `claude plugin install` cannot load them as clast's; single-source or delete the rotted SKILL copies embedded in `docs/reference/plugin.md`. `[tracker: BDS-88]`

### Lane coverage-decision
- **step-05 — mirror `retro` as a skill; declare `undismiss` CLI-only** — **the decision is made (BRIEF, 2026-07-12): `retro` gets a plugin skill, `undismiss` does not.** So this step is now implementation, not deliberation: author `skills/retro/SKILL.md` mirroring `clast retro` (reading the shared `lib/clast/prompts/retro-summary-{system,user}.md` templates — do NOT inline a prompt copy, that is the BDS-83 mistake) and covering its flag surface (`--from`/`--to`, `--all`, `--window`, `--refresh`, `--json`); then record `undismiss` as `cli-only` with its stated reason so the step-07 guard stops flagging it. Consequence for step-07: the parity manifest covers three mirrored subcommands (`wake`, `brief`, `retro`) plus one `cli-only` entry. `[tracker: BDS-85]`

## Round 2 — Tell the truth, then guard it

The docs match the porcelain, and a fail-closed guard keeps the two surfaces
from drifting again.

Sequential, and after Round 1 by necessity: the guard asserts that every
`CLAST_*` var in `lib/clast/**.bash` appears in `docs/reference/config.md` and
that the wake `--since` default matches across surfaces, so the docs must be
true and steps 01-05 must have settled before it can pass.

- **step-06 — docs: porcelain flags, and kill the stale cron claims** — delete the "`clast wake` is interactive and expects a tty — don't run it from cron" assertions in `docs/guides/run-without-claude-code.md`, which `--auto` makes false; document `--auto` and `CLAST_WAKE_AUTO_MIN_CHARS`; add a `clast wake --auto` example to the cron and systemd guides; add the missing `[wake]` block to `examples/config/config.toml.sample`; decide whether porcelain flags belong in `docs/reference/cli.md`, which today documents `clast-plumbing` only. Do not hand-edit `CHANGELOG.md` — git-cliff generates it. `[tracker: BDS-87]`
- **step-07 — the parity drift guard** — add `test/parity.tsv` (per subcommand: each flag and `CLAST_*` var, tagged `mirrored` or `cli-only`) and `test/test-parity.sh` asserting the five checks, registered in the `suites=()` array in `test/test-clast.sh`. It must fail closed: an unclassified flag is an error, so drift is caught by forcing a decision when the flag is added. `[tracker: BDS-89]`

---

## Deferred (decided-not-now)

_Items consciously postponed; keep the why so future-you can re-evaluate._

## Backlog (cross-cutting; see also `.wip/backlog.md`)

_Cross-cutting work that hasn't earned a round yet._
