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

## Unit files

- [`examples/cron/clast-snapshot.service`](../../examples/cron/clast-snapshot.service)
- [`examples/cron/clast-snapshot.timer`](../../examples/cron/clast-snapshot.timer)
