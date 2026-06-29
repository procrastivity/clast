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
