# First snapshot

A linear "press these buttons" tour for a fresh CLI install (no plugin / no
`SessionStart` hook yet). Stop at any point; each step is read-only unless
noted otherwise.

## 0. Sanity check

```sh
clast --version
clast whereami      # where is the journal? where is the config?
clast doctor        # is everything wired up?
```

You should see your journal root path. Nothing has been written yet.

## 1. Capture (the one write you need to bootstrap)

```sh
clast snapshot --dry-run --json | jq   # preview what would be captured
clast snapshot                          # actually capture
```

First real run — copies any existing Claude Code session JSONLs into clast's
journal. Idempotent: run it twice, the second is a silent no-op.

## 2. Browse what got captured

```sh
clast projects                   # which projects had activity today
clast projects --since -7d       # …in the last week
clast sessions --since -7d       # one line per session
clast stats                      # one-line activity summary for today
clast stats --since -7d          # weekly rollup
```

This is the "oh, it's a journal" moment.

## 3. Inspect a single session

```sh
clast sessions --since -7d --json | jq -r '.[0].uuid'   # grab a uuid
clast show <session-uuid>            # metadata
clast show <session-uuid> --full     # metadata + first/last turns
```

## 4. Curate (the value-add — durable, human-edited notes)

```sh
clast entries                                            # list curated entries (empty at first)
printf 'First curated note — testing clast.\n' \
  | clast entries write --session <uuid> --slug smoke-test --body-stdin
clast entries                                            # now it's listed
clast entries read <the-file-it-wrote>.md                # read it back
```

See [`guides/curate-an-entry.md`](../guides/curate-an-entry.md) for the
end-to-end curation workflow.

## 5. Drop a breadcrumb (lightweight, append-only)

```sh
clast breadcrumb --project clast 'try the wakeup skill next'
clast breadcrumb --global 'clast first impressions: snappy'
clast breadcrumb --read --project clast
clast breadcrumb --read --global
```

See [`guides/use-breadcrumbs.md`](../guides/use-breadcrumbs.md) for when to use
which.

## 6. Automate capture (optional, no plugin needed)

Wire `clast snapshot` into cron or a systemd timer. This is the "CLI-only
equivalent" of the `SessionStart` hook — keeps the journal warm without
installing the plugin.

- [Automate with cron](../guides/automate-with-cron.md)
- [Automate with systemd](../guides/automate-with-systemd.md)

## 7. When ready, install the plugin

See [Install the plugin](./install-the-plugin.md).
