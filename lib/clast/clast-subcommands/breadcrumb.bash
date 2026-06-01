# clast-subcommands/breadcrumb.bash — `clast breadcrumb` write/read/list.
#
# Breadcrumbs are one-line in-flight hints stored under
#   $(clast_journal_dir)/breadcrumbs/YYYY-MM-DD-<project>.md
# with tiny YAML frontmatter and append-only Markdown body lines.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

clast_cmd_breadcrumb() {
  local arg saw_read=0 saw_list=0 before_double_dash=1

  _clast_breadcrumb_strip_json "$@"
  set -- "${_CLAST_BREADCRUMB_JSON_STRIPPED[@]}"

  for arg in "$@"; do
    case "$arg" in
      -h|--help)
        _clast_breadcrumb_usage
        return 0
        ;;
    esac
  done

  for arg in "$@"; do
    if (( before_double_dash )) && [[ "$arg" == "--" ]]; then
      before_double_dash=0
      continue
    fi
    if (( ! before_double_dash )); then
      continue
    fi
    case "$arg" in
      --read) saw_read=1 ;;
      --list) saw_list=1 ;;
    esac
  done

  if (( saw_read && saw_list )); then
    _clast_breadcrumb_err "--read and --list are mutually exclusive" 2
    return 2
  fi

  if (( saw_list )); then
    _clast_breadcrumb_strip_mode --list "$@"
    _clast_breadcrumb_list "${_CLAST_BREADCRUMB_STRIPPED[@]}"
  elif (( saw_read )); then
    _clast_breadcrumb_strip_mode --read "$@"
    _clast_breadcrumb_read "${_CLAST_BREADCRUMB_STRIPPED[@]}"
  else
    _clast_breadcrumb_write "$@"
  fi
}

_clast_breadcrumb_strip_json() {
  local arg before_double_dash=1
  _CLAST_BREADCRUMB_JSON_STRIPPED=()
  for arg in "$@"; do
    if (( before_double_dash )) && [[ "$arg" == "--" ]]; then
      before_double_dash=0
      _CLAST_BREADCRUMB_JSON_STRIPPED+=("$arg")
      continue
    fi
    if (( before_double_dash )) && [[ "$arg" == "--json" ]]; then
      export CLAST_JSON=1
      continue
    fi
    _CLAST_BREADCRUMB_JSON_STRIPPED+=("$arg")
  done
}

_clast_breadcrumb_usage() {
  cat <<'EOF'
Usage:
  clast breadcrumb [--project SLUG | --global] [--date DATE] <TEXT>
  clast breadcrumb --read [--project SLUG | --global] [--day DATE]
  clast breadcrumb --list [--day DATE]

DATE accepts ISO, today, yesterday, last-week, -Nd, -Nw.
EOF
}

_clast_breadcrumb_err() {
  local msg="$1" code="${2:-2}"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" --argjson c "$code" '{error:$m, code:$c}'
  else
    printf 'clast: breadcrumb: %s\n' "$msg" >&2
  fi
}

_clast_breadcrumb_strip_mode() {
  local strip="$1" arg stripped=0 before_double_dash=1
  shift
  _CLAST_BREADCRUMB_STRIPPED=()
  for arg in "$@"; do
    if (( before_double_dash )) && [[ "$arg" == "--" ]]; then
      before_double_dash=0
      _CLAST_BREADCRUMB_STRIPPED+=("$arg")
      continue
    fi
    if (( before_double_dash )) && (( ! stripped )) && [[ "$arg" == "$strip" ]]; then
      stripped=1
      continue
    fi
    _CLAST_BREADCRUMB_STRIPPED+=("$arg")
  done
}

