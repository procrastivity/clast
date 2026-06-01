# clast-subcommands/stats.bash — `clast stats`.
#
# Read-only summary of journal activity over a date window using manifest
# + filesystem stat only. No JSONL body parsing. See
# docs/cli-contract.md#clast-stats.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

_clast_stats_usage() {
  cat <<'EOF'
Usage: clast stats [--day DATE] [--since DATE] [--until DATE] [--project SLUG]

Summarize journal activity over a date window (default: today).

Flags:
  --day DATE       Single-day window. Mutually exclusive with --since/--until.
  --since DATE     Start of range (inclusive).
  --until DATE     End of range (inclusive).
  --project SLUG   Filter to one registered project slug.
  -h, --help       Print this usage and exit.

DATE accepts ISO (YYYY-MM-DD), `today`, `yesterday`, `last-week`,
`-Nd`, or `-Nw`. See docs/cli-contract.md#date-parsing.
EOF
}

_clast_stats_err() {
  local msg="$1" code="${2:-2}"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" --argjson c "$code" '{error:$m, code:$c}'
  else
    clast_log_error "stats: $msg"
  fi
}

# _clast_stats_human_bytes <int>
#   Render bytes as "<n> B" / "<x.x> KB" / "<x.x> MB" / "<x.x> GB".
#   Base-2 math, base-10 labels — matches snapshot summary from step 06.
_clast_stats_human_bytes() {
  local b="$1"
  if (( b < 1024 )); then
    printf '%s B' "$b"
    return 0
  fi
  awk -v b="$b" 'BEGIN {
    units[0] = "KB"; units[1] = "MB"; units[2] = "GB"; units[3] = "TB"
    i = 0
    v = b / 1024
    while (v >= 1024 && i < 3) { v = v / 1024; i++ }
    printf "%.1f %s", v, units[i]
  }'
}

