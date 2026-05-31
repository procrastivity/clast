# clast-manifest-lib.bash — manifest read/append/lookup/iterate/rebuild
#
# Sourced after clast-lib.bash. The manifest is an append-only JSONL log at
# $(clast_journal_dir)/.manifest.jsonl, one line per capture event. Per
# docs/cli-contract.md#manifest-line each line has seven required fields:
# session_id, source, snapshot, captured_at, source_mtime, source_size,
# day_bucket. Lookups use "most recent line wins" semantics.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash

if [[ -n "${_CLAST_MANIFEST_LIB_SOURCED:-}" ]]; then
  return 0
fi
_CLAST_MANIFEST_LIB_SOURCED=1

# clast_manifest_path — single chokepoint so CLAST_JOURNAL_DIR override
# (via clast_journal_dir) redirects every read/write in this file.
clast_manifest_path() {
  printf '%s\n' "$(clast_journal_dir)/.manifest.jsonl"
}

# _clast_manifest_now_iso — current UTC time, ISO 8601 with Z suffix.
# Honors CLAST_NOW_EPOCH (test-only freeze hook from clast-lib.bash).
_clast_manifest_now_iso() {
  local epoch="${CLAST_NOW_EPOCH:-$(date +%s)}"
  date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ
}

# clast_manifest_append <session-id> <source> <snapshot> <source-mtime> <source-size> <day-bucket>
clast_manifest_append() {
  if [[ $# -ne 6 ]]; then
    clast_log_error "clast_manifest_append: expected 6 args, got $#"
    return 2
  fi
  local session_id="$1" source="$2" snapshot="$3" source_mtime="$4" source_size="$5" day_bucket="$6"
  local field
  for field in session_id source snapshot source_mtime source_size day_bucket; do
    if [[ -z "${!field}" ]]; then
      clast_log_error "clast_manifest_append: empty field '$field'"
      return 2
    fi
  done
  if ! [[ "$source_size" =~ ^[0-9]+$ ]]; then
    clast_log_error "clast_manifest_append: source_size must be a non-negative integer, got '$source_size'"
    return 2
  fi

  local journal_dir
  journal_dir="$(clast_journal_dir)"
  if ! mkdir -p "$journal_dir"; then
    clast_log_error "clast_manifest_append: failed to create '$journal_dir'"
    return 1
  fi

  local captured_at line
  captured_at="$(_clast_manifest_now_iso)"
  line="$(jq -c -n \
    --arg session_id "$session_id" \
    --arg source "$source" \
    --arg snapshot "$snapshot" \
    --arg captured_at "$captured_at" \
    --arg source_mtime "$source_mtime" \
    --argjson source_size "$source_size" \
    --arg day_bucket "$day_bucket" \
    '{session_id: $session_id, source: $source, snapshot: $snapshot, captured_at: $captured_at, source_mtime: $source_mtime, source_size: $source_size, day_bucket: $day_bucket}')" || {
    clast_log_error "clast_manifest_append: jq failed to build manifest line"
    return 1
  }

  # Append-only single-line write — crash-safe at line boundary per
  # docs/overview.md#cross-machine-considerations. Do NOT use
  # clast_atomic_write here: it rewrites the whole file and would clobber
  # concurrent appends from another machine.
  local manifest_path
  manifest_path="$(clast_manifest_path)"
  if ! printf '%s\n' "$line" >>"$manifest_path" 2>/dev/null; then
    clast_log_error "clast_manifest_append: failed to append to '$manifest_path'"
    return 1
  fi
}

# clast_manifest_lookup <session-id>
#   Print the most recent manifest line for <session-id>. Exit 1 if no
#   match (including when the manifest file does not exist).
clast_manifest_lookup() {
  if [[ $# -ne 1 ]]; then
    clast_log_error "clast_manifest_lookup: expected 1 arg, got $#"
    return 2
  fi
  local sid="$1" path
  path="$(clast_manifest_path)"
  if [[ ! -f "$path" ]]; then
    return 1
  fi
  # Manifest is append-only so file order is time order; tac to scan from
  # newest backward, fromjson? skips garbage lines silently, head -n1 stops
  # at the first match.
  local line
  line="$(tac "$path" | jq -cR --arg sid "$sid" 'fromjson? | select(.session_id == $sid)' | head -n1)"
  if [[ -z "$line" ]]; then
    return 1
  fi
  printf '%s\n' "$line"
}

# clast_manifest_has_capture <session-id> <source-mtime>
#   Exit 0 if a manifest line exists for this (session_id, source_mtime)
#   pair; exit 1 otherwise. Fast-path predicate for `clast snapshot`.
clast_manifest_has_capture() {
  if [[ $# -ne 2 ]]; then
    clast_log_error "clast_manifest_has_capture: expected 2 args, got $#"
    return 2
  fi
  local sid="$1" mtime="$2" path
  path="$(clast_manifest_path)"
  if [[ ! -f "$path" ]]; then
    return 1
  fi
  local match
  match="$(jq -cR --arg sid "$sid" --arg mtime "$mtime" \
    'fromjson? | select(.session_id == $sid and .source_mtime == $mtime)' \
    "$path" | head -n1)"
  if [[ -z "$match" ]]; then
    return 1
  fi
  return 0
}

# clast_manifest_iterate <jq-filter-body>
#   Stream every manifest line whose parsed object matches the supplied
#   select() body (e.g. '.day_bucket == "2026-05-30"'). Malformed lines
#   are silently skipped via fromjson?. Missing manifest = no output, 0.
clast_manifest_iterate() {
  if [[ $# -ne 1 ]]; then
    clast_log_error "clast_manifest_iterate: expected 1 arg, got $#"
    return 2
  fi
  local filter="$1" path
  path="$(clast_manifest_path)"
  if [[ ! -f "$path" ]]; then
    return 0
  fi
  jq -cR 'fromjson? | select('"$filter"')' "$path"
}

# clast_manifest_rebuild_from_disk
#   Walk $(clast_journal_dir)/transcripts/<day>/<segment>/<uuid>.jsonl and
#   write a best-effort manifest atomically. For doctor --fix (step 10).
#   source / source_size are unrecoverable from the snapshot alone, so
#   they're emitted as null / 0 — round-trips through lookup, schema
#   validators can still flag the lossy lines.
clast_manifest_rebuild_from_disk() {
  local journal_dir manifest_path transcripts_root tmp_file
  journal_dir="$(clast_journal_dir)"
  manifest_path="$(clast_manifest_path)"
  transcripts_root="$journal_dir/transcripts"

  if ! mkdir -p "$journal_dir"; then
    clast_log_error "clast_manifest_rebuild_from_disk: failed to create '$journal_dir'"
    return 1
  fi

  tmp_file="$(mktemp "$journal_dir/.manifest.jsonl.rebuild.XXXXXX")" || {
    clast_log_error "clast_manifest_rebuild_from_disk: mktemp failed"
    return 1
  }

  local count=0
  if [[ -d "$transcripts_root" ]]; then
    local snapshot day session_id mtime_epoch mtime_iso line
    # source / source_size are unrecoverable: the snapshot file's size is
    # the captured copy's size, not the original source's size at capture
    # time, and the source path is not encoded in the snapshot itself.
    while IFS= read -r snapshot; do
      [[ -z "$snapshot" ]] && continue
      session_id="$(basename "$snapshot" .jsonl)"
      day="$(basename "$(dirname "$(dirname "$snapshot")")")"
      if ! mtime_epoch="$(stat -c %Y "$snapshot" 2>/dev/null || stat -f %m "$snapshot" 2>/dev/null)" || [[ -z "$mtime_epoch" ]]; then
        clast_log_error "clast_manifest_rebuild_from_disk: stat failed for '$snapshot'"
        rm -f "$tmp_file"
        return 1
      fi
      if ! mtime_iso="$(date -u -d "@$mtime_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)" || [[ -z "$mtime_iso" ]]; then
        clast_log_error "clast_manifest_rebuild_from_disk: date conversion failed for '$snapshot' (epoch '$mtime_epoch')"
        rm -f "$tmp_file"
        return 1
      fi
      line="$(jq -c -n \
        --arg session_id "$session_id" \
        --arg snapshot "${snapshot#"$journal_dir/"}" \
        --arg captured_at "$mtime_iso" \
        --arg source_mtime "$mtime_iso" \
        --arg day_bucket "$day" \
        '{session_id: $session_id, source: null, snapshot: $snapshot, captured_at: $captured_at, source_mtime: $source_mtime, source_size: 0, day_bucket: $day_bucket}')" || {
        clast_log_error "clast_manifest_rebuild_from_disk: jq failed for '$snapshot'"
        rm -f "$tmp_file"
        return 1
      }
      if ! printf '%s\n' "$line" >>"$tmp_file" 2>/dev/null; then
        clast_log_error "clast_manifest_rebuild_from_disk: failed to write to '$tmp_file'"
        rm -f "$tmp_file"
        return 1
      fi
      count=$((count + 1))
    done < <(find "$transcripts_root" -mindepth 3 -maxdepth 3 -type f -name '*.jsonl' | sort)

    # Sort the temp file by captured_at ascending. The find|sort above sorts
    # by path; re-sort by captured_at to honor the documented invariant.
    if (( count > 0 )); then
      local sorted
      sorted="$(jq -sc 'sort_by(.captured_at) | .[]' "$tmp_file")" || {
        clast_log_error "clast_manifest_rebuild_from_disk: failed to sort rebuilt manifest"
        rm -f "$tmp_file"
        return 1
      }
      if ! printf '%s\n' "$sorted" >"$tmp_file" 2>/dev/null; then
        clast_log_error "clast_manifest_rebuild_from_disk: failed to write sorted manifest to '$tmp_file'"
        rm -f "$tmp_file"
        return 1
      fi
    fi
  fi

  if ! mv -f "$tmp_file" "$manifest_path"; then
    clast_log_error "clast_manifest_rebuild_from_disk: failed to rename onto '$manifest_path'"
    rm -f "$tmp_file"
    return 1
  fi

  clast_log_info "manifest rebuilt: $count line(s)"
  return 0
}