_clast_breadcrumb_resolve_date() {
  local flag="$1" input="$2" resolved
  if ! resolved="$(clast_parse_date "$input" 2>/dev/null)"; then
    return 2
  fi
  if ! [[ "$resolved" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
    return 2
  fi
  printf '%s\n' "$resolved"
}

_clast_breadcrumb_resolve_scope() {
  local project_filter="$1" scope_global="$2" out_name="$3" resolved_slug reg_json
  if [[ -n "$project_filter" ]]; then
    resolved_slug="$project_filter"
    reg_json="$(clast_registry_list_json)"
    if ! jq -e --arg s "$resolved_slug" 'any(.slug == $s)' <<<"$reg_json" >/dev/null; then
      clast_log_warn "slug '$resolved_slug' not in registry"
    fi
    printf -v "$out_name" '%s' "$resolved_slug"
    return 0
  fi
  if [[ "$scope_global" == "1" ]]; then
    printf -v "$out_name" '%s' "_global"
    return 0
  fi
  if resolved_slug="$(clast_registry_resolve "$PWD" 2>/dev/null)" && [[ -n "$resolved_slug" ]]; then
    printf -v "$out_name" '%s' "$resolved_slug"
    return 0
  fi

  return 1
}

_clast_breadcrumb_path() {
  local day="$1" slug="$2" journal_dir
  journal_dir="$(realpath -m "$(clast_journal_dir)")"
  printf '%s\n' "$journal_dir/breadcrumbs/$day-$slug.md"
}

_clast_breadcrumb_validate_slug() {
  local slug="$1"
  if [[ -z "$slug" || "$slug" == "." || "$slug" == ".." || "$slug" == *"/"* || "$slug" == *$'\n'* || "$slug" == *$'\r'* ]]; then
    _clast_breadcrumb_err "invalid project slug '$slug'" 2
    return 2
  fi
}

_clast_breadcrumb_line_count() {
  local path="$1" count
  count="$(grep -c '^- ' "$path" 2>/dev/null || true)"
  printf '%s\n' "${count:-0}"
}

_clast_breadcrumb_parse_scope_flag() {
  local flag="$1" project_filter_name="$2" scope_global_name="$3"
  local -n project_filter_ref="$project_filter_name"
  local -n scope_global_ref="$scope_global_name"
  case "$flag" in
    project)
      if [[ "$scope_global_ref" == "1" ]]; then
        _clast_breadcrumb_err "--project and --global are mutually exclusive" 2
        return 2
      fi
      ;;
    global)
      if [[ -n "$project_filter_ref" ]]; then
        _clast_breadcrumb_err "--project and --global are mutually exclusive" 2
        return 2
      fi
      scope_global_ref=1
      ;;
  esac
}

