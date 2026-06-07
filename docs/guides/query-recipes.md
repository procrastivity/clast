# Query recipes

Every read command supports `--json`. Pair with `jq` for sharper queries than
the default table view.

## Sessions

```sh
# All UUIDs from the past week
clast sessions --since -7d --json | jq -r '.[].session_id'

# Only uncurated sessions
clast sessions --since -7d --json | jq -r '.[] | select(.curated == false) | .session_id'

# Total approximate message count this week
clast sessions --since -7d --json | jq '[.[].msg_count_approx] | add'

# Sessions on a specific branch
clast sessions --since -30d --json | jq -r '.[] | select(.branch == "main") | "\(.start)  \(.project)"'
```

## Projects

```sh
# Projects sorted by session count (desc)
clast projects --since -7d --json | jq -r 'sort_by(-.session_count) | .[] | "\(.session_count)\t\(.slug)"'

# Unregistered paths that had activity
clast projects --since -30d --unregistered --json | jq -r '.[].path'
```

## Entries

```sh
# All entries tagged a certain way
clast entries --json | jq -r '.[] | select(.tags | index("mysql")) | .path'

# Entry titles for a project, newest first
clast entries --project xesapps --json | jq -r 'sort_by(.date + .time) | reverse | .[] | "\(.date)  \(.title)"'

# Count entries per project this month
clast entries --since -30d --json | jq -r 'group_by(.project) | .[] | "\(length)\t\(.[0].project)"'
```

## Stats

```sh
# Just the curated ratio for the last week
clast stats --since -7d --json | jq '.curated_sessions / .total_sessions'
```

## Breadcrumbs

```sh
# Every breadcrumb file written today
clast breadcrumb --list --json | jq -r '.[] | "\(.line_count)\t\(.project)\t\(.path)"'
```

## See also

- [`reference/cli.md`](../reference/cli.md) — the `--json` output schema for
  each command.
