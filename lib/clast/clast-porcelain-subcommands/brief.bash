# clast brief — LLM-powered project briefing.
#
# Replicates the /brief plugin skill using an OpenAI-compatible chat
# completions endpoint. Reads curated entries, breadcrumbs, and today's
# sessions for a project (via clast-plumbing), then synthesizes a briefing.
#
# Usage: clast brief [<project-slug>]
#   If no slug is given, resolves from the current working directory.
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

_clast_brief_gather_entries() {
  local project="$1"
  local entries_json
  entries_json="$(clast-plumbing --json entries --project "$project" --limit 5 2>/dev/null)" || true

  if [[ -z "$entries_json" ]] || [[ "$(jq 'length' <<<"$entries_json" 2>/dev/null)" == "0" ]]; then
    return
  fi

  local n i entry_meta entry_path entry_body result=""
  n="$(jq 'length' <<<"$entries_json")"

  for (( i = 0; i < n; i++ )); do
    entry_meta="$(jq -c ".[$i]" <<<"$entries_json")"
    entry_path="$(jq -r '.path' <<<"$entry_meta")"

    entry_body="$(clast-plumbing entries read "$entry_path" 2>/dev/null)" || true
    if [[ -z "$entry_body" ]]; then
      local title date
      title="$(jq -r '.title // "untitled"' <<<"$entry_meta")"
      date="$(jq -r '.date' <<<"$entry_meta")"
      entry_body="# $date — $title (body not available)"
    fi

    if [[ -n "$result" ]]; then
      result="${result}

---

"
    fi
    result="${result}${entry_body}"
  done

  printf '%s' "$result"
}

_clast_brief_gather_breadcrumbs() {
  local project="$1"
  clast-plumbing breadcrumb --read --project "$project" --day today 2>/dev/null || true
}

_clast_brief_gather_sessions() {
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
  local project="$1" entries="$2" breadcrumbs="$3" sessions="$4"

  local template_file template
  template_file="$(clast_porcelain_user_prompt_file brief-user)"

  if [[ -n "$template_file" ]]; then
    template="$(cat "$template_file")"
    template="${template//\{\{project\}\}/${project}}"
    template="${template//\{\{entries\}\}/${entries:-None.}}"
    template="${template//\{\{breadcrumbs\}\}/${breadcrumbs:-None.}}"
    template="${template//\{\{sessions\}\}/${sessions:-None.}}"
    printf '%s' "$template"
  else
    clast_porcelain_warn "user prompt template not found: brief-user.md — using inline fallback"
    cat <<EOF
Project: ${project}

Recent curated entries (newest first):
${entries:-None.}

Today's breadcrumbs for this project:
${breadcrumbs:-None.}

Today's session activity for this project:
${sessions:-None.}
EOF
  fi
}

# --- Main --------------------------------------------------------------------

clast_cmd_brief() {
  clast_porcelain_preflight_llm

  local project
  project="$(_clast_brief_resolve_project "${1:-}")"
  clast_porcelain_info "Briefing for project: $project"

  clast_porcelain_info "Gathering context..."

  local entries breadcrumbs sessions
  entries="$(_clast_brief_gather_entries "$project")"
  breadcrumbs="$(_clast_brief_gather_breadcrumbs "$project")"
  sessions="$(_clast_brief_gather_sessions "$project")"

  if [[ -z "$entries" && -z "$breadcrumbs" && -z "$sessions" ]]; then
    clast_porcelain_info "No curated entries, breadcrumbs, or sessions for \`$project\`."
    clast_porcelain_info "Run \`clast wake\` to curate recent sessions first, or run \`clast-plumbing sessions --project $project\` to see what's available."
    return 0
  fi

  local system_prompt user_prompt
  system_prompt="$(clast_porcelain_load_system_prompt brief-system)"
  user_prompt="$(_clast_brief_build_user_prompt "$project" "$entries" "$breadcrumbs" "$sessions")"

  clast_porcelain_info "Synthesizing briefing..."
  printf '\n'

  local briefing
  if ! briefing="$(clast_porcelain_llm_chat "$system_prompt" "$user_prompt")"; then
    clast_porcelain_die "LLM call failed"
  fi

  printf '%s\n' "$briefing"
}
