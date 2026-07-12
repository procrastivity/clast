# Automate capture with a systemd user timer

Same idea as [Automate with cron](./automate-with-cron.md), but using a
systemd user timer instead. Useful on Linux desktops where you'd rather not
run cron, or in WSL2 with systemd enabled.

## Install

```sh
cp examples/cron/clast-snapshot.service ~/.config/systemd/user/
cp examples/cron/clast-snapshot.timer   ~/.config/systemd/user/

# Edit ExecStart in the .service if clast is not at /usr/local/bin/clast.
systemctl --user daemon-reload
systemctl --user enable --now clast-snapshot.timer
```

## Verify

```sh
systemctl --user status clast-snapshot.timer
systemctl --user list-timers clast-snapshot.timer
journalctl --user -u clast-snapshot.service -n 20
```

## Cadence

The default timer fires five minutes after boot and then every hour:

```ini
[Timer]
OnBootSec=5min
OnUnitActiveSec=1h
Persistent=true
```

Edit `OnUnitActiveSec` to change the interval. Because `clast-plumbing snapshot` is
idempotent, more frequent runs are safe — they're free no-ops when there's
nothing new.

## Curating unattended

`clast wake --auto` is also safe to run from a timer: it skips the interactive
triage menu and the per-session accept/edit/dismiss prompt entirely, never
blocks on a tty, and suppresses trivial drafts via `CLAST_WAKE_AUTO_MIN_CHARS`
(default `60`) so short or junk drafts don't pile up unreviewed.

Reuse the same `OnBootSec`/`OnUnitActiveSec` cadence pattern as
`clast-snapshot.timer` above, but point `ExecStart` at `clast wake --auto`
instead:

```ini
[Service]
ExecStart=/usr/local/bin/clast wake --auto
```

**systemd user units do not inherit your login shell's environment.**
`clast wake` needs three env vars — `CLAST_LLM_BASE_URL`, `CLAST_LLM_API_KEY`,
`CLAST_LLM_MODEL` — and if they're only set in your `.bashrc`/`.zshrc`, the
unit never sees them. Supply them explicitly in the `.service` file with
`Environment=` lines or an `EnvironmentFile=` pointing at a file that sets
them, for example:

```ini
[Service]
EnvironmentFile=%h/.config/clast/wake.env
ExecStart=/usr/local/bin/clast wake --auto
```

Skip this and the unit doesn't error — it just silently no-ops, which is the
most common way this kind of timer goes unnoticed.

See [`## clast wake`](./run-without-claude-code.md#clast-wake--curate-the-day)
for the full `--auto` flag and env-var reference.

## Unit files

- [`examples/cron/clast-snapshot.service`](../../examples/cron/clast-snapshot.service)
- [`examples/cron/clast-snapshot.timer`](../../examples/cron/clast-snapshot.timer)
