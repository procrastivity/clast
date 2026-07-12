# Porcelain parity: keep the clast CLI and the Claude plugin skills in sync — BRIEF (single source of truth)

> The brief is the durable context for this initiative. Every later artifact
> (roadmap, workplans, intake amendments) reads this first. If a cross-cutting
> decision changes, it changes **here**.

- Slug: `porcelain-parity`
- Started: 2026-07-11

## Goal

`clast` ships the same curation UX twice, and nothing enforces that the two
surfaces stay equivalent:

- **CLI porcelain** — `bin/clast` (`wake`, `brief`, `retro`, `undismiss`):
  bash + an OpenAI-compatible endpoint, interactive TUI menus.
- **Model-backed porcelain** — the Claude Code plugin, which is skills-only:
  `skills/wake/SKILL.md`, `skills/brief/SKILL.md`.

Both drive the same deterministic core (`clast-plumbing`) and are supposed to
share prompt templates in `lib/clast/prompts/`. The only existing check is
`test/test-clast.sh`, which greps each SKILL.md for a hardcoded list of expected
plumbing invocations — it never reads the CLI, so it cannot catch flag drift.

Close every divergence the 2026-07-11 parity audit turned up, then land a guard
that keeps the two porcelains from drifting again.

## What triggered this

`clast wake --auto` was added to the CLI (PR #45) while `skills/wake/SKILL.md`
still told the model that unattended curation was *"not a v1 feature — the
friction is intentional."* The two porcelains directly contradicted each other,
and no test noticed. PR #45 closes that specific gap (Auto mode mirrored into
the skill + a static assert). This initiative covers everything else the audit
found.

## Scope — the audited divergences

Each is tracked as a sub-issue of BDS-82:

- **BDS-83** — `brief`: the skill inlines its own synthesis prompt instead of
  reading the shared `lib/clast/prompts/brief-*.md` templates. Highest
  silent-drift risk in the audit and the smallest fix; do it first.
- **BDS-84** — `wake`: the CLI ⇄ `/wake` skill divergences that predate PR #45
  (scan-window default, the promote-decision flow, and related gaps).
- **BDS-85** — decide plugin coverage for `clast retro` and `clast undismiss`:
  the CLI has four subcommands, the plugin has two skills. A decision, not
  (necessarily) an implementation.
- **BDS-86** — give `clast brief` a `--help` / usage heredoc. `clast retro`
  already has one; the guard in BDS-89 diffs `clast <cmd> --help` against a
  manifest, so every subcommand needs a machine-readable usage surface.
- **BDS-87** — docs: document the porcelain flags and fix the stale
  "`clast wake` is interactive and can't run from cron" claims, which become
  factually wrong the moment PR #45 merges.
- **BDS-88** — plugin hygiene: `.claude-plugin/plugin.json` version drift
  (`0.1.0` vs `package.json`'s `0.0.6`, uncaught because
  `contrib/check-version-sync.sh` only compares `package.json` ↔ `flake.nix`),
  stray wip artifacts, and stale SKILL copies in `plugin.md`.
- **BDS-89** — the CLI ⇄ skill parity drift guard. The issue that prevents all
  the others from recurring.

## Confirmed decisions (do not relitigate)

- The two porcelains stay two porcelains. Parity is enforced by a guard, not by
  collapsing the CLI and the plugin into one surface.
- Prompts are shared via `lib/clast/prompts/`; a porcelain that inlines its own
  copy of a shared prompt is a bug (that is BDS-83).
- BDS-89 lands **last**. A parity guard can only assert over surfaces once
  those surfaces are actually true, so every other sub-issue closes first.
- BDS-86 is a hard prerequisite for BDS-89 — the guard reads `--help` output.
- ~~PR #45 already closed the `wake --auto` skill contradiction; it is not in
  scope here.~~ **FALSE — retracted 2026-07-12.** See "Blocked on PR #45" below.
  This premise came from BDS-82's own description and does not hold: PR #45 is
  still **open and unmerged**. Nothing in the merged tree closes that gap.
- **The CLI is the reference; the skill conforms to it.** (Beau, 2026-07-12.)
  Wherever the CLI and a skill disagree, the default is to change the *skill*.
  The two surfaces started in sync and drifted because development focused on
  the CLI, so the skill is simply behind — the divergences are lag, not design.
  This settles BDS-84's "make the CLI match the skill, or the skill match the
  CLI" table: **make the skill match the CLI**, row by row. Deviating from this
  default requires an explicit, stated reason — and a `cli-only` allowlist entry
  in the BDS-89 guard so it stays honest.
