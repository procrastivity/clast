# clast-subcommands/snapshot.bash — `clast snapshot`.
#
# Walks $(clast_projects_dir)/<segment>/<uuid>.jsonl, copies each new or
# modified session into $(clast_journal_dir)/transcripts/<day>/<segment>/,
# and appends one manifest line per capture. Idempotent: re-running a file
# whose (session_id, source_mtime) is already in the manifest is a no-op.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash

_clast_snapshot_usage() {
  cat <<'EOF'
Usage: clast snapshot [--dry-run] [--since TIMESTAMP] [--include-segment SEG]

Capture new Claude Code transcripts into the journal.

Flags:
  --dry-run               Preview only; do not copy or write manifest.
  --since TIMESTAMP       Skip sources with mtime < TIMESTAMP (ISO 8601 or
                          any string GNU `date -d` understands; e.g. -1d).
  --include-segment SEG   Limit scan to SEG (repeatable). Value must start
                          with '-' (segments always do).
  -h, --help              Print this usage and exit.
EOF
}

# _clast_snapshot_bucket_for_epoch <epoch>
#   Mirror of clast_today's cutoff math against an arbitrary epoch. Local
#   time per docs/explanation/conventions.md.
_clast_snapshot_bucket_for_epoch() {
  local epoch="$1"
  local cutoff="${CLAST_DAY_CUTOFF:-04:00}"
  local h="${cutoff%%:*}" m="${cutoff##*:}"
  h=$((10#$h))
  m=$((10#$m))
  local off=$((h * 3600 + m * 60))
  # GNU `date -d` — BSD date not supported, per overview.md.
  date -d "@$((epoch - off))" +%Y-%m-%d
}

clast_cmd_snapshot() {
  local dry_run=0
  local since_epoch=""
  local -a include_segments=()

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --dry-run)
        dry_run=1; shift ;;
      --since)
        if [[ $# -lt 2 ]]; then
          clast_log_error "snapshot: --since requires a value"; return 2
        fi
        if ! since_epoch="$(date -d "$2" +%s 2>/dev/null)" || [[ -z "$since_epoch" ]]; then
          clast_log_error "snapshot: cannot parse --since '$2'"; return 2
        fi
        shift 2 ;;
      --since=*)
        local _v="${1#*=}"
        if ! since_epoch="$(date -d "$_v" +%s 2>/dev/null)" || [[ -z "$since_epoch" ]]; then
          clast_log_error "snapshot: cannot parse --since '$_v'"; return 2
        fi
        shift ;;
      --include-segment)
        if [[ $# -lt 2 ]]; then
          clast_log_error "snapshot: --include-segment requires a value"; return 2
        fi
        if [[ "$2" != -* ]]; then
          clast_log_error "snapshot: --include-segment value must start with '-' (got '$2')"; return 2
        fi
        include_segments+=("$2"); shift 2 ;;
      --include-segment=*)
        local _v="${1#*=}"
        if [[ "$_v" != -* ]]; then
          clast_log_error "snapshot: --include-segment value must start with '-' (got '$_v')"; return 2
        fi
        include_segments+=("$_v"); shift ;;
      --json)
        # Accept the global flag positionally as well, mirroring registry.
        # Lets `clast snapshot --json` work alongside `clast --json snapshot`.
        export CLAST_JSON=1; shift ;;
      -h|--help)
        _clast_snapshot_usage; return 0 ;;
      *)
        clast_log_error "snapshot: unknown flag '$1'"; return 2 ;;
    esac
  done

  # Manifest precondition: cron-mode safety. clast_manifest_iterate uses
  # fromjson? today, so most corruption is swallowed silently — this guard
  # is the documented hook for a future strict-iterate contract (step plan
  # task 3). Leaving it in keeps the exit-4 path wired the moment iterate
  # learns to surface corruption.
  local _it_rc=0
  clast_manifest_iterate 'true' >/dev/null 2>&1 || _it_rc=$?
  if (( _it_rc != 0 )); then
    if [[ -n "${CLAST_JSON:-}" ]]; then
      printf '{"error":"manifest is corrupt","code":4}\n'
    else
      clast_log_error "snapshot: manifest is corrupt; refusing to write"
    fi
    return 4
  fi

  local projects_dir journal_dir
  projects_dir="$(clast_projects_dir)"
  journal_dir="$(clast_journal_dir)"

  local skipped=0
  local total_bytes=0
  local -a captured_lines=()
  local -a error_lines=()
  local -a captured_segments=()

  # TODO(v1.1): parallel capture across segments.
  if [[ -d "$projects_dir" ]]; then
    local source segment session_id mtime_epoch mtime_iso source_size
    local first_ts ts_epoch day_bucket
    local dest_rel dest dest_dir tmp
    while IFS= read -r -d '' source; do
      segment="$(basename "$(dirname "$source")")"

      if (( ${#include_segments[@]} > 0 )); then
        local _hit=0 _seg
        for _seg in "${include_segments[@]}"; do
          if [[ "$_seg" == "$segment" ]]; then _hit=1; break; fi
        done
        if (( _hit == 0 )); then
          skipped=$((skipped + 1))
          continue
        fi
      fi

      if ! mtime_epoch="$(stat -c %Y "$source" 2>/dev/null)" || [[ -z "$mtime_epoch" ]]; then
        error_lines+=("$(jq -cn --arg f "$source" --arg r "stat failed" '{file:$f,reason:$r}')")
        continue
      fi

      if [[ -n "$since_epoch" ]] && (( mtime_epoch < since_epoch )); then
        skipped=$((skipped + 1))
        continue
      fi

      session_id="$(basename "$source" .jsonl)"
      if ! [[ "$session_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
        error_lines+=("$(jq -cn --arg f "$source" --arg r "non-uuid filename" '{file:$f,reason:$r}')")
        continue
      fi

      mtime_iso="$(date -u -d "@$mtime_epoch" +%Y-%m-%dT%H:%M:%SZ)"
      source_size="$(stat -c %s "$source" 2>/dev/null || echo 0)"

      first_ts="$(head -n1 "$source" 2>/dev/null | jq -r '.timestamp // empty' 2>/dev/null || true)"
      if [[ -n "$first_ts" ]] && ts_epoch="$(date -d "$first_ts" +%s 2>/dev/null)" && [[ -n "$ts_epoch" ]]; then
        day_bucket="$(_clast_snapshot_bucket_for_epoch "$ts_epoch")"
      else
        # --verbose only: a malformed first line shouldn't spam cron/hook logs.
        if [[ -n "${CLAST_VERBOSE:-}" ]]; then
          clast_log_warn "snapshot: '$source' missing first-line timestamp; falling back to mtime"
        fi
        day_bucket="$(_clast_snapshot_bucket_for_epoch "$mtime_epoch")"
      fi

      # Dedup on (session_id, source_mtime). Mtime — not ctime or size —
      # is the manifest's "most recent line wins" key; Claude Code's
      # writer bumps mtime whenever a session grows.
      if clast_manifest_has_capture "$session_id" "$mtime_iso"; then
        skipped=$((skipped + 1))
        continue
      fi

      dest_rel="transcripts/$day_bucket/$segment/$session_id.jsonl"
      dest="$journal_dir/$dest_rel"

      if (( dry_run == 0 )); then
        dest_dir="$(dirname "$dest")"
        if ! mkdir -p "$dest_dir"; then
          error_lines+=("$(jq -cn --arg f "$source" --arg r "mkdir failed" '{file:$f,reason:$r}')")
          continue
        fi
        if ! tmp="$(mktemp "$dest.copy.XXXXXX" 2>/dev/null)"; then
          error_lines+=("$(jq -cn --arg f "$source" --arg r "mktemp failed" '{file:$f,reason:$r}')")
          continue
        fi
        # cp+mv, not clast_atomic_write: the helper takes content as a
        # string and would slurp a multi-megabyte session into a bash var.
        if ! cp "$source" "$tmp" 2>/dev/null; then
          rm -f "$tmp"
          error_lines+=("$(jq -cn --arg f "$source" --arg r "copy failed" '{file:$f,reason:$r}')")
          continue
        fi
        if ! mv -f "$tmp" "$dest" 2>/dev/null; then
          rm -f "$tmp"
          error_lines+=("$(jq -cn --arg f "$source" --arg r "rename failed" '{file:$f,reason:$r}')")
          continue
        fi
        # Invariant: a manifest line implies the dest file exists. If the
        # append fails the dest is an orphan; doctor (step 10) will reap
        # it, and a re-run of snapshot overwrites + retries the append.
        if ! clast_manifest_append "$session_id" "$source" "$dest_rel" "$mtime_iso" "$source_size" "$day_bucket"; then
          error_lines+=("$(jq -cn --arg f "$source" --arg r "manifest append failed" '{file:$f,reason:$r}')")
          continue
        fi
      fi

      captured_lines+=("$(jq -cn \
        --arg sid "$session_id" \
        --arg src "$source" \
        --arg snap "$dest_rel" \
        --argjson b "$source_size" \
        --arg day "$day_bucket" \
        '{session_id:$sid, source:$src, snapshot:$snap, bytes:$b, day_bucket:$day}')")
      captured_segments+=("$segment")
      total_bytes=$((total_bytes + source_size))
    done < <(find "$projects_dir" -mindepth 2 -maxdepth 2 -type f -name '*.jsonl' -print0 2>/dev/null | sort -z)
  fi

  _clast_snapshot_emit_summary "$dry_run" "$skipped" "$total_bytes" \
    captured_lines error_lines captured_segments

  if (( ${#error_lines[@]} > 0 )); then
    return 1
  fi
  return 0
}

# _clast_snapshot_emit_summary <dry_run> <skipped> <total_bytes> <caps-arr-name> <errs-arr-name> <segs-arr-name>
_clast_snapshot_emit_summary() {
  local dry_run="$1" skipped="$2" total_bytes="$3"
  local -n _caps="$4"
  local -n _errs="$5"
  local -n _segs="$6"

  if [[ -n "${CLAST_JSON:-}" ]]; then
    local caps_json='[]' errs_json='[]'
    if (( ${#_caps[@]} > 0 )); then
      caps_json="$(printf '%s\n' "${_caps[@]}" | jq -cs '.')"
    fi
    if (( ${#_errs[@]} > 0 )); then
      errs_json="$(printf '%s\n' "${_errs[@]}" | jq -cs '.')"
    fi
    jq -cn --argjson c "$caps_json" --argjson e "$errs_json" --argjson s "$skipped" \
      '{captured:$c, skipped:$s, errors:$e}'
    return 0
  fi

  # Silent no-op: load-bearing for the SessionStart hook (step 11). Re-read
  # docs/reference/cli.md#clast-snapshot before changing this.
  if (( ${#_caps[@]} == 0 && ${#_errs[@]} == 0 )); then
    return 0
  fi

  if (( dry_run == 1 )); then
    local i seg sid day
    for (( i = 0; i < ${#_caps[@]}; i++ )); do
      seg="${_segs[$i]}"
      sid="$(jq -r '.session_id' <<<"${_caps[$i]}")"
      day="$(jq -r '.day_bucket' <<<"${_caps[$i]}")"
      printf 'would capture: %s/%s → %s\n' "$seg" "$sid" "$day" >&2
    done
  elif (( ${#_caps[@]} > 0 )) && [[ -z "${CLAST_QUIET:-}" ]]; then
    declare -A counts=()
    local i seg label slug
    for (( i = 0; i < ${#_caps[@]}; i++ )); do
      seg="${_segs[$i]}"
      if slug="$(clast_registry_resolve "$seg" 2>/dev/null)" && [[ -n "$slug" ]]; then
        label="$slug"
      else
        label="$seg"
      fi
      counts["$label"]=$(( ${counts["$label"]:-0} + 1 ))
    done

    local mb
    mb="$(awk -v b="$total_bytes" 'BEGIN{printf "%.1f", b/1048576}')"
    printf 'Captured %d session(s) across %d project(s) (%s MB).\n' \
      "${#_caps[@]}" "${#counts[@]}" "$mb"

    local key cnt
    while IFS=$'\t' read -r key cnt; do
      printf '  %s: %s session(s)\n' "$key" "$cnt"
    done < <(
      for key in "${!counts[@]}"; do
        printf '%s\t%s\n' "$key" "${counts[$key]}"
      done | sort -t$'\t' -k2,2nr -k1,1
    )
  fi

  if (( ${#_errs[@]} > 0 )); then
    printf '%d error(s); see --json for details.\n' "${#_errs[@]}" >&2
  fi
}
