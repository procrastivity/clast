# clast-dismissed-lib.bash — dismissed session tracking
#
# Thin layer over $(clast_journal_dir)/.dismissed.jsonl. Each line
# records a session_id + timestamp. Used by sessions.bash to exclude
# throwaway sessions from query results.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash

if [[ -n "${_CLAST_DISMISSED_LIB_SOURCED:-}" ]]; then
  return 0
fi
_CLAST_DISMISSED_LIB_SOURCED=1

clast_dismissed_path() {
  printf '%s\n' "$(clast_journal_dir)/.dismissed.jsonl"
}

# clast_dismissed_add <session-id> [reason]
clast_dismissed_add() {
  local session_id="$1"
  local reason="${2:-}"
  if [[ -z "$session_id" ]]; then
    clast_log_error "clast_dismissed_add: empty session_id"
    return 2
  fi

  local dismissed_file
  dismissed_file="$(clast_dismissed_path)"

  local now
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  local line
  line="$(jq -cn \
    --arg sid "$session_id" \
    --arg ts "$now" \
    --arg reason "$reason" \
    '{session_id: $sid, dismissed_at: $ts, reason: (if $reason == "" then null else $reason end)}')"

  printf '%s\n' "$line" >> "$dismissed_file"
}

# clast_dismissed_set — populate an associative array with dismissed IDs.
# Usage: declare -A dismissed=(); clast_dismissed_set dismissed
clast_dismissed_set() {
  local -n _ref=$1
  local dismissed_file
  dismissed_file="$(clast_dismissed_path)"

  if [[ ! -f "$dismissed_file" ]]; then
    return 0
  fi

  # Single jq pass over the whole file. Previously this forked one jq per
  # line, which dominated when the dismissed log grew large. `fromjson?`
  # silently drops malformed lines, matching the old per-line tolerance.
  local sid
  while IFS= read -r sid; do
    [[ -n "$sid" ]] && _ref["$sid"]=1
  done < <(jq -rR 'fromjson? | .session_id // empty' "$dismissed_file" 2>/dev/null)
}

# clast_dismissed_check <session-id> — exit 0 if dismissed, 1 if not.
clast_dismissed_check() {
  local session_id="$1"
  local dismissed_file
  dismissed_file="$(clast_dismissed_path)"

  if [[ ! -f "$dismissed_file" ]]; then
    return 1
  fi

  grep -q "\"session_id\":\"$session_id\"" "$dismissed_file" 2>/dev/null
}

# clast_dismissed_remove <session-id> — drop all records for a session,
# reversing a dismissal. Prints the number of records removed to stdout.
# Returns 0 if anything was removed, 1 if the session was not dismissed.
clast_dismissed_remove() {
  local session_id="$1"
  if [[ -z "$session_id" ]]; then
    clast_log_error "clast_dismissed_remove: empty session_id"
    return 2
  fi

  local dismissed_file
  dismissed_file="$(clast_dismissed_path)"

  if [[ ! -f "$dismissed_file" ]]; then
    printf '0\n'
    return 1
  fi

  # Count matches, then rewrite the log without them. A match is a
  # well-formed JSON *object* whose session_id equals the target;
  # `try fromjson catch null` turns unparseable lines into null so they
  # never match, and the `type == "object"` guard keeps non-object JSON
  # (numbers, strings) from erroring on `.session_id`. This mirrors the
  # tolerant read in clast_dismissed_set.
  local removed
  removed="$(jq -rRn --arg sid "$session_id" '
    [ inputs
      | (try fromjson catch null) as $obj
      | select(($obj | type) == "object" and $obj.session_id == $sid)
    ] | length
  ' "$dismissed_file" 2>/dev/null)" || removed=0
  [[ -z "$removed" ]] && removed=0

  if (( removed == 0 )); then
    printf '0\n'
    return 1
  fi

  local tmp
  tmp="$(mktemp "${dismissed_file}.XXXXXX")" || {
    clast_log_error "clast_dismissed_remove: failed to create temp file"
    return 2
  }
  # Emit every line except well-formed objects matching the target id.
  # Malformed lines parse to null (type != "object") and so are preserved
  # verbatim rather than silently dropped. Rewrite goes through a temp file
  # + mv so a crash mid-write can't truncate the log.
  if jq -Rr --arg sid "$session_id" '
    . as $line
    | (try ($line | fromjson) catch null) as $obj
    | if ($obj | type) == "object" and $obj.session_id == $sid
      then empty
      else $line
      end
  ' "$dismissed_file" >"$tmp" 2>/dev/null; then
    if ! mv "$tmp" "$dismissed_file"; then
      rm -f "$tmp"
      clast_log_error "clast_dismissed_remove: failed to replace log"
      return 2
    fi
  else
    rm -f "$tmp"
    clast_log_error "clast_dismissed_remove: rewrite failed"
    return 2
  fi

  printf '%s\n' "$removed"
  return 0
}
