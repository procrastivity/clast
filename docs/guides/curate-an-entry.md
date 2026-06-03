# Curate an entry by hand

`/day-wakeup` (in the plugin) is the normal way to curate. This guide covers
the CLI-only path: producing a journal entry from a captured session without
any LLM in the loop.

## Pick a session

```sh
clast sessions --since -7d                                    # human-readable
clast sessions --since -7d --json | jq -r '.[] | "\(.session_id)  \(.project)  \(.start)"'
```

Grab the UUID of the session you want to curate.

## Inspect it

```sh
clast show <session-uuid>            # metadata only
clast show <session-uuid> --full     # metadata + first/last turns
```

The `--full` view is what you'll skim while writing the entry.

## Write the entry

```sh
cat > /tmp/entry.md <<'EOF'
# Session: Short human-readable title

## Goal
One sentence describing what this session was trying to accomplish.

## What shipped
- Bullet list of what actually got done.

## Issues + fixes
- **Issue:** what broke. **Fix:** what resolved it.

## Open threads
- Anything still unfinished or deferred.
EOF

clast entries write \
  --session <session-uuid> \
  --slug short-kebab-slug \
  --tags tag1,tag2 \
  --title "Short human-readable title" \
  --body-from /tmp/entry.md
```

Or pipe via stdin:

```sh
clast entries write --session <uuid> --slug short-slug --body-stdin < /tmp/entry.md
```

`clast entries write` looks up the session in the manifest, composes the
frontmatter from the captured snapshot and the registry, and writes
`entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md` atomically.

## Read it back

```sh
clast entries                              # list all
clast entries --project <slug>             # filter
clast entries --since -7d                  # window
clast entries read <entry-filename>.md     # cat one
```

## See also

- [`reference/entry-frontmatter.md`](../reference/entry-frontmatter.md) — full
  field-by-field frontmatter schema and conventional body sections.
- [`reference/cli.md#clast-entries`](../reference/cli.md#clast-entries) — write
  command flags and exit codes.
- [`guides/morning-briefing.md`](./morning-briefing.md) — a worked
  `/day-wakeup` walkthrough (uses the plugin).
