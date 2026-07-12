# clast brief — LLM-powered project briefing.
#
# Replicates the /brief plugin skill using an OpenAI-compatible chat
# completions endpoint. Reads curated entries, breadcrumbs, and today's
# sessions for a project (via clast-plumbing), then synthesizes a briefing.
#
# Usage: clast brief [<project-slug>] [-h|--help]
#   If no slug is given, resolves from the current working directory.
#   -h, --help prints usage and exits.
# shellcheck shell=bash

# --- Resolve project ---------------------------------------------------------

_clast_brief_resolve_project() {
  local slug="${1:-}"

  if [[ -n "$slug" ]]; then
    printf '%s' "$slug"
    return
  fi

  local resolved
  resolved="$(clast-plumbing registry resolve "$(pwd)" 2>/dev/null)" || true
  if [[ -n "$resolved" ]]; then
    printf '%s' "$resolved"
    return
  fi

  clast_porcelain_die "Not in a registered project. Run \`clast-plumbing registry add .\` first, or invoke as \`clast brief <slug>\`."
}

# --- Gather data -------------------------------------------------------------

# Group key for an entry: per-directory label, else branch, else "default".
# A slug may span several directories; grouping keeps their work distinct
# instead of letting the single newest entry stand in for the whole project.
_clast_brief_gather_entries() {
  local project="$1" current_label="${2:-}"

  # Pull the full entries list (no global cap). A global `--limit` here would
  # let one busy clone with N newer entries push the current clone out of the
  # window entirely before we ever see it — even though the per-group/total
  # caps below already keep the prompt within the brief's token budget.
  local entries_json
  entries_json="$(clast-plumbing --json entries --project "$project" 2>/dev/null)" || true

  if [[ -z "$entries_json" ]] || [[ "$(jq 'length' <<<"$entries_json" 2>/dev/null)" == "0" ]]; then
    return
  fi

  # Ordered, de-duplicated group keys in newest-first order of first
  # appearance (entries list is already sorted newest-first). If a
  # current_label was provided AND it appears in the entries, hoist it to
  # the front so the active thread gets its share before total_cap is hit.
  local -a groups=()
  mapfile -t groups < <(jq -r --arg cur "$current_label" '
    [ .[] | (.label // .branch // "default") ] as $keys
    | (reduce $keys[] as $k ([]; if index($k) then . else . + [$k] end)) as $uniq
    | (if $cur != "" and ($uniq | index($cur)) != null
         then [$cur] + ($uniq - [$cur])
         else $uniq end)
    | .[]
  ' <<<"$entries_json")

  # A single-workspace project renders exactly as before (no group headers),
  # so this change is a strict superset for the common case.
  local single_group=0
  (( ${#groups[@]} <= 1 )) && single_group=1

  # Per-group and total caps keep the prompt within the brief's token budget.
  local per_group=3 total_cap=8 emitted=0
  local result="" g

  for g in "${groups[@]}"; do
    (( emitted >= total_cap )) && break

    local -a paths=()
    mapfile -t paths < <(jq -r --arg g "$g" --argjson per "$per_group" '
      [ .[] | select((.label // .branch // "default") == $g) ][0:$per] | .[].path
    ' <<<"$entries_json")
    (( ${#paths[@]} == 0 )) && continue

    # Build this group's body first, so we can skip the header if nothing reads.
    local group_block="" p entry_body
    for p in "${paths[@]}"; do
      (( emitted >= total_cap )) && break
      entry_body="$(clast-plumbing entries read "$p" 2>/dev/null)" || true
      if [[ -z "$entry_body" ]]; then continue; fi
      if [[ -n "$group_block" ]]; then
        group_block="${group_block}

---

"
      fi
      group_block="${group_block}${entry_body}"
      emitted=$(( emitted + 1 ))
    done
    [[ -z "$group_block" ]] && continue

    local block="$group_block"
    if (( single_group == 0 )); then
      local branch_disp
      branch_disp="$(jq -r --arg g "$g" '
        [ .[] | select((.label // .branch // "default") == $g) ][0].branch // ""
      ' <<<"$entries_json")"
      local header="## Workspace: $g"
      if [[ -n "$branch_disp" && "$branch_disp" != "null" ]]; then
        header="$header (branch: $branch_disp)"
      fi
      block="${header}

${group_block}"
    fi

    if [[ -n "$result" ]]; then
      result="${result}

"
    fi
    result="${result}${block}"
  done

  printf '%s' "$result"
}

_clast_brief_gather_breadcrumbs() {
  local project="$1"
  clast-plumbing breadcrumb --read --project "$project" --day today 2>/dev/null || true
}

_clast_brief_gather_sessions() {
  # NOTE: today's sessions come from the manifest, not curated entries, so
  # they are not (yet) grouped by workspace label the way entries are. Per-
  # directory session grouping is a possible follow-up.
  local project="$1"
  local sessions_json
  sessions_json="$(clast-plumbing --json sessions --day today --project "$project" 2>/dev/null)" || true

  if [[ -z "$sessions_json" ]] || [[ "$(jq 'length' <<<"$sessions_json" 2>/dev/null)" == "0" ]]; then
    return
  fi

  local n
  n="$(jq 'length' <<<"$sessions_json")"

  if (( n > 5 )); then
    local latest_start
    latest_start="$(jq -r 'sort_by(.start) | last | .start // ""' <<<"$sessions_json")"
    printf 'Worked %d sessions today, most recent at %s.' "$n" "${latest_start:11:5}"
    return
  fi

  jq -r '
    sort_by(.start) | .[] |
    "\(.start[11:16]) start: \(if .branch and .branch != "null" then .branch else "no branch" end), \(.msg_count_approx) messages"
  ' <<<"$sessions_json" 2>/dev/null || true
}

# --- User prompt -------------------------------------------------------------

_clast_brief_build_user_prompt() {
  local project="$1" entries="$2" breadcrumbs="$3" sessions="$4" current_label="$5"

  local template_file template
  template_file="$(clast_porcelain_user_prompt_file brief-user)"

  if [[ -n "$template_file" ]]; then
    template="$(cat "$template_file")"
    template="${template//\{\{project\}\}/${project}}"
    template="${template//\{\{current_label\}\}/${current_label:-unknown}}"
    template="${template//\{\{entries\}\}/${entries:-None.}}"
    template="${template//\{\{breadcrumbs\}\}/${breadcrumbs:-None.}}"
    template="${template//\{\{sessions\}\}/${sessions:-None.}}"
    printf '%s' "$template"
  else
    clast_porcelain_warn "user prompt template not found: brief-user.md — using inline fallback"
    cat <<EOF
Project: ${project}
Current workspace (the directory you are in now): ${current_label:-unknown}

Recent curated entries, grouped by workspace (newest first):
${entries:-None.}

Today's breadcrumbs for this project:
${breadcrumbs:-None.}

Today's session activity for this project:
${sessions:-None.}
EOF
  fi
}

# --- Main --------------------------------------------------------------------

_clast_brief_usage() {
  cat <<'EOF'
Usage: clast brief [<project-slug>]

Replicate the /brief plugin skill: synthesize an LLM briefing from curated
entries, breadcrumbs, and today's sessions for a project. If no slug is
given, resolves the project from the current working directory.

Flags:
  -h, --help  Print this usage and exit.

Requires the CLAST_LLM_* env vars (see `clast --help`).
EOF
}

clast_cmd_brief() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) _clast_brief_usage; return 0 ;;
      --)        shift; break ;;
      -*)        clast_porcelain_log_error "brief: unknown argument '$1'"; return 2 ;;
      *)         break ;;
    esac
  done

  clast_porcelain_preflight_llm

  local project
  project="$(_clast_brief_resolve_project "${1:-}")"
  clast_porcelain_info "Briefing for project: $project"

  # When the project was resolved from the current directory (no positional
  # slug), find that directory's workspace label so the briefing can prefer
  # the active thread for the clone the user is actually in.
  local current_label=""
  if [[ -z "${1:-}" ]]; then
    current_label="$(clast-plumbing registry resolve "$(pwd)" --json 2>/dev/null | jq -r '.label // empty' 2>/dev/null)" || true
  fi

  clast_porcelain_info "Gathering context..."

  local entries breadcrumbs sessions
  entries="$(_clast_brief_gather_entries "$project" "$current_label")"
  breadcrumbs="$(_clast_brief_gather_breadcrumbs "$project")"
  sessions="$(_clast_brief_gather_sessions "$project")"

  if [[ -z "$entries" && -z "$breadcrumbs" && -z "$sessions" ]]; then
    clast_porcelain_info "No curated entries, breadcrumbs, or sessions for \`$project\`."
    clast_porcelain_info "Run \`clast wake\` to curate recent sessions first, or run \`clast-plumbing sessions --project $project\` to see what's available."
    return 0
  fi

  local system_prompt user_prompt
  system_prompt="$(clast_porcelain_load_system_prompt brief-system)"
  user_prompt="$(_clast_brief_build_user_prompt "$project" "$entries" "$breadcrumbs" "$sessions" "$current_label")"

  clast_porcelain_info "Synthesizing briefing..."
  printf '\n'

  local briefing
  if ! briefing="$(clast_porcelain_llm_chat "$system_prompt" "$user_prompt")"; then
    clast_porcelain_die "LLM call failed"
  fi

  printf '%s\n' "$briefing"
}
