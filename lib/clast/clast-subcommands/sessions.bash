# clast-subcommands/sessions.bash — `clast sessions`.
#
# Read-only view over .manifest.jsonl: list sessions in a date window,
# optionally filtered by registry slug. See
# docs/cli-contract.md#clast-sessions.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

_clast_sessions_usage() {
  cat <<'EOF'
Usage: clast sessions [--day DATE] [--since DATE] [--until DATE] [--project SLUG]

List sessions captured in a date window.

Flags:
  --day DATE       Single-day window (default: today). Mutually exclusive
                   with --since/--until.
  --since DATE     Start of range (inclusive).
  --until DATE     End of range (inclusive).
  --project SLUG   Filter to a single registry slug.
  -h, --help       Print this usage and exit.

DATE accepts ISO (YYYY-MM-DD), `today`, `yesterday`, `last-week`,
`-Nd`, or `-Nw`. See docs/cli-contract.md#date-parsing.
EOF
}

_clast_sessions_err() {
  local msg="$1"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" '{error:$m, code:2}'
  else
    clast_log_error "sessions: $msg"
  fi
}

clast_cmd_sessions() {
  local day_filter="" since_date="" until_date=""
  local project_filter=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --day)
        if [[ $# -lt 2 ]]; then _clast_sessions_err "--day requires a value"; return 2; fi
        if ! day_filter="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_sessions_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --day=*)
        if ! day_filter="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_sessions_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --since)
        if [[ $# -lt 2 ]]; then _clast_sessions_err "--since requires a value"; return 2; fi
        if ! since_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_sessions_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --since=*)
        if ! since_date="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_sessions_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --until)
        if [[ $# -lt 2 ]]; then _clast_sessions_err "--until requires a value"; return 2; fi
        if ! until_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_sessions_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --until=*)
        if ! until_date="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_sessions_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --project)
        if [[ $# -lt 2 ]]; then _clast_sessions_err "--project requires a value"; return 2; fi
        project_filter="$2"; shift 2 ;;
      --project=*)
        project_filter="${1#*=}"; shift ;;
      -h|--help)
        _clast_sessions_usage; return 0 ;;
      --)
        shift; break ;;
      -*)
        _clast_sessions_err "unknown flag '$1'"; return 2 ;;
      *)
        _clast_sessions_err "unexpected positional '$1'"; return 2 ;;
    esac
  done

  if [[ -n "$day_filter" && ( -n "$since_date" || -n "$until_date" ) ]]; then
    _clast_sessions_err "--day is mutually exclusive with --since/--until"
    return 2
  fi

  if [[ -z "$day_filter" && -z "$since_date" && -z "$until_date" ]]; then
    day_filter="$(clast_today)"
  fi

  # `--until` defaults to `today` per docs/cli-contract.md#clast-sessions
  # when `--since` is supplied without an explicit upper bound.
  if [[ -n "$since_date" && -z "$until_date" ]]; then
    until_date="$(clast_today)"
  fi

  local filter
  filter="$(_clast_sessions_window_filter "$day_filter" "$since_date" "$until_date")"

  local journal_dir
  journal_dir="$(clast_journal_dir)"

  # Build the project segment whitelist if --project was passed.
  declare -A allowed_segs=()
  local have_project_filter=0
  if [[ -n "$project_filter" ]]; then
    have_project_filter=1
    local p seg_enc
    while IFS= read -r p; do
      [[ -z "$p" ]] && continue
      seg_enc="$(clast_encode_path "$p")"
      allowed_segs["$seg_enc"]=1
    done < <(clast_registry_list_json \
      | jq -r --arg s "$project_filter" '.[] | select(.slug == $s) | .path')
  fi

  # Most-recent manifest line per session_id within window.
  declare -A latest_line=()
  local line sid
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    sid="$(jq -r '.session_id' <<<"$line")"
    [[ -z "$sid" || "$sid" == "null" ]] && continue
    latest_line["$sid"]="$line"
  done < <(clast_manifest_iterate "$filter")

  local -a rows=()
  local snapshot day_bucket mtime seg abs_path msgs
  local first_ts last_ts start_ts end_ts curated branch slug
  local entries_dir
  entries_dir="$journal_dir/entries"

  for sid in "${!latest_line[@]}"; do
    line="${latest_line[$sid]}"
    snapshot="$(jq -r '.snapshot' <<<"$line")"
    day_bucket="$(jq -r '.day_bucket' <<<"$line")"
    mtime="$(jq -r '.source_mtime' <<<"$line")"
    seg="$(awk -F/ 'NR==1{print $3}' <<<"$snapshot")"

    if (( have_project_filter == 1 )); then
      if [[ -z "${allowed_segs[$seg]:-}" ]]; then
        continue
      fi
    fi

    abs_path="$journal_dir/$snapshot"
    if [[ -r "$abs_path" ]]; then
      msgs="$(wc -l <"$abs_path" 2>/dev/null | tr -d ' ')"
      [[ -z "$msgs" ]] && msgs=0
      first_ts="$(head -n1 "$abs_path" 2>/dev/null | jq -r '.timestamp // empty' 2>/dev/null || true)"
      last_ts="$(tail -n1 "$abs_path" 2>/dev/null | jq -r '.timestamp // empty' 2>/dev/null || true)"
    else
      if [[ -n "${CLAST_VERBOSE:-}" ]]; then
        clast_log_warn "sessions: snapshot missing or unreadable: $abs_path"
      fi
      msgs=0
      first_ts=""
      last_ts=""
    fi
    start_ts="${first_ts:-$mtime}"
    end_ts="${last_ts:-$mtime}"

    if slug="$(clast_registry_resolve "$seg" 2>/dev/null)" && [[ -n "$slug" ]]; then
      :
    else
      slug="$seg"
    fi

    # TODO(step-10): branch field is best-effort; revisit when stats command lands.
    branch=""

    curated=false
    if [[ -d "$entries_dir" ]]; then
      if grep -l "session_id: $sid" "$entries_dir"/*.md 2>/dev/null | head -n1 | grep -q .; then
        curated=true
      fi
    fi

    rows+=("$(jq -cn \
      --arg session_id "$sid" \
      --arg project "$slug" \
      --arg segment "$seg" \
      --arg branch "$branch" \
      --arg start "$start_ts" \
      --arg end "$end_ts" \
      --argjson msg_count_approx "$msgs" \
      --arg snapshot_path "$snapshot" \
      --arg day_bucket "$day_bucket" \
      --argjson curated "$curated" \
      '{
         session_id: $session_id,
         project: $project,
         segment: $segment,
         branch: (if $branch == "" then null else $branch end),
         start: $start,
         end: $end,
         msg_count_approx: $msg_count_approx,
         snapshot_path: $snapshot_path,
         day_bucket: $day_bucket,
         curated: $curated
       }')")
  done

  local rows_json='[]'
  if (( ${#rows[@]} > 0 )); then
    rows_json="$(printf '%s\n' "${rows[@]}" | jq -cs 'sort_by(.start)')"
  fi

  if [[ -n "${CLAST_JSON:-}" ]]; then
    printf '%s\n' "$rows_json"
    return 0
  fi

  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi

  printf '%-37s %-17s %-25s %5s  %5s  %s\n' \
    "session_id" "project" "branch" "start" "end" "msgs"

  local n i sj r_sid r_project r_branch r_start r_end r_msgs r_day disp_start disp_end
  n="$(jq 'length' <<<"$rows_json")"
  for (( i = 0; i < n; i++ )); do
    sj="$(jq -c ".[$i]" <<<"$rows_json")"
    r_sid="$(jq -r '.session_id' <<<"$sj")"
    r_project="$(jq -r '.project' <<<"$sj")"
    r_branch="$(jq -r '.branch // ""' <<<"$sj")"
    r_start="$(jq -r '.start' <<<"$sj")"
    r_end="$(jq -r '.end' <<<"$sj")"
    r_msgs="$(jq -r '.msg_count_approx' <<<"$sj")"
    r_day="$(jq -r '.day_bucket' <<<"$sj")"

    if [[ "${r_start:0:10}" == "${r_end:0:10}" && "${r_start:0:10}" == "$r_day" ]]; then
      disp_start="${r_start:11:5}"
      disp_end="${r_end:11:5}"
    else
      disp_start="${r_start:0:10} ${r_start:11:5}"
      disp_end="${r_end:0:10} ${r_end:11:5}"
    fi

    printf '%-37s %-17s %-25s %5s  %5s  %s\n' \
      "$r_sid" "$r_project" "$r_branch" "$disp_start" "$disp_end" "$r_msgs"
  done
}

# _clast_sessions_window_filter <day> <since> <until>
_clast_sessions_window_filter() {
  local day="$1" since="$2" until="$3"
  if [[ -n "$day" ]]; then
    printf '.day_bucket == "%s"' "$day"
    return 0
  fi
  local -a parts=()
  if [[ -n "$since" ]]; then parts+=(".day_bucket >= \"$since\""); fi
  if [[ -n "$until" ]]; then parts+=(".day_bucket <= \"$until\""); fi
  if (( ${#parts[@]} == 0 )); then printf 'true'; return 0; fi
  local joined="${parts[0]}" i
  for (( i = 1; i < ${#parts[@]}; i++ )); do
    joined+=" and ${parts[i]}"
  done
  printf '%s' "$joined"
}