_clast_breadcrumb_write() {
  local project_filter="" scope_global=0 date_input="" resolved_date="" slug="" text="" path line epoch hhmm content line_count rel
  local -a words=()

  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help)
        _clast_breadcrumb_usage
        return 0
        ;;
      --project)
        if [[ $# -lt 2 ]]; then _clast_breadcrumb_err "--project requires a value" 2; return 2; fi
        _clast_breadcrumb_parse_scope_flag project project_filter scope_global || return $?
        project_filter="$2"
        shift 2
        ;;
      --project=*)
        _clast_breadcrumb_parse_scope_flag project project_filter scope_global || return $?
        project_filter="${1#*=}"
        shift
        ;;
      --global)
        _clast_breadcrumb_parse_scope_flag global project_filter scope_global || return $?
        shift
        ;;
      --date)
        if [[ $# -lt 2 ]]; then _clast_breadcrumb_err "--date requires a value" 2; return 2; fi
        date_input="$2"
        shift 2
        ;;
      --date=*)
        date_input="${1#*=}"
        shift
        ;;
      --)
        shift
        while [[ $# -gt 0 ]]; do words+=("$1"); shift; done
        ;;
      -*)
        _clast_breadcrumb_err "unknown flag '$1'" 2
        return 2
        ;;
      *)
        words+=("$1")
        shift
        ;;
    esac
  done

  local word
  for word in "${words[@]}"; do
    if [[ "$word" == *$'\n'* || "$word" == *$'\r'* ]]; then
      _clast_breadcrumb_err "text must be a single line" 2
      return 2
    fi
  done
  text="${words[*]}"
  if ! [[ "$text" =~ [^[:space:]] ]]; then
    _clast_breadcrumb_err "missing required argument <TEXT>" 2
    return 2
  fi

  if [[ -n "$date_input" ]]; then
    if ! resolved_date="$(_clast_breadcrumb_resolve_date --date "$date_input")"; then
      _clast_breadcrumb_err "invalid --date '$date_input'" 2
      return 2
    fi
  else
    resolved_date="$(clast_today)"
  fi

  if ! _clast_breadcrumb_resolve_scope "$project_filter" "$scope_global" slug; then
    _clast_breadcrumb_err "pwd does not resolve to a registered project (pass --project SLUG or --global)" 1
    return 1
  fi
  _clast_breadcrumb_validate_slug "$slug" || return $?
  path="$(_clast_breadcrumb_path "$resolved_date" "$slug")"
  mkdir -p "$(dirname "$path")" || { _clast_breadcrumb_err "failed to create breadcrumbs directory" 1; return 1; }

  epoch="${CLAST_NOW_EPOCH:-$(date +%s)}"
  if ! hhmm="$(date -d "@$epoch" +%H:%M 2>/dev/null)"; then
    _clast_breadcrumb_err "failed to format timestamp" 1
    return 1
  fi
  line="- $hhmm — $text"

  if [[ ! -e "$path" ]]; then
    content=$'---\n'
    content+="date: $resolved_date"$'\n'
    content+="project: $slug"$'\n'
    content+=$'---\n\n'
    content+="$line"$'\n'
    if ! clast_atomic_write "$path" "$content"; then
      _clast_breadcrumb_err "failed to write '$path'" 1
      return 1
    fi
  else
    if [[ -s "$path" ]] && [[ "$(tail -c 1 "$path" | wc -l | tr -d ' ')" == "0" ]]; then
      printf '\n%s\n' "$line" >>"$path" || { _clast_breadcrumb_err "failed to append '$path'" 1; return 1; }
    else
      printf '%s\n' "$line" >>"$path" || { _clast_breadcrumb_err "failed to append '$path'" 1; return 1; }
    fi
  fi

  line_count="$(_clast_breadcrumb_line_count "$path")"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg path "$path" --arg slug "$slug" --arg date "$resolved_date" --argjson n "$line_count" \
      '{path:$path, slug:$slug, date:$date, line_count:$n}'
  elif [[ -n "${CLAST_VERBOSE:-}" ]]; then
    rel="breadcrumbs/$resolved_date-$slug.md"
    printf 'clast: breadcrumb: wrote %s (%s lines)\n' "$rel" "$line_count" >&2
  fi
}