clast_cmd_stats() {
  local day_input="" since_input="" until_input=""
  local day_filter="" since_date="" until_date=""
  local project_filter=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --day)
        if [[ $# -lt 2 ]]; then _clast_stats_err "--day requires a value"; return 2; fi
        day_input="$2"
        if ! day_filter="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_stats_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --day=*)
        day_input="${1#*=}"
        if ! day_filter="$(clast_parse_date "$day_input" 2>/dev/null)"; then
          _clast_stats_err "invalid date '$day_input'"; return 2
        fi
        shift ;;
      --since)
        if [[ $# -lt 2 ]]; then _clast_stats_err "--since requires a value"; return 2; fi
        since_input="$2"
        if ! since_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_stats_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --since=*)
        since_input="${1#*=}"
        if ! since_date="$(clast_parse_date "$since_input" 2>/dev/null)"; then
          _clast_stats_err "invalid date '$since_input'"; return 2
        fi
        shift ;;
      --until)
        if [[ $# -lt 2 ]]; then _clast_stats_err "--until requires a value"; return 2; fi
        until_input="$2"
        if ! until_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_stats_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --until=*)
        until_input="${1#*=}"
        if ! until_date="$(clast_parse_date "$until_input" 2>/dev/null)"; then
          _clast_stats_err "invalid date '$until_input'"; return 2
        fi
        shift ;;
      --project)
        if [[ $# -lt 2 ]]; then _clast_stats_err "--project requires a value"; return 2; fi
        project_filter="$2"; shift 2 ;;
      --project=*)
        project_filter="${1#*=}"; shift ;;
      -h|--help)
        _clast_stats_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_stats_err "unknown flag '$1'"; return 2 ;;
      *)  _clast_stats_err "unexpected positional '$1'"; return 2 ;;
    esac
  done

  if [[ -n "$day_filter" && ( -n "$since_date" || -n "$until_date" ) ]]; then
    _clast_stats_err "--day cannot be combined with --since or --until"
    return 2
  fi

  local today
  today="$(clast_today)"
  local label_suffix="" json_label=""
  local window_start window_end
  if [[ -n "$day_filter" ]]; then
    window_start="$day_filter"
    window_end="$day_filter"
    if [[ "$day_filter" == "$today" ]]; then
      label_suffix=" (today)"; json_label="today"
    else
      local yest
      yest="$(clast_parse_date yesterday)"
      if [[ "$day_filter" == "$yest" ]]; then
        label_suffix=" (yesterday)"; json_label="yesterday"
      fi
    fi
  elif [[ -z "$since_date" && -z "$until_date" ]]; then
    window_start="$today"
    window_end="$today"
    label_suffix=" (today)"; json_label="today"
  else
    if [[ -z "$since_date" ]]; then
      since_date="1970-01-01"
    fi
    local until_was_default=0
    if [[ -z "$until_date" ]]; then
      until_date="$today"
      until_was_default=1
    fi
    window_start="$since_date"
    window_end="$until_date"
    if (( until_was_default == 1 )); then
      label_suffix=" (through today)"; json_label="through_today"
    fi
  fi

  if ! [[ "$window_start" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
    _clast_stats_err "invalid date '$window_start'"; return 2
  fi
  if ! [[ "$window_end" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
    _clast_stats_err "invalid date '$window_end'"; return 2
  fi
  if [[ "$window_start" > "$window_end" ]]; then
    _clast_stats_err "--since must be <= --until"
    return 2
  fi

  # Validate --project slug exists in registry.
  if [[ -n "$project_filter" ]]; then
    local reg_json slug_match
    reg_json="$(clast_registry_list_json)"
    slug_match="$(jq -r --arg s "$project_filter" \
      'map(select(.slug == $s)) | .[0].slug // empty' <<<"$reg_json")"
    if [[ -z "$slug_match" ]]; then
      _clast_stats_err "unknown project slug '$project_filter'" 1
      return 1
    fi
  fi

  if [[ -n "${CLAST_VERBOSE:-}" ]]; then
    clast_log_info "stats: window $window_start..$window_end"
  fi

  # Stream manifest rows in window, reduce to most-recent per session_id.
  local journal_dir
  journal_dir="$(clast_journal_dir)"

  local filter
  if [[ "$window_start" == "$window_end" ]]; then
    filter='.day_bucket == "'"$window_start"'"'
  else
    filter='.day_bucket >= "'"$window_start"'" and .day_bucket <= "'"$window_end"'"'
  fi

  local rows_json='[]'
  local raw
  raw="$(clast_manifest_iterate "$filter" | jq -cs \
    'group_by(.session_id) | map(max_by(.captured_at))')"
  if [[ -n "$raw" ]]; then
    rows_json="$raw"
  fi

  local n
  n="$(jq 'length' <<<"$rows_json")"

  # Resolve project slug per row, apply --project filter.
  local -a kept_rows=()
  local -A slug_counts=()
  local i row source snapshot seg slug bytes_sum=0 msgs_sum=0
  local n_sessions=0
  for (( i = 0; i < n; i++ )); do
    row="$(jq -c ".[$i]" <<<"$rows_json")"
    source="$(jq -r '.source' <<<"$row")"
    snapshot="$(jq -r '.snapshot' <<<"$row")"
    seg="$(awk -F/ 'NR==1{print $3}' <<<"$snapshot")"

    slug=""
    if [[ "$source" != "null" && -n "$source" ]]; then
      if ! slug="$(clast_registry_resolve "$seg" 2>/dev/null)" || [[ -z "$slug" ]]; then
        slug="$seg"
      fi
    fi

    if [[ -n "$project_filter" ]]; then
      if [[ "$slug" != "$project_filter" ]]; then
        continue
      fi
    fi

    kept_rows+=("$row")
    n_sessions=$((n_sessions + 1))
    if [[ -n "$slug" ]]; then
      slug_counts["$slug"]=1
    fi

    local size
    size="$(jq -r '.source_size // 0' <<<"$row")"
    [[ "$size" =~ ^[0-9]+$ ]] || size=0
    bytes_sum=$((bytes_sum + size))

    local abs="$journal_dir/$snapshot" m=0
    if [[ -r "$abs" ]]; then
      m="$(wc -l <"$abs" 2>/dev/null | tr -d ' ')"
      [[ -z "$m" ]] && m=0
    fi
    msgs_sum=$((msgs_sum + m))
  done

  local n_projects=${#slug_counts[@]}

  # Curated count: entries/*.md files whose date prefix is in window.
  local curated=0
  local entries_dir="$journal_dir/entries"
  if [[ -d "$entries_dir" ]]; then
    local f base prefix
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      base="$(basename "$f")"
      prefix="${base:0:10}"
      if [[ "$prefix" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] \
         && ! [[ "$prefix" < "$window_start" ]] \
         && ! [[ "$prefix" > "$window_end" ]]; then
        curated=$((curated + 1))
      fi
    done < <(find "$entries_dir" -mindepth 1 -maxdepth 1 -type f -name '*.md' 2>/dev/null)
  fi

  local curated_pct=0
  if (( n_sessions > 0 )); then
    curated_pct=$(( (curated * 100 + n_sessions / 2) / n_sessions ))
    if (( curated_pct > 100 )); then curated_pct=100; fi
  fi

  # Breadcrumb count: breadcrumbs/<YYYY-MM-DD>-<slug>.md in window.
  local breadcrumbs=0
  local -A breadcrumb_slugs=()
  local breadcrumbs_dir="$journal_dir/breadcrumbs"
  if [[ -d "$breadcrumbs_dir" ]]; then
    local bf bname bprefix brest bslug
    while IFS= read -r bf; do
      [[ -z "$bf" ]] && continue
      bname="$(basename "$bf")"
      bprefix="${bname:0:10}"
      if [[ "$bprefix" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] \
         && ! [[ "$bprefix" < "$window_start" ]] \
         && ! [[ "$bprefix" > "$window_end" ]]; then
        breadcrumbs=$((breadcrumbs + 1))
        brest="${bname:11}"
        bslug="${brest%.md}"
        if [[ -n "$bslug" ]]; then
          breadcrumb_slugs["$bslug"]=1
        fi
      fi
    done < <(find "$breadcrumbs_dir" -mindepth 1 -maxdepth 1 -type f -name '*.md' 2>/dev/null)
  fi
  local breadcrumb_projects=${#breadcrumb_slugs[@]}

  local bytes_human
  bytes_human="$(_clast_stats_human_bytes "$bytes_sum")"

  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn \
      --arg start "$window_start" \
      --arg end "$window_end" \
      --arg label "$json_label" \
      --argjson projects "$n_projects" \
      --argjson sessions "$n_sessions" \
      --argjson messages_approx "$msgs_sum" \
      --argjson bytes "$bytes_sum" \
      --arg bytes_human "$bytes_human" \
      --argjson curated "$curated" \
      --argjson curated_pct "$curated_pct" \
      --argjson breadcrumbs "$breadcrumbs" \
      --argjson breadcrumb_projects "$breadcrumb_projects" \
      '{
        window: {start:$start, end:$end, label:$label},
        projects: $projects,
        sessions: $sessions,
        messages_approx: $messages_approx,
        bytes: $bytes,
        bytes_human: $bytes_human,
        curated: $curated,
        curated_pct: $curated_pct,
        breadcrumbs: $breadcrumbs,
        breadcrumb_projects: $breadcrumb_projects
      }'
    return 0
  fi

  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi

  local window_text
  if [[ "$window_start" == "$window_end" ]]; then
    window_text="$window_start"
  else
    window_text="$window_start..$window_end"
  fi

  printf '%-12s %s%s\n' "Window:"      "$window_text" "$label_suffix"
  printf '%-12s %d\n'   "Projects:"    "$n_projects"
  printf '%-12s %d\n'   "Sessions:"    "$n_sessions"
  printf '%-12s %d (approx)\n' "Messages:" "$msgs_sum"
  printf '%-12s %s\n'   "Bytes:"       "$bytes_human"
  printf '%-12s %d of %d sessions (%d%%)\n' "Curated:" "$curated" "$n_sessions" "$curated_pct"
  printf '%-12s %d across %d projects\n' "Breadcrumbs:" "$breadcrumbs" "$breadcrumb_projects"
}
