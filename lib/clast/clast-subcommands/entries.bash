# clast-subcommands/entries.bash — `clast entries` list/read/write.
#
# Curated journal entries live at
#   $(clast_journal_dir)/entries/YYYY-MM-DD-HHMM-<project-slug>-<session-slug>.md
# as Markdown files with a YAML frontmatter block. See
# docs/reference/cli.md#clast-entries and docs/reference/cli.md#entry-frontmatter.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

_clast_entries_usage() {
  cat <<'EOF'
Usage:
  clast entries [--day DATE] [--since DATE] [--until DATE]
                [--project SLUG] [--tag TAG]... [--limit N]
  clast entries read <entry-path-or-basename>
  clast entries write --session SESSION_ID --slug SESSION_SLUG
                      [--tags TAG,TAG,...] [--title TITLE]
                      (--body-from FILE | --body-stdin)

DATE accepts ISO, today, yesterday, last-week, -Nd, -Nw.
Tags are AND-intersection when --tag is repeated.
EOF
}

_clast_entries_err() {
  local msg="$1" code="${2:-2}"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" --argjson c "$code" '{error:$m, code:$c}'
  else
    clast_log_error "entries: $msg"
  fi
}

clast_cmd_entries() {
  local first="${1:-}"
  case "$first" in
    -h|--help)
      _clast_entries_usage
      return 0
      ;;
    read)
      shift
      _clast_entries_read "$@"
      ;;
    write)
      shift
      _clast_entries_write "$@"
      ;;
    list)
      shift
      _clast_entries_list "$@"
      ;;
    ""|-*)
      _clast_entries_list "$@"
      ;;
    *)
      _clast_entries_err "unknown subcommand '$first'"
      return 2
      ;;
  esac
}

# ---------------------------------------------------------------------------
# Frontmatter helpers
# ---------------------------------------------------------------------------

# _clast_entries_read_frontmatter <path>
#   Emit raw frontmatter lines (between the first two `---` fences) to stdout.
#   Thin alias over the shared primitive in clast-lib.bash.
_clast_entries_read_frontmatter() {
  clast_read_frontmatter "$1"
}

# _clast_entries_extract_title <path>
#   Look for `# Session: <title>` as the first non-blank body line.
#   Thin alias over the shared primitive in clast-lib.bash.
_clast_entries_extract_title() {
  clast_entry_title "$1"
}

# _clast_entries_unquote <string>
#   Strip surrounding double quotes and unescape \", \\, \n.
#   Thin alias over the shared primitive in clast-lib.bash.
_clast_entries_unquote() {
  clast_yaml_unquote "$1"
}

# ---------------------------------------------------------------------------
# YAML emit helpers
# ---------------------------------------------------------------------------

# _clast_entries_yaml_string <value>
#   Emit a YAML scalar. Bare when safe; double-quoted otherwise.
_clast_entries_yaml_string() {
  local v="$1"
  if [[ -z "$v" ]]; then
    printf '""'
    return 0
  fi
  if [[ "$v" =~ ^[A-Za-z0-9._/@+-][A-Za-z0-9._/@+-]*$ ]]; then
    printf '%s' "$v"
    return 0
  fi
  local esc="${v//\\/\\\\}"
  esc="${esc//\"/\\\"}"
  esc="${esc//$'\n'/\\n}"
  printf '"%s"' "$esc"
}

# ---------------------------------------------------------------------------
# Time helpers
# ---------------------------------------------------------------------------

_clast_entries_now_hhmm() {
  local epoch
  epoch="$(_clast_now_epoch)"
  date -d "@$epoch" +%H:%M
}

_clast_entries_now_hhmm_compact() {
  local epoch
  epoch="$(_clast_now_epoch)"
  date -d "@$epoch" +%H%M
}

# ---------------------------------------------------------------------------
# list
# ---------------------------------------------------------------------------