_clast_breadcrumb_read() {
  local project_filter="" scope_global=0 day_input="" resolved_day="" slug="" path
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help)
        _clast_breadcrumb_usage
        return 0
        ;;
      --project)
        if [[ $# -lt 2 ]]; then _clast_breadcrumb_err "--project requires a value" 2; return 2; fi
        _clast_breadcrumb_parse_scope_flag project project_filter scope_global || return $?
        project_filter="$2"
        shift 2
        ;;
      --project=*)
        _clast_breadcrumb_parse_scope_flag project project_filter scope_global || return $?
        project_filter="${1#*=}"
        shift
        ;;
      --global)
        _clast_breadcrumb_parse_scope_flag global project_filter scope_global || return $?
        shift
        ;;
      --day)
        if [[ $# -lt 2 ]]; then _clast_breadcrumb_err "--day requires a value" 2; return 2; fi
        day_input="$2"
        shift 2
        ;;
      --day=*)
        day_input="${1#*=}"
        shift
        ;;
      --)
        shift
        if [[ $# -gt 0 ]]; then _clast_breadcrumb_err "unexpected arg '$1'" 2; return 2; fi
        ;;
      -*)
        _clast_breadcrumb_err "unknown flag '$1'" 2
        return 2
        ;;
      *)
        _clast_breadcrumb_err "unexpected arg '$1'" 2
        return 2
        ;;
    esac
  done

  if [[ -n "$day_input" ]]; then
    if ! resolved_day="$(_clast_breadcrumb_resolve_date --day "$day_input")"; then
      _clast_breadcrumb_err "invalid --day '$day_input'" 2
      return 2
    fi
  else
    resolved_day="$(clast_today)"
  fi
  if ! _clast_breadcrumb_resolve_scope "$project_filter" "$scope_global" slug; then
    _clast_breadcrumb_err "pwd does not resolve to a registered project (pass --project SLUG or --global)" 1
    return 1
  fi
  _clast_breadcrumb_validate_slug "$slug" || return $?
  path="$(_clast_breadcrumb_path "$resolved_day" "$slug")"

  if [[ -f "$path" ]]; then
    if [[ -n "${CLAST_JSON:-}" ]]; then
      jq -n --arg path "$path" --rawfile content "$path" '{path:$path, exists:true, content:$content}'
    elif [[ -z "${CLAST_QUIET:-}" ]]; then
      cat -- "$path"
    fi
  else
    if [[ -n "${CLAST_JSON:-}" ]]; then
      jq -cn --arg path "$path" '{path:$path, exists:false, content:""}'
    fi
  fi
}

_clast_breadcrumb_list() {
  local day_input="" resolved_day="" dir rows_json
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help)
        _clast_breadcrumb_usage
        return 0
        ;;
      --day)
        if [[ $# -lt 2 ]]; then _clast_breadcrumb_err "--day requires a value" 2; return 2; fi
        day_input="$2"
        shift 2
        ;;
      --day=*)
        day_input="${1#*=}"
        shift
        ;;
      --)
        shift
        if [[ $# -gt 0 ]]; then _clast_breadcrumb_err "unexpected arg '$1'" 2; return 2; fi
        ;;
      -*)
        _clast_breadcrumb_err "unknown flag '$1'" 2
        return 2
        ;;
      *)
        _clast_breadcrumb_err "unexpected arg '$1'" 2
        return 2
        ;;
    esac
  done

  if [[ -n "$day_input" ]]; then
    if ! resolved_day="$(_clast_breadcrumb_resolve_date --day "$day_input")"; then
      _clast_breadcrumb_err "invalid --day '$day_input'" 2
      return 2
    fi
  else
    resolved_day="$(clast_today)"
  fi

  dir="$(realpath -m "$(clast_journal_dir)")/breadcrumbs"
  rows_json="$(_clast_breadcrumb_list_rows "$dir" "$resolved_day")"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -n --argjson rows "$rows_json" '$rows'
    return 0
  fi
  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi

  printf '%-17s %-50s %11s\n' "project" "path" "breadcrumbs"
  jq -r '.[] | [.project, .path, (.line_count|tostring)] | @tsv' <<<"$rows_json" \
    | while IFS=$'\t' read -r project path count; do
        if [[ "$project" == "_global" ]]; then
          project="(global)"
        fi
        printf '%-17s %-50s %11s\n' "$project" "$path" "$count"
      done
}

_clast_breadcrumb_list_rows() {
  local dir="$1" day="$2" file base project count abs
  if [[ ! -d "$dir" ]]; then
    printf '[]\n'
    return 0
  fi

  find "$dir" -maxdepth 1 -type f -name "$day-*.md" -print0 \
    | while IFS= read -r -d '' file; do
        base="$(basename "$file")"
        project="${base#"$day-"}"
        project="${project%.md}"
        abs="$(realpath -m "$file")"
        count="$(_clast_breadcrumb_line_count "$file")"
        jq -cn --arg project "$project" --arg path "$abs" --argjson line_count "$count" \
          '{project:$project, path:$path, line_count:$line_count}'
      done \
    | jq -cs 'sort_by(.project)'
}
