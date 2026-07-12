# Automate capture with cron

`clast-plumbing snapshot` is idempotent and silent on no-op, so it's safe to run from
cron as frequently as you like. Use this when you want capture to happen
without installing the Claude Code plugin (or in addition to it).

## Quick start

```sh
crontab -l | { cat; cat examples/cron/crontab.sample; } | crontab -
```

The sample appends a single hourly job that runs `clast-plumbing snapshot` at five past
the hour:

```cron
5 * * * * /usr/local/bin/clast-plumbing snapshot >/dev/null 2>&1
```

Edit the path to `clast` if you installed somewhere other than `/usr/local/bin`.

## Cadence options

The sample file ([`examples/cron/crontab.sample`](../../examples/cron/crontab.sample))
includes commented-out alternatives:

```cron
# Every 15 minutes for active users moving between many sessions.
*/15 * * * * /usr/local/bin/clast-plumbing snapshot >/dev/null 2>&1

# Once daily for hands-off archival.
10 4 * * * /usr/local/bin/clast-plumbing snapshot >/dev/null 2>&1
```

Pick what matches your appetite. Idempotence means there's no cost to running
more often than strictly needed.

## Curating unattended

`clast wake --auto` is also safe to run from cron: it skips the interactive
triage menu and the per-session accept/edit/dismiss prompt entirely, never
blocks on a tty, and suppresses trivial drafts via `CLAST_WAKE_AUTO_MIN_CHARS`
(default `60`) so short or junk drafts don't pile up unreviewed.

```cron
0 6 * * * /usr/local/bin/clast wake --auto >/dev/null 2>&1
```

**Cron does not source your login shell's profile.** `clast wake` needs three
env vars — `CLAST_LLM_BASE_URL`, `CLAST_LLM_API_KEY`, `CLAST_LLM_MODEL` — and
if they're only set in your `.bashrc`/`.zshrc`, cron never sees them. Set them
explicitly, either as `CLAST_LLM_*=...` lines at the top of the crontab or via
a wrapper script that sources your profile before invoking `clast`. Skip this
and the job doesn't error — it just silently no-ops, which is the most common
way this kind of cron job goes unnoticed.

See [`## clast wake`](./run-without-claude-code.md#clast-wake--curate-the-day)
for the full `--auto` flag and env-var reference.

## When you don't need cron

If you installed the Claude Code plugin, its `SessionStart` hook already runs
`clast-plumbing snapshot` in the background each time you open Claude Code. Cron is
useful when you want capture to happen even on days you never start a session.

See also: [Automate with systemd](./automate-with-systemd.md).