_clast_entries_list() {
  local day_filter="" since_date="" until_date=""
  local project_filter="" limit=""
  local -a tag_filters=()

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --day)
        if [[ $# -lt 2 ]]; then _clast_entries_err "--day requires a value"; return 2; fi
        if ! day_filter="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_entries_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --day=*)
        if ! day_filter="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_entries_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --since)
        if [[ $# -lt 2 ]]; then _clast_entries_err "--since requires a value"; return 2; fi
        if ! since_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_entries_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --since=*)
        if ! since_date="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_entries_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --until)
        if [[ $# -lt 2 ]]; then _clast_entries_err "--until requires a value"; return 2; fi
        if ! until_date="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_entries_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --until=*)
        if ! until_date="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_entries_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --project)
        if [[ $# -lt 2 ]]; then _clast_entries_err "--project requires a value"; return 2; fi
        project_filter="$2"; shift 2 ;;
      --project=*)
        project_filter="${1#*=}"; shift ;;
      --tag)
        if [[ $# -lt 2 ]]; then _clast_entries_err "--tag requires a value"; return 2; fi
        tag_filters+=("$2"); shift 2 ;;
      --tag=*)
        tag_filters+=("${1#*=}"); shift ;;
      --limit)
        if [[ $# -lt 2 ]]; then _clast_entries_err "--limit requires a value"; return 2; fi
        if ! [[ "$2" =~ ^[1-9][0-9]*$ ]]; then
          _clast_entries_err "--limit must be a positive integer"; return 2
        fi
        limit="$2"; shift 2 ;;
      --limit=*)
        local v="${1#*=}"
        if ! [[ "$v" =~ ^[1-9][0-9]*$ ]]; then
          _clast_entries_err "--limit must be a positive integer"; return 2
        fi
        limit="$v"; shift ;;
      -h|--help) _clast_entries_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_entries_err "unknown flag '$1'"; return 2 ;;
      *) _clast_entries_err "unexpected positional '$1'"; return 2 ;;
    esac
  done

  if [[ -n "$day_filter" && ( -n "$since_date" || -n "$until_date" ) ]]; then
    _clast_entries_err "--day is mutually exclusive with --since/--until"
    return 2
  fi

  local journal_dir entries_dir
  journal_dir="$(clast_journal_dir)"
  entries_dir="$journal_dir/entries"

  local -a rows=()
  if [[ -d "$entries_dir" ]]; then
    local f
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      _clast_entries_list_consider "$f" "$day_filter" "$since_date" "$until_date" \
        "$project_filter" "${#tag_filters[@]}" "${tag_filters[@]+"${tag_filters[@]}"}" \
        && rows+=("$_CLAST_ENTRY_ROW_JSON")
    done < <(find "$entries_dir" -mindepth 1 -maxdepth 1 -type f -name '*.md' 2>/dev/null | sort)
  fi

  local rows_json='[]'
  if (( ${#rows[@]} > 0 )); then
    rows_json="$(printf '%s\n' "${rows[@]}" | jq -cs 'sort_by(.date + "T" + .time) | reverse')"
  fi
  if [[ -n "$limit" ]]; then
    rows_json="$(jq -c --argjson n "$limit" '.[0:$n]' <<<"$rows_json")"
  fi

  if [[ -n "${CLAST_JSON:-}" ]]; then
    printf '%s\n' "$rows_json"
    return 0
  fi

  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi

  printf '%-50s %s\n' "entry" "tags"

  local n i row r_path r_basename r_tags tags_disp
  n="$(jq 'length' <<<"$rows_json")"
  for (( i = 0; i < n; i++ )); do
    row="$(jq -c ".[$i]" <<<"$rows_json")"
    r_path="$(jq -r '.path // ""' <<<"$row")"
    r_basename="$(basename "$r_path")"
    r_tags="$(jq -r '.tags // [] | join(",")' <<<"$row")"
    tags_disp="$r_tags"
    if (( ${#tags_disp} > 30 )); then
      tags_disp="${tags_disp:0:29}…"
    fi
    printf '%-50s %s\n' "$r_basename" "$tags_disp"
  done
}

# _clast_entries_list_consider <file> <day> <since> <until> <project> <tag_count> <tags...>
#   On match, set _CLAST_ENTRY_ROW_JSON globally and return 0. On miss, return 1.
_clast_entries_list_consider() {
  local file="$1" day="$2" since="$3" until="$4" project="$5" tag_count="$6"
  shift 6
  local -a wanted_tags=()
  if (( tag_count > 0 )); then
    wanted_tags=("$@")
  fi

  # Parse frontmatter.
  local fm_date="" fm_time="" fm_day_bucket="" fm_project="" fm_label=""
  local fm_session_id="" fm_session_slug="" fm_branch=""
  local fm_tags_raw=""
  local line key val
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    key="${line%%:*}"
    val="${line#*:}"
    # Trim leading whitespace from val.
    val="${val#"${val%%[![:space:]]*}"}"
    # Trim trailing whitespace.
    val="${val%"${val##*[![:space:]]}"}"
    case "$key" in
      date)         fm_date="$(_clast_entries_unquote "$val")" ;;
      time)         fm_time="$(_clast_entries_unquote "$val")" ;;
      day_bucket)   fm_day_bucket="$(_clast_entries_unquote "$val")" ;;
      project)      fm_project="$(_clast_entries_unquote "$val")" ;;
      label)
        if [[ "$val" != "null" ]]; then
          fm_label="$(_clast_entries_unquote "$val")"
        fi
        ;;
      session_id)   fm_session_id="$(_clast_entries_unquote "$val")" ;;
      session_slug) fm_session_slug="$(_clast_entries_unquote "$val")" ;;
      branch)
        if [[ "$val" != "null" ]]; then
          fm_branch="$(_clast_entries_unquote "$val")"
        fi
        ;;
      tags)         fm_tags_raw="$val" ;;
    esac
  done < <(_clast_entries_read_frontmatter "$file")

  # Window filter on day_bucket (fall back to date if day_bucket missing).
  local bucket="${fm_day_bucket:-$fm_date}"
  if [[ -n "$day" && "$bucket" != "$day" ]]; then return 1; fi
  if [[ -n "$since" && "$bucket" < "$since" ]]; then return 1; fi
  if [[ -n "$until" && "$bucket" > "$until" ]]; then return 1; fi

  if [[ -n "$project" && "$project" != "$fm_project" ]]; then return 1; fi

  # Parse tags array.
  local -a tags_arr=()
  if [[ "$fm_tags_raw" == "["*"]" ]]; then
    local inner="${fm_tags_raw#[}"
    inner="${inner%]}"
    if [[ -n "$inner" ]]; then
      local IFS=','
      read -r -a tags_arr <<<"$inner"
      unset IFS
      local k t
      for k in "${!tags_arr[@]}"; do
        t="${tags_arr[$k]}"
        t="${t#"${t%%[![:space:]]*}"}"
        t="${t%"${t##*[![:space:]]}"}"
        tags_arr[k]="$t"
      done
    fi
  fi

  # AND-intersection of tag filters.
  if (( ${#wanted_tags[@]} > 0 )); then
    local want have ok
    for want in "${wanted_tags[@]}"; do
      ok=0
      for have in "${tags_arr[@]+"${tags_arr[@]}"}"; do
        if [[ "$have" == "$want" ]]; then ok=1; break; fi
      done
      if (( ok == 0 )); then return 1; fi
    done
  fi

  local title
  title="$(_clast_entries_extract_title "$file")"

  # Build the JSON row.
  local tags_json='[]'
  if (( ${#tags_arr[@]} > 0 )); then
    tags_json="$(printf '%s\n' "${tags_arr[@]}" | jq -R . | jq -cs .)"
  fi

  _CLAST_ENTRY_ROW_JSON="$(jq -cn \
    --arg path "$file" \
    --arg date "$fm_date" \
    --arg time "$fm_time" \
    --arg day_bucket "$bucket" \
    --arg project "$fm_project" \
    --arg label "$fm_label" \
    --arg session_id "$fm_session_id" \
    --arg session_slug "$fm_session_slug" \
    --arg branch "$fm_branch" \
    --argjson tags "$tags_json" \
    --arg title "$title" \
    '{
       path: $path,
       date: $date,
       time: $time,
       day_bucket: $day_bucket,
       project: $project,
       label: (if $label == "" then null else $label end),
       session_id: $session_id,
       session_slug: $session_slug,
       branch: (if $branch == "" then null else $branch end),
       tags: $tags,
       title: (if $title == "" then null else $title end)
     }')"
  return 0
}

# ---------------------------------------------------------------------------
# read
# ---------------------------------------------------------------------------

_clast_entries_read() {
  local arg=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) _clast_entries_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_entries_err "read: unknown flag '$1'"; return 2 ;;
      *)
        if [[ -n "$arg" ]]; then
          _clast_entries_err "read: unexpected positional '$1'"; return 2
        fi
        arg="$1"; shift ;;
    esac
  done

  if [[ -z "$arg" ]]; then
    _clast_entries_err "read: missing <entry-path>"
    return 2
  fi

  local resolved
  if [[ "$arg" == /* && -f "$arg" ]]; then
    resolved="$arg"
  else
    resolved="$(clast_journal_dir)/entries/$arg"
  fi

  if [[ ! -f "$resolved" ]]; then
    _clast_entries_err "read: not found '$arg'" 1
    return 1
  fi

  if [[ -n "${CLAST_JSON:-}" ]]; then
    local content
    content="$(cat -- "$resolved")"
    jq -cn --arg path "$resolved" --arg content "$content" \
      '{path:$path, content:$content}'
    return 0
  fi

  cat -- "$resolved"
}

# ---------------------------------------------------------------------------
# write
# ---------------------------------------------------------------------------

_clast_entries_write() {
  local session_id="" slug="" tags_csv="" title=""
  local body_from="" body_stdin=0
  local tags_explicit=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --session)
        if [[ $# -lt 2 ]]; then _clast_entries_err "write: --session requires a value"; return 2; fi
        session_id="$2"; shift 2 ;;
      --session=*)  session_id="${1#*=}"; shift ;;
      --slug)
        if [[ $# -lt 2 ]]; then _clast_entries_err "write: --slug requires a value"; return 2; fi
        slug="$2"; shift 2 ;;
      --slug=*)     slug="${1#*=}"; shift ;;
      --tags)
        if [[ $# -lt 2 ]]; then _clast_entries_err "write: --tags requires a value"; return 2; fi
        tags_csv="$2"; tags_explicit=1; shift 2 ;;
      --tags=*)     tags_csv="${1#*=}"; tags_explicit=1; shift ;;
      --title)
        if [[ $# -lt 2 ]]; then _clast_entries_err "write: --title requires a value"; return 2; fi
        title="$2"; shift 2 ;;
      --title=*)    title="${1#*=}"; shift ;;
      --body-from)
        if [[ $# -lt 2 ]]; then _clast_entries_err "write: --body-from requires a value"; return 2; fi
        body_from="$2"; shift 2 ;;
      --body-from=*) body_from="${1#*=}"; shift ;;
      --body-stdin)  body_stdin=1; shift ;;
      -h|--help)     _clast_entries_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_entries_err "write: unknown flag '$1'"; return 2 ;;
      *)  _clast_entries_err "write: unexpected positional '$1'"; return 2 ;;
    esac
  done

  if [[ -z "$session_id" ]]; then
    _clast_entries_err "write: missing required flag '--session'"; return 2
  fi
  if ! [[ "$session_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
    _clast_entries_err "write: '$session_id' is not a valid UUID"; return 2
  fi
  if [[ -z "$slug" ]]; then
    _clast_entries_err "write: missing required flag '--slug'"; return 2
  fi
  if ! [[ "$slug" =~ ^[a-z0-9][a-z0-9-]{0,63}$ ]]; then
    _clast_entries_err "write: invalid --slug '$slug'"; return 2
  fi
  if [[ -n "$body_from" && $body_stdin -eq 1 ]]; then
    _clast_entries_err "write: --body-from and --body-stdin are mutually exclusive"; return 2
  fi
  if [[ -z "$body_from" && $body_stdin -eq 0 ]]; then
    _clast_entries_err "write: missing required flag '--body-from' or '--body-stdin'"; return 2
  fi
  if [[ -n "$title" && "$title" == *$'\n'* ]]; then
    _clast_entries_err "write: --title must not contain newlines"; return 2
  fi

  # Parse + validate tags.
  local -a tags=()
  if (( tags_explicit == 1 )) && [[ -n "$tags_csv" ]]; then
    local IFS=','
    read -r -a _raw_tags <<<"$tags_csv"
    unset IFS
    local rt trimmed
    for rt in "${_raw_tags[@]}"; do
      trimmed="${rt#"${rt%%[![:space:]]*}"}"
      trimmed="${trimmed%"${trimmed##*[![:space:]]}"}"
      [[ -z "$trimmed" ]] && continue
      trimmed="${trimmed,,}"
      if ! [[ "$trimmed" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]]; then
        _clast_entries_err "write: invalid tag '$trimmed'"; return 2
      fi
      tags+=("$trimmed")
    done
  fi

  # Look up manifest entry.
  local manifest_line
  if ! manifest_line="$(clast_manifest_lookup "$session_id" 2>/dev/null)" || [[ -z "$manifest_line" ]]; then
    _clast_entries_err "write: session '$session_id' not found in manifest" 1
    return 1
  fi

  local snapshot_rel mtime
  snapshot_rel="$(jq -r '.snapshot' <<<"$manifest_line")"
  mtime="$(jq -r '.source_mtime' <<<"$manifest_line")"
  # curated_source_mtime: stored in frontmatter for stale-curation detection

  local seg
  seg="$(awk -F/ 'NR==1{print $3}' <<<"$snapshot_rel")"

  # Resolve the *specific* registry line for this session's directory (by
  # path), not the first line that shares the slug. A slug may span several
  # directories (clones/worktrees), each with its own path and label;
  # slug-first-match would stamp every entry with the first line's path.
  local project_slug="" project_path="" project_remote="" project_label=""
  local reg_line=""
  if reg_line="$(clast_registry_line_for_path "$seg" 2>/dev/null)" && [[ -n "$reg_line" ]]; then
    project_slug="$(jq -r '.slug // empty' <<<"$reg_line")"
    project_path="$(jq -r '.path // empty' <<<"$reg_line")"
    project_remote="$(jq -r '.remote // empty' <<<"$reg_line")"
    project_label="$(jq -r '.label // empty' <<<"$reg_line")"
  else
    project_slug="$seg"
    local -a decoded=()
    mapfile -t decoded < <(clast_decode_candidates "$seg" 2>/dev/null)
    local d existing=()
    for d in "${decoded[@]+"${decoded[@]}"}"; do
      if [[ -d "$d" ]]; then existing+=("$d"); fi
    done
    if (( ${#existing[@]} == 1 )); then
      project_path="${existing[0]}"
    fi
    clast_log_warn "entries: write: segment '$seg' is not registered; using slug '$project_slug'"
  fi

  # Best-effort branch from snapshot.
  # Claude Code stores gitBranch (camelCase) on type:"user" lines, not on line 1.
  local journal_dir snapshot_abs branch=""
  journal_dir="$(clast_journal_dir)"
  snapshot_abs="$journal_dir/$snapshot_rel"
  if [[ -r "$snapshot_abs" ]]; then
    branch="$(grep -m1 '"gitBranch"' "$snapshot_abs" 2>/dev/null \
      | jq -r '.gitBranch // empty' 2>/dev/null || true)"
  fi

  local author machine
  author="${CLAST_AUTHOR:-${USER:-unknown}}"
  machine="${CLAST_MACHINE:-$(hostname)}"

  local today hhmm hhmm_compact
  today="$(clast_today)"
  hhmm="$(_clast_entries_now_hhmm)"
  hhmm_compact="$(_clast_entries_now_hhmm_compact)"

  # Body acquisition.
  local body=""
  if [[ -n "$body_from" ]]; then
    if [[ ! -r "$body_from" ]]; then
      _clast_entries_err "write: --body-from: cannot read '$body_from'" 1
      return 1
    fi
    body="$(cat -- "$body_from")"
  else
    body="$(cat)"
  fi
  # Empty / whitespace-only check.
  local body_stripped="${body//[[:space:]]/}"
  if [[ -z "$body_stripped" ]]; then
    _clast_entries_err "write: body is empty" 1
    return 1
  fi
  # Trim a single trailing newline at most; ensure exactly one trailing newline.
  if [[ "${body: -1}" == $'\n' ]]; then
    body="${body%$'\n'}"
  fi
  body+=$'\n'

  if [[ -n "$title" ]]; then
    body="# Session: $title"$'\n\n'"$body"
  fi

  # Compose frontmatter.
  local fm=""
  fm+="date: $(_clast_entries_yaml_string "$today")"$'\n'
  fm+="time: $(_clast_entries_yaml_string "$hhmm")"$'\n'
  fm+="day_bucket: $(_clast_entries_yaml_string "$today")"$'\n'
  fm+="project: $(_clast_entries_yaml_string "$project_slug")"$'\n'
  if [[ -n "$project_path" ]]; then
    fm+="project_path: $(_clast_entries_yaml_string "$project_path")"$'\n'
  else
    fm+="project_path: null"$'\n'
  fi
  if [[ -n "$project_label" ]]; then
    fm+="label: $(_clast_entries_yaml_string "$project_label")"$'\n'
  else
    fm+="label: null"$'\n'
  fi
  if [[ -n "$project_remote" ]]; then
    fm+="project_remote: $(_clast_entries_yaml_string "$project_remote")"$'\n'
  else
    fm+="project_remote: null"$'\n'
  fi
  if [[ -n "$branch" ]]; then
    fm+="branch: $(_clast_entries_yaml_string "$branch")"$'\n'
  else
    fm+="branch: null"$'\n'
  fi
  fm+="author: $(_clast_entries_yaml_string "$author")"$'\n'
  if (( ${#tags[@]} == 0 )); then
    fm+="tags: []"$'\n'
  else
    local joined="" t
    for t in "${tags[@]}"; do
      if [[ -n "$joined" ]]; then joined+=", "; fi
      joined+="$t"
    done
    fm+="tags: [$joined]"$'\n'
  fi
  fm+="session_id: $(_clast_entries_yaml_string "$session_id")"$'\n'
  fm+="session_slug: $(_clast_entries_yaml_string "$slug")"$'\n'
  fm+="snapshot_path: $(_clast_entries_yaml_string "$snapshot_rel")"$'\n'
  fm+="machine: $(_clast_entries_yaml_string "$machine")"$'\n'
  fm+="curated_source_mtime: $(_clast_entries_yaml_string "$mtime")"$'\n'

  local composed="---"$'\n'"$fm""---"$'\n\n'"$body"

  # Resolve target filename with collision suffixing.
  local entries_dir="$journal_dir/entries"
  if ! mkdir -p "$entries_dir"; then
    _clast_entries_err "write: failed to create '$entries_dir'" 1
    return 1
  fi
  local base="${today}-${hhmm_compact}-${project_slug}-${slug}"
  # Collapse leading dashes from segment-derived slug (but not internal `--`).
  base="${base/-\///}"  # no-op safety
  local target="$entries_dir/${base}.md"
  if [[ -e "$target" ]]; then
    local i found=""
    for (( i = 2; i <= 99; i++ )); do
      if [[ ! -e "$entries_dir/${base}-${i}.md" ]]; then
        target="$entries_dir/${base}-${i}.md"
        found=1
        break
      fi
    done
    if [[ -z "$found" ]]; then
      _clast_entries_err "write: too many collisions for ${base}.md" 1
      return 1
    fi
  fi

  if ! clast_atomic_write "$target" "$composed"; then
    _clast_entries_err "write: failed to write '$target'" 1
    return 1
  fi

  local basename_only
  basename_only="$(basename "$target")"

  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg path "$target" '{path:$path}'
    return 0
  fi
  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi
  printf 'Wrote entries/%s\n' "$basename_only"
}
