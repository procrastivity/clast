# clast-retro-lib.bash — the retro index pass (Round 1, step-01).
#
# Reads every curated journal entry's YAML front-matter and emits a per-entry
# index of the four fields the day→project grouping depends on:
#   session_id, project_path, snapshot_path, curated_source_mtime.
# Pure code, deterministic, read-only — no bucketing, dedup, render, or LLM
# (those are step-02 / step-03). The raw `snapshot_path` string is kept intact;
# parsing its day-bucket dir is step-02's job.
#
# Entries live at $(clast_journal_dir)/entries/*.md as Markdown with a leading
# `---`-fenced front-matter block. See docs/reference/cli.md#entry-frontmatter.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash

# clast_retro_index [<entries_dir>]
#   Print a JSON array to stdout — one element per *.md file in the entries
#   dir, sorted by absolute path ascending. Each element:
#     { path, session_id, project_path, snapshot_path, curated_source_mtime }
#   An absent, empty, or literal-`null` field is emitted as JSON null. A
#   missing or empty entries dir yields `[]`. Read-only; returns 0.
clast_retro_index() {
  local entries_dir="${1:-$(clast_journal_dir)/entries}"

  if [[ ! -d "$entries_dir" ]]; then
    printf '[]\n'
    return 0
  fi

  local -a rows=()
  local file
  while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    rows+=("$(_clast_retro_index_record "$file")")
  done < <(find "$entries_dir" -mindepth 1 -maxdepth 1 -type f -name '*.md' 2>/dev/null | sort)

  if (( ${#rows[@]} == 0 )); then
    printf '[]\n'
    return 0
  fi

  printf '%s\n' "${rows[@]}" | jq -cs 'sort_by(.path)'
}

# _clast_retro_index_record <path>
#   Parse one entry's front-matter and emit a single compact JSON object with
#   the indexed fields. Empty / literal-`null` values become JSON null.
_clast_retro_index_record() {
  local file="$1"
  local fm_session_id="" fm_project_path="" fm_snapshot_path="" fm_mtime=""
  local line key val
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    key="${line%%:*}"
    val="${line#*:}"
    # Trim surrounding whitespace from the value.
    val="${val#"${val%%[![:space:]]*}"}"
    val="${val%"${val##*[![:space:]]}"}"
    case "$key" in
      session_id)           fm_session_id="$(clast_yaml_unquote "$val")" ;;
      project_path)         fm_project_path="$(clast_yaml_unquote "$val")" ;;
      snapshot_path)        fm_snapshot_path="$(clast_yaml_unquote "$val")" ;;
      curated_source_mtime) fm_mtime="$(clast_yaml_unquote "$val")" ;;
    esac
  done < <(clast_read_frontmatter "$file")

  # A literal YAML `null` reads back as the string "null"; collapse it (and any
  # absent/empty field) to the empty marker so jq emits JSON null.
  [[ "$fm_session_id"   == "null" ]] && fm_session_id=""
  [[ "$fm_project_path" == "null" ]] && fm_project_path=""
  [[ "$fm_snapshot_path" == "null" ]] && fm_snapshot_path=""
  [[ "$fm_mtime"        == "null" ]] && fm_mtime=""

  jq -cn \
    --arg path "$file" \
    --arg session_id "$fm_session_id" \
    --arg project_path "$fm_project_path" \
    --arg snapshot_path "$fm_snapshot_path" \
    --arg curated_source_mtime "$fm_mtime" \
    '{
       path: $path,
       session_id:           (if $session_id == "" then null else $session_id end),
       project_path:         (if $project_path == "" then null else $project_path end),
       snapshot_path:        (if $snapshot_path == "" then null else $snapshot_path end),
       curated_source_mtime: (if $curated_source_mtime == "" then null else $curated_source_mtime end)
     }'
}

# ---------------------------------------------------------------------------
# step-02: work-day bucketing + session dedup
# ---------------------------------------------------------------------------

