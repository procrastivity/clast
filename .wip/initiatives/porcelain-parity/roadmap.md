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
- **step-02 — brief: `--help` and an arg loop** — add `_clast_brief_usage` plus flag parsing to `brief.bash`, following the shape PR #45 set for wake: `-h`/`--help` exits 0, `--` ends flags, an unknown flag exits 2 instead of being silently ignored, `$1` stays the project slug. Prerequisite for the guard in step-07. `[tracker: BDS-86]`

### Lane wake
- **step-03 — wake: close the pre-#45 divergences** — work the drift table row by row (scan window `-14d` vs `-30d`, the promote decision/common-issue/workflow flow, per-session Dismiss, triage quit, model-call timing, the 2000-char turn cap, session id + recorded date), fixing each or recording it as an intended divergence for the guard's allowlist. `[tracker: BDS-84]`

### Lane plugin-hygiene
- **step-04 — plugin hygiene** — reconcile `.claude-plugin/plugin.json` (`0.1.0`) with `package.json` (`0.0.6`) and extend `contrib/check-version-sync.sh` to cover the plugin manifest; get the stray `wip` `commands/`, `agents/`, and `.claude-plugin/README.md` artifacts out of the plugin root so `claude plugin install` cannot load them as clast's; single-source or delete the rotted SKILL copies embedded in `docs/reference/plugin.md`. `[tracker: BDS-88]`

### Lane coverage-decision
- **step-05 — decide plugin coverage for `retro` and `undismiss`** — for each: mirror it as a skill, or declare it CLI-only and put it on the parity guard's allowlist with a stated reason. `undismiss` is the pointed one — `/wake` already tells plugin users to run a command they have no skill for. The outcome sets the guard's manifest scope in step-07. `[tracker: BDS-85]`

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
