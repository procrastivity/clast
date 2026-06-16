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

## When you don't need cron

If you installed the Claude Code plugin, its `SessionStart` hook already runs
`clast-plumbing snapshot` in the background each time you open Claude Code. Cron is
useful when you want capture to happen even on days you never start a session.

See also: [Automate with systemd](./automate-with-systemd.md).