- **`clast retro` gets a plugin skill. `clast undismiss` does not.** (Beau,
  2026-07-12.) This settles BDS-85. `retro` is mirrored as a skill; `undismiss`
  is declared CLI-only and goes on the BDS-89 guard's allowlist with that stated
  reason, so the guard stops flagging it. Consequence for BDS-89: the guard's
  parity manifest covers **three** mirrored subcommands (`wake`, `brief`,
  `retro`) plus one `cli-only` entry (`undismiss`).
- **Issue lifecycle**: an issue moves to **In Review** in Linear once the last
  commit for it is pushed (not Done — Done is the human's merge gate).

## Constraints

- `make test` and `make lint` pass before every commit; conventional commit
  prefixes (`feat:`, `fix:`, `docs:`, ...).
- AGENTS.md forbids touching `docs/` without an explicit request — which is why
  `--auto` shipped with zero doc coverage. BDS-87 is that explicit request.
- BDS-83, BDS-84, BDS-86, and BDS-88 look mutually independent (disjoint files,
  no ordering between them) and are candidates for parallel lanes. Confirm the
  file-overlap assumption before laning them; when in doubt, sequence.

## ~~Blocked on PR #45~~ — RESOLVED 2026-07-12: Beau merged it

**Beau merged PR #45** (merge commit `d4a27e5`). Verified on `origin/main`:
`wake.bash` now has `_clast_wake_usage` and `--auto`, and
`skills/wake/SKILL.md` no longer claims auto-curation is "not a v1 feature" —
it documents `--auto`. **BDS-82's premise is now TRUE**, and the three steps
that rested on it are UNPARKED: step-03 (BDS-84), step-06 (BDS-87), step-07
(BDS-89).

Two consequences carry forward:
- **BDS-84's drift table is valid as written** — it was authored for a post-#45
  world, and that is now the world. `--auto` is mirrored; the remaining rows are
  the genuine pre-#45 divergences.
- **BDS-89's guard now has a `wake --help` to diff.** BDS-86 was *not* the only
  `--help` prerequisite (see below), but both `brief` and `wake` now have one, so
  the guard can cover all of `wake`, `brief`, `retro` + the `undismiss` allowlist
  entry.

Any branch doing this work must be **rebased onto `main` at or after `d4a27e5`**
before touching wake — otherwise it is coding against a tree where `--auto` does
not exist.

The original finding is preserved below for the record.

---

## Blocked on PR #45 — NEEDS BEAU (found 2026-07-12, during step-02) — NOW RESOLVED, see above

**BDS-82's premise is false.** It says *"PR #45 now closes that specific gap
(Auto mode mirrored into the skill + a static assert)"* and the sub-issues are
written on top of that. Verified against the repo:

- PR #45 (`feat(wake): add --auto for non-interactive curation`, branch
  `feat/wake-auto`) is **OPEN, `mergedAt: null`**.
- `lib/clast/clast-porcelain-subcommands/wake.bash` on `main` and on this branch
  has **zero** arg parsing — no `_clast_wake_usage`, no `--auto`, no `--help`.
- `skills/wake/SKILL.md:228` **still** says unattended curation is *"not a v1
  feature… The friction is intentional."*

So the contradiction BDS-82 was opened to chase is still live, and three steps
rest on a premise that isn't true:

- **step-03 / BDS-84** — framed as the divergences that *"predate PR #45"* and
  states *"PR #45 mirrored `--auto` into the skill."* Neither is true. Its drift
  table is therefore incomplete: `--auto` itself is still a live divergence.
- **step-06 / BDS-87** — its docs work is triggered by *"the moment PR #45
  merges."* That moment hasn't happened.
- **step-07 / BDS-89** — assumes *"`wake` got one in PR #45"* when listing which
  subcommands already have `--help`. `wake` does **not** have one. If #45 never
  lands, `wake` needs its own `--help` before the guard can diff it, which makes
  BDS-86 not the only `--help` prerequisite.

**The decision is Beau's** (merge #45 first, rebase this initiative on top of it,
or absorb #45's scope into BDS-84). Do not paper over it and do not silently
redesign the steps.

~~**Interim sequencing (Orchestrator, 2026-07-12):** the loop runs **02 → 04 →
05** and then **holds**; steps 03, 06 and 07 stay parked until Beau rules on
#45.~~ **Superseded — Beau merged #45.** Full sequencing resumes: **04 → 05 →
rebase onto `main` (≥ `d4a27e5`) → 03 → 06 → 07**, with the guard (07) last as
always.

## Open questions

- ~~BDS-85: do `retro` and `undismiss` get plugin skills?~~ **Settled
  2026-07-12** — see Confirmed decisions: `retro` yes, `undismiss` no.
- Does the BDS-89 guard belong in `test/test-clast.sh`, in a new `contrib/`
  check, or in both (test + pre-commit)?