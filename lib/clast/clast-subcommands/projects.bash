# clast-subcommands/projects.bash — `clast projects`.
#
# Read-only view over .manifest.jsonl: list projects (segments) with
# session activity in a date window. See docs/cli-contract.md#clast-projects.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

_clast_projects_usage() {
  cat <<'EOF'
Usage: clast projects [--day DATE] [--since DATE] [--until DATE] [--unregistered]

List projects with activity in a date window.

Flags:
  --day DATE         Single-day window (default: today). Mutually exclusive
                     with --since/--until.
  --since DATE       Start of range (inclusive).
  --until DATE       End of range (inclusive).
  --unregistered     Show only projects whose segment does not resolve via
                     the registry.
  -h, --help         Print this usage and exit.

DATE accepts ISO (YYYY-MM-DD), `today`, `yesterday`, `last-week`,
`-Nd`, or `-Nw`. See docs/cli-contract.md#date-parsing.
EOF
}

_clast_projects_err() {
  local msg="$1"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" '{error:$m, code:2}'
  else
    clast_log_error "projects: $msg"
  fi
}

clast_cmd_projects() {
  local day_filter="" since_date="" until_date=""
  local unregistered_only=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --day)
        if [[ $# -lt 2 ]]; then _clast_projects_err "--day requires a value"; return 2; fi
        if ! day_filter="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_projects_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --day=*)
        if ! day_filter="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_projects_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --since)
        if [[ $# -lt 2 ]]; then _clast_projects_err "--since requires a value"; return 2; fi
        if ! since_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_projects_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --since=*)
        if ! since_date="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_projects_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --until)
        if [[ $# -lt 2 ]]; then _clast_projects_err "--until requires a value"; return 2; fi
        if ! until_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_projects_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --until=*)
        if ! until_date="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_projects_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --unregistered)
        unregistered_only=1; shift ;;
      -h|--help)
        _clast_projects_usage; return 0 ;;
      --)
        shift; break ;;
      -*)
        _clast_projects_err "unknown flag '$1'"; return 2 ;;
      *)
        _clast_projects_err "unexpected positional '$1'"; return 2 ;;
    esac
  done

  if [[ -n "$day_filter" && ( -n "$since_date" || -n "$until_date" ) ]]; then
    _clast_projects_err "--day is mutually exclusive with --since/--until"
    return 2
  fi

  if [[ -z "$day_filter" && -z "$since_date" && -z "$until_date" ]]; then
    day_filter="$(clast_today)"
  fi

  # `--until` defaults to `today` per docs/cli-contract.md#clast-projects when
  # `--since` is supplied without an explicit upper bound.
  if [[ -n "$since_date" && -z "$until_date" ]]; then
    until_date="$(clast_today)"
  fi

  # Single-day window when --day is set, or --since == --until.
  local single_day=""
  if [[ -n "$day_filter" ]]; then
    single_day="$day_filter"
  elif [[ -n "$since_date" && "$since_date" == "$until_date" ]]; then
    single_day="$since_date"
  fi

  local filter
  filter="$(_clast_projects_window_filter "$day_filter" "$since_date" "$until_date")"

  local journal_dir
  journal_dir="$(clast_journal_dir)"

  # Two-pass over the manifest stream: first pass finds the most-recent
  # manifest line per session_id within the window (manifest is append-
  # ordered, so the last occurrence wins). Second pass aggregates per
  # segment.
  declare -A latest_line=()
  local line sid
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    sid="$(jq -r '.session_id' <<<"$line")"
    [[ -z "$sid" || "$sid" == "null" ]] && continue
    latest_line["$sid"]="$line"
  done < <(clast_manifest_iterate "$filter")

  declare -A seg_session_count=()
  declare -A seg_msg_count=()
  declare -A seg_last_active=()
  local snapshot mtime seg msgs abs_path

  for sid in "${!latest_line[@]}"; do
    line="${latest_line[$sid]}"
    snapshot="$(jq -r '.snapshot' <<<"$line")"
    mtime="$(jq -r '.source_mtime' <<<"$line")"
    seg="$(awk -F/ 'NR==1{print $3}' <<<"$snapshot")"
    [[ -z "$seg" ]] && continue

    seg_session_count["$seg"]=$(( ${seg_session_count["$seg"]:-0} + 1 ))

    abs_path="$journal_dir/$snapshot"
    if [[ -r "$abs_path" ]]; then
      msgs="$(wc -l <"$abs_path" 2>/dev/null | tr -d ' ')"
      [[ -z "$msgs" ]] && msgs=0
    else
      if [[ -n "${CLAST_VERBOSE:-}" ]]; then
        clast_log_warn "projects: snapshot missing or unreadable: $abs_path"
      fi
      msgs=0
    fi
    seg_msg_count["$seg"]=$(( ${seg_msg_count["$seg"]:-0} + msgs ))

    if [[ -z "${seg_last_active["$seg"]:-}" || "$mtime" > "${seg_last_active["$seg"]}" ]]; then
      seg_last_active["$seg"]="$mtime"
    fi
  done

  # Build per-segment rows as JSON, applying registry + --unregistered.
  local -a rows=()
  local slug path remote registered row decode_rc decoded
  for seg in "${!seg_session_count[@]}"; do
    if slug="$(clast_registry_resolve "$seg" 2>/dev/null)" && [[ -n "$slug" ]]; then
      registered=true
      path="$(_clast_projects_path_for_slug "$slug")"
      remote="$(_clast_projects_remote_for_slug "$slug")"
    else
      registered=false
      slug=""
      remote=""
      decode_rc=0
      decoded="$(clast_decode_segment "$seg" 2>/dev/null)" || decode_rc=$?
      if (( decode_rc == 0 )); then
        path="$decoded"
      else
        path=""
      fi
    fi

    if (( unregistered_only == 1 )) && [[ "$registered" == "true" ]]; then
      continue
    fi

    row="$(jq -cn \
      --arg slug "$slug" \
      --arg path "$path" \
      --arg segment "$seg" \
      --arg remote "$remote" \
      --argjson session_count "${seg_session_count[$seg]}" \
      --argjson msg_count_approx "${seg_msg_count[$seg]}" \
      --arg last_active "${seg_last_active[$seg]}" \
      --argjson registered "$registered" \
      '{
         slug: (if $slug == "" then null else $slug end),
         path: (if $path == "" then null else $path end),
         segment: $segment,
         remote: (if $remote == "" then null else $remote end),
         session_count: $session_count,
         msg_count_approx: $msg_count_approx,
         last_active: $last_active,
         registered: $registered
       }')"
    rows+=("$row")
  done

  # Sort by (-session_count, slug-or-segment).
  local rows_json='[]'
  if (( ${#rows[@]} > 0 )); then
    rows_json="$(printf '%s\n' "${rows[@]}" \
      | jq -cs 'sort_by([-.session_count, (.slug // .segment)])')"
  fi

  if [[ -n "${CLAST_JSON:-}" ]]; then
    printf '%s\n' "$rows_json"
    return 0
  fi

  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi

  # Default human output: header + one row per project.
  printf '%-17s %-33s %9s %5s  %s\n' \
    "slug" "path" "sessions" "msgs" "last_active"

  local n
  n="$(jq 'length' <<<"$rows_json")"
  local i sj
  for (( i = 0; i < n; i++ )); do
    sj="$(jq -c ".[$i]" <<<"$rows_json")"
    local r_slug r_path r_seg r_sessions r_msgs r_last_active disp_slug disp_path
    r_slug="$(jq -r '.slug // ""' <<<"$sj")"
    r_path="$(jq -r '.path // ""' <<<"$sj")"
    r_seg="$(jq -r '.segment' <<<"$sj")"
    r_sessions="$(jq -r '.session_count' <<<"$sj")"
    r_msgs="$(jq -r '.msg_count_approx' <<<"$sj")"
    r_last_active="$(jq -r '.last_active' <<<"$sj")"

    if [[ -n "$r_slug" ]]; then
      disp_slug="$r_slug"
    else
      disp_slug="(unregistered)"
    fi
    if [[ -n "$r_path" ]]; then
      disp_path="$r_path"
    else
      disp_path="$r_seg"
    fi

    local disp_last
    if [[ -n "$single_day" ]]; then
      disp_last="${r_last_active:11:5}"
    else
      disp_last="${r_last_active:0:10} ${r_last_active:11:5}"
    fi

    printf '%-17s %-33s %9s %5s  %s\n' \
      "$disp_slug" "$disp_path" "$r_sessions" "$r_msgs" "$disp_last"
  done
}

# _clast_projects_window_filter <day_filter> <since> <until>
#   Build a jq select-body string matching .day_bucket against the window.
_clast_projects_window_filter() {
  local day="$1" since="$2" until="$3"
  if [[ -n "$day" ]]; then
    printf '.day_bucket == "%s"' "$day"
    return 0
  fi
  local parts=()
  if [[ -n "$since" ]]; then
    parts+=(".day_bucket >= \"$since\"")
  fi
  if [[ -n "$until" ]]; then
    parts+=(".day_bucket <= \"$until\"")
  fi
  if (( ${#parts[@]} == 0 )); then
    printf 'true'
    return 0
  fi
  local joined="${parts[0]}"
  local i
  for (( i = 1; i < ${#parts[@]}; i++ )); do
    joined+=" and ${parts[i]}"
  done
  printf '%s' "$joined"
}

# _clast_projects_path_for_slug <slug>
#   First registry path for <slug>, or empty.
_clast_projects_path_for_slug() {
  local slug="$1"
  clast_registry_list_json \
    | jq -r --arg s "$slug" 'map(select(.slug == $s)) | .[0].path // empty'
}

# _clast_projects_remote_for_slug <slug>
#   First non-empty registry remote for <slug>, or empty.
_clast_projects_remote_for_slug() {
  local slug="$1"
  clast_registry_list_json \
    | jq -r --arg s "$slug" 'map(select(.slug == $s and (.remote // "") != "")) | .[0].remote // empty'
}
