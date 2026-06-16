# Query recipes

Every read command supports `--json`. Pair with `jq` for sharper queries than
the default table view.

## Sessions

```sh
# All UUIDs from the past week
clast-plumbing sessions --since -7d --json | jq -r '.[].session_id'

# Only uncurated sessions
clast-plumbing sessions --since -7d --json | jq -r '.[] | select(.curated == false) | .session_id'

# Total approximate message count this week
clast-plumbing sessions --since -7d --json | jq '[.[].msg_count_approx] | add'

# Sessions on a specific branch
clast-plumbing sessions --since -30d --json | jq -r '.[] | select(.branch == "main") | "\(.start)  \(.project)"'
```

## Projects

```sh
# Projects sorted by session count (desc)
clast-plumbing projects --since -7d --json | jq -r 'sort_by(-.session_count) | .[] | "\(.session_count)\t\(.slug)"'

# Unregistered paths that had activity
clast-plumbing projects --since -30d --unregistered --json | jq -r '.[].path'
```

## Entries

```sh
# All entries tagged a certain way
clast-plumbing entries --json | jq -r '.[] | select(.tags | index("mysql")) | .path'

# Entry titles for a project, newest first
clast-plumbing entries --project xesapps --json | jq -r 'sort_by(.date + .time) | reverse | .[] | "\(.date)  \(.title)"'

# Count entries per project this month
clast-plumbing entries --since -30d --json | jq -r 'group_by(.project) | .[] | "\(length)\t\(.[0].project)"'
```

## Stats

```sh
# Just the curated ratio for the last week
clast-plumbing stats --since -7d --json | jq '.curated_sessions / .total_sessions'
```

## Breadcrumbs

```sh
# Every breadcrumb file written today
clast-plumbing breadcrumb --list --json | jq -r '.[] | "\(.line_count)\t\(.project)\t\(.path)"'
```

## See also

- [`reference/cli.md`](../reference/cli.md) — the `--json` output schema for
  each command.