# _clast_retro_work_day <snapshot_path> <curated_source_mtime>
#   Resolve the day work actually happened. Primary: the <day> dir of
#   snapshot_path (transcripts/<day>/<seg>/<sid>.jsonl). Fallback: the local
#   cutoff-adjusted day of curated_source_mtime. Neither → "unknown".
_clast_retro_work_day() {
  local snapshot_path="$1" mtime="$2"
  local day=""
  if [[ -n "$snapshot_path" ]]; then
    day="$(awk -F/ '{print $2}' <<<"$snapshot_path")"
    if [[ "$day" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
      printf '%s' "$day"
      return 0
    fi
  fi
  if [[ -n "$mtime" ]]; then
    local epoch
    if epoch="$(date -d "$mtime" +%s 2>/dev/null)" && [[ -n "$epoch" ]]; then
      clast_day_bucket_for_epoch "$epoch"
      return 0
    fi
  fi
  printf 'unknown'
}

# _clast_retro_file_date <path>
#   The curation (filename) date: leading YYYY-MM-DD of the basename. Empty if
#   the name does not start with an ISO date.
_clast_retro_file_date() {
  local base
  base="$(basename "$1")"
  if [[ "$base" =~ ^([0-9]{4}-[0-9]{2}-[0-9]{2}) ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
  fi
}

# clast_retro_manifest [--from DATE] [--to DATE] [--window work-days|file-dates]
#   Consume the step-01 index, assign each entry to its work day, dedup by
#   session_id (later day wins; contributing entry paths merged into entries[]),
#   group day → project, and honor the date window under the chosen scope.
#   Prints the manifest JSON to stdout. Read-only; returns 0 (2 on bad args).
clast_retro_manifest() {
  local from="" to="" window="work-days"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --from)
        if [[ $# -lt 2 ]]; then clast_log_error "retro: --from requires a value"; return 2; fi
        if ! from="$(clast_parse_date "$2" 2>/dev/null)"; then
          clast_log_error "retro: invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --from=*)
        if ! from="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          clast_log_error "retro: invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --to)
        if [[ $# -lt 2 ]]; then clast_log_error "retro: --to requires a value"; return 2; fi
        if ! to="$(clast_parse_date "$2" 2>/dev/null)"; then
          clast_log_error "retro: invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --to=*)
        if ! to="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          clast_log_error "retro: invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --window)
        if [[ $# -lt 2 ]]; then clast_log_error "retro: --window requires a value"; return 2; fi
        window="$2"; shift 2 ;;
      --window=*) window="${1#*=}"; shift ;;
      *) clast_log_error "retro: unknown argument '$1'"; return 2 ;;
    esac
  done

  case "$window" in
    work-days|file-dates) ;;
    *) clast_log_error "retro: --window must be 'work-days' or 'file-dates'"; return 2 ;;
  esac

  # Enrich each indexed entry with its work day + filename date (bash owns the
  # cutoff/epoch math; jq owns the filter/group/sort below).
  local -a enriched=()
  local rec sp mt path wd fd
  while IFS= read -r rec; do
    [[ -z "$rec" ]] && continue
    sp="$(jq -r '.snapshot_path // ""' <<<"$rec")"
    mt="$(jq -r '.curated_source_mtime // ""' <<<"$rec")"
    path="$(jq -r '.path' <<<"$rec")"
    wd="$(_clast_retro_work_day "$sp" "$mt")"
    fd="$(_clast_retro_file_date "$path")"
    enriched+=("$(jq -c --arg wd "$wd" --arg fd "$fd" \
      '. + {work_day: $wd, file_date: (if $fd == "" then null else $fd end)}' <<<"$rec")")
  done < <(clast_retro_index | jq -c '.[]')

  if (( ${#enriched[@]} == 0 )); then
    jq -cn --arg from "$from" --arg to "$to" --arg window "$window" \
      '{from: (if $from == "" then null else $from end),
        to:   (if $to == "" then null else $to end),
        window: $window, days: []}'
    return 0
  fi

  local manifest
  manifest="$(printf '%s\n' "${enriched[@]}" | jq -s \
    --arg from "$from" --arg to "$to" --arg window "$window" '
    def within($day): ($from == "" or $day >= $from) and ($to == "" or $day <= $to);

    # file-dates scope filters entries by filename date *before* dedup.
    (if $window == "file-dates"
     then [ .[] | select(.file_date != null and within(.file_date)) ]
     else . end)

    # Dedup by session_id: later real day wins; merge contributing paths.
    | [ group_by(.session_id)[]
        | (map(.work_day) | map(select(. != "unknown"))
           | (if length > 0 then max else "unknown" end)) as $wd
        | ((map(select(.work_day == $wd))[0]) // .[0]) as $rep
        | { session_id: .[0].session_id,
            work_day: $wd,
            entries: (map(.path) | sort),
            project_path: ($rep.project_path
              // (map(.project_path) | map(select(. != null))[0])),
            curated_source_mtime: ($rep.curated_source_mtime
              // (map(.curated_source_mtime) | map(select(. != null))[0])) } ]

    # work-days scope filters resolved sessions by their work day.
    | (if $window == "work-days"
       then [ .[] | select(if .work_day == "unknown"
                           then ($from == "" and $to == "")
                           else within(.work_day) end) ]
       else . end)

    # Group day → project, with deterministic ordering.
    | { from: (if $from == "" then null else $from end),
        to:   (if $to == "" then null else $to end),
        window: $window,
        days: (
          group_by(.work_day)          # ascending; "unknown" sorts last
          | map({
              day: .[0].work_day,
              curation_dates: ([.[].entries[] | sub(".*/"; "") | .[0:10]]
                               | map(select(test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$")))
                               | unique),
              projects: (
                group_by(.project_path)
                | map({
                    project_path: .[0].project_path,
                    sessions: (sort_by(.session_id)
                      | map({session_id, work_day, entries, curated_source_mtime}))
                  })
                | sort_by(.project_path == null)   # null project group sorts last
              )
            })
        ) }')"

  _clast_retro_inject_project_names "$manifest"
}

# _clast_retro_inject_project_names <manifest-json>
#   Add a friendly `project_name` to every project group (display polish; the
#   raw project_path is kept). Names are computed in bash (clast_retro_friendly_name
#   needs $HOME + string ops) and merged back via jq, keyed by project_path.
_clast_retro_inject_project_names() {
  local manifest="$1"
  local -a pairs=()
  local pp name
  while IFS= read -r pp; do
    if [[ "$pp" == "null" ]]; then
      name="$(clast_retro_friendly_name "")"
      pairs+=("$(jq -cn --arg n "$name" '{path: null, name: $n}')")
    else
      name="$(clast_retro_friendly_name "$pp")"
      pairs+=("$(jq -cn --arg p "$pp" --arg n "$name" '{path: $p, name: $n}')")
    fi
  done < <(jq -r '[.days[].projects[].project_path] | unique | .[]
                  | if . == null then "null" else . end' <<<"$manifest")

  if (( ${#pairs[@]} == 0 )); then
    printf '%s\n' "$manifest"
    return 0
  fi

  # Manifest on stdin (it can be large with many sessions), name pairs via
  # --slurpfile — keep both off argv (MAX_ARG_STRLEN).
  printf '%s' "$manifest" | jq -c --slurpfile pairs <(printf '%s\n' "${pairs[@]}") '
    (reduce $pairs[] as $p ({}; .[($p.path | tostring)] = $p.name)) as $names
    | .days |= map(.projects |= map(
        . + {project_name: ($names[(.project_path | tostring)] // "(no project)")} ))
  '
}

# ---------------------------------------------------------------------------
# Session body assembly (shared by the Round 1 render and the `--bodies` JSON)
# ---------------------------------------------------------------------------

# _clast_retro_trim_body
#   Drop leading blank lines and leading `# Session:` heading(s) from an entry
#   body on stdin; pass the rest through unchanged.
_clast_retro_trim_body() {
  awk '
    started { print; next }
    /^[[:space:]]*$/ { next }
    /^# Session:/ { next }
    { started = 1; print }
  '
}

# clast_retro_is_interrupted  (stdin = session body; exit 0 if interrupted)
#   An interrupted session has a goal and/or open threads but nothing shipped —
#   work was started and left hanging. Flag it rather than overstate or drop it.
clast_retro_is_interrupted() {
  local body
  body="$(cat)"
  if grep -qiE '^#+ +what shipped' <<<"$body"; then
    return 1
  fi
  grep -qiE '^#+ +(goal|open threads)' <<<"$body"
}

# clast_retro_session_body <session-json>
#   Concatenate the trimmed bodies of a session's entries[] in order. For a
#   merged (multi-entry) session each body is preceded by a "--- <file> ---"
#   marker so the split is visible. Unreadable entries emit a notice line.
clast_retro_session_body() {
  local sess="$1"
  local ne ei entry
  ne="$(jq '.entries | length' <<<"$sess")"
  for (( ei = 0; ei < ne; ei++ )); do
    entry="$(jq -r ".entries[$ei]" <<<"$sess")"
    if (( ne > 1 )); then
      printf '  --- %s ---\n' "$(basename "$entry")"
    fi
    if [[ -r "$entry" ]]; then
      clast_entry_body "$entry" | _clast_retro_trim_body
    else
      printf '  (entry not readable: %s)\n' "$entry"
    fi
  done
}

# ---------------------------------------------------------------------------
# Friendly project names (step-05)
# ---------------------------------------------------------------------------

# clast_retro_friendly_name <project_path|encoded-segment|empty>
#   A short, readable name for a project group. Path-derived (the registry slug
#   is already dash-joined, so label/slug doesn't yield the wanted form):
#     - empty / "null"                  -> "(no project)"
#     - a leading-"-" encoded segment   -> decoded to a path first
#     - == $HOME                        -> "~"
#     - under $HOME, >=3 components      -> last two (…/Workspaces/dev/xesapps -> dev/xesapps)
#     - under $HOME, otherwise           -> "~/<rest>" (~/Code/clast, ~/fix)
#     - elsewhere, >=3 components        -> last two
#     - elsewhere, otherwise             -> the path verbatim (/tmp/projA)
clast_retro_friendly_name() {
  local p="$1"
  if [[ -z "$p" || "$p" == "null" ]]; then
    printf '(no project)'
    return 0
  fi

  # Decode an encoded snapshot segment (…/ -> -, literal - -> --).
  if [[ "$p" == -* ]]; then
    p="${p//--/$'\x01'}"
    p="${p//-//}"
    p="${p//$'\x01'/-}"
  fi

  p="${p%/}"  # drop a trailing slash

  local home="${HOME%/}"
  if [[ -n "$home" && "$p" == "$home" ]]; then
    printf '~'
    return 0
  fi

  local rest="" tilde=0
  if [[ -n "$home" && "$p" == "$home/"* ]]; then
    rest="${p#"$home"/}"
    tilde=1
  else
    rest="${p#/}"   # strip a single leading slash for component counting
  fi

  # Count components.
  local -a parts=()
  local IFS='/'
  read -r -a parts <<<"$rest"
  unset IFS

  if (( ${#parts[@]} >= 3 )); then
    # Last two components, regardless of home/elsewhere.
    printf '%s/%s' "${parts[${#parts[@]}-2]}" "${parts[${#parts[@]}-1]}"
    return 0
  fi

  if (( tilde )); then
    # shellcheck disable=SC2088  # literal "~" for display, not path expansion
    printf '~/%s' "$rest"
  else
    printf '%s' "$1"   # short non-home path: verbatim original (keep leading /)
  fi
}
