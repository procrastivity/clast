# clast retro — LLM-condensed work retrospective grouped by work day → project.
#
# Round 2 of the retro feature. Structure comes from the deterministic core
# (`clast-plumbing retro --json --bodies`); the model only condenses each
# session's body into a few retro bullets. Summaries are cached per session_id
# (content-fingerprinted) under the journal dir, so re-runs are free unless a
# session changed or --refresh is passed. See .wip/initiatives/clast-retro/.
# shellcheck shell=bash

_clast_retrosum_usage() {
  cat <<'EOF'
Usage: clast retro [--from DATE] [--to DATE] [--window work-days|file-dates]
                   [--refresh] [--json]

Condense the work retrospective (grouped by actual work day → project) into
model-written bullets per session. Structure is deterministic; only the prose
condensation calls the LLM. Summaries are cached per session under
<journal>/.retro-summaries/ and reused until the session content changes.

Flags:
  --from DATE     Start of the window (inclusive). Default: corpus start.
  --to DATE       End of the window (inclusive). Default: corpus end.
  --window WHICH  work-days (default) | file-dates. See `clast-plumbing retro`.
  --refresh       Ignore cached summaries and re-summarize (rewrites the cache).
  --json          Emit the manifest with a `summary` per session (no render).
  -h, --help      Print this usage and exit.

Requires the CLAST_LLM_* env vars (see `clast --help`).
EOF
}

# _clast_retrosum_fingerprint  (stdin → short hex/cksum on stdout)
#   Content fingerprint of a session body; changes invalidate the cache.
_clast_retrosum_fingerprint() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | cut -c1-16
  else
    cksum | awk '{print $1}'
  fi
}

# _clast_retrosum_build_user <project> <work_day> <session_id> <body>
_clast_retrosum_build_user() {
  local project="$1" work_day="$2" sid="$3" body="$4"
  local tf tpl
  tf="$(clast_porcelain_user_prompt_file retro-summary-user)"
  if [[ -n "$tf" ]]; then
    tpl="$(cat "$tf")"
    tpl="${tpl//\{\{project\}\}/$project}"
    tpl="${tpl//\{\{work_day\}\}/$work_day}"
    tpl="${tpl//\{\{session_id\}\}/$sid}"
    tpl="${tpl//\{\{body\}\}/$body}"
    printf '%s' "$tpl"
  else
    printf 'Project: %s\nWork day: %s\nSession id: %s\n\nEntry body:\n%s\n' \
      "$project" "$work_day" "$sid" "$body"
  fi
}

# _clast_retrosum_summary <project> <work_day> <session_id> <body> <cache_dir> <refresh>
#   Echo the condensed summary. Cache hit (matching fingerprint) reuses; miss
#   calls the LLM and writes the cache. Returns nonzero on LLM failure.
_clast_retrosum_summary() {
  local project="$1" work_day="$2" sid="$3" body="$4" cache_dir="$5" refresh="$6"
  local fp cache_file cached_fp
  fp="$(printf '%s' "$body" | _clast_retrosum_fingerprint)"
  cache_file="$cache_dir/$sid.json"

  if (( ! refresh )) && [[ -r "$cache_file" ]]; then
    cached_fp="$(jq -r '.fingerprint // empty' "$cache_file" 2>/dev/null)"
    if [[ -n "$cached_fp" && "$cached_fp" == "$fp" ]]; then
      jq -r '.summary // empty' "$cache_file"
      return 0
    fi
  fi

  local system user summary
  system="$(clast_porcelain_load_system_prompt retro-summary-system)"
  user="$(_clast_retrosum_build_user "$project" "$work_day" "$sid" "$body")"
  if ! summary="$(clast_porcelain_llm_chat "$system" "$user")"; then
    return 1
  fi

  if mkdir -p "$cache_dir" 2>/dev/null; then
    jq -n --arg fp "$fp" --arg s "$summary" '{fingerprint:$fp, summary:$s}' \
      >"$cache_file" 2>/dev/null || clast_porcelain_warn "failed to cache summary for $sid"
  fi
  printf '%s' "$summary"
}

# _clast_retrosum_journal_dir — resolve the journal dir (for the cache).
_clast_retrosum_journal_dir() {
  if [[ -n "${CLAST_JOURNAL_DIR:-}" ]]; then
    printf '%s' "$CLAST_JOURNAL_DIR"
    return
  fi
  local jd
  jd="$(clast-plumbing whereami 2>/dev/null | awk '/^journal_dir:/{print $2}')" || true
  printf '%s' "${jd:-$HOME/.claude/journal}"
}

clast_cmd_retro() {
  local from="" to="" window="work-days" refresh=0 as_json=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --from)     [[ $# -lt 2 ]] && { clast_porcelain_log_error "retro: --from requires a value"; return 2; }; from="$2"; shift 2 ;;
      --from=*)   from="${1#*=}"; shift ;;
      --to)       [[ $# -lt 2 ]] && { clast_porcelain_log_error "retro: --to requires a value"; return 2; }; to="$2"; shift 2 ;;
      --to=*)     to="${1#*=}"; shift ;;
      --window)   [[ $# -lt 2 ]] && { clast_porcelain_log_error "retro: --window requires a value"; return 2; }; window="$2"; shift 2 ;;
      --window=*) window="${1#*=}"; shift ;;
      --refresh)  refresh=1; shift ;;
      --json)     as_json=1; shift ;;
      -h|--help)  _clast_retrosum_usage; return 0 ;;
      --) shift; break ;;
      *) clast_porcelain_log_error "retro: unknown argument '$1'"; return 2 ;;
    esac
  done

  clast_porcelain_preflight_llm

  # Structure + per-session bodies from the deterministic core.
  local -a pl=(--json retro --bodies --window "$window")
  [[ -n "$from" ]] && pl+=(--from "$from")
  [[ -n "$to" ]] && pl+=(--to "$to")
  local manifest
  if ! manifest="$(clast-plumbing "${pl[@]}" 2>/dev/null)"; then
    local msg
    msg="$(jq -r '.error // empty' <<<"$manifest" 2>/dev/null || true)"
    clast_porcelain_die "retro: ${msg:-failed to build manifest}" 2
  fi

  local cache_dir
  cache_dir="$(_clast_retrosum_journal_dir)/.retro-summaries"

  # Summarize each session; collect session_id → summary.
  local -a summary_pairs=()
  local sess sid project work_day body title summary
  while IFS= read -r sess; do
    [[ -z "$sess" ]] && continue
    sid="$(jq -r '.session_id' <<<"$sess")"
    work_day="$(jq -r '.work_day' <<<"$sess")"
    body="$(jq -r '.body // ""' <<<"$sess")"
    project="$(jq -r '.project_path // "(no project)"' <<<"$sess")"
    if [[ -z "$body" ]]; then
      summary="(no body to summarize)"
    elif ! summary="$(_clast_retrosum_summary "$project" "$work_day" "$sid" "$body" "$cache_dir" "$refresh")"; then
      clast_porcelain_warn "summary failed for session $sid — leaving it unsummarized"
      summary="(summary unavailable)"
    fi
    summary_pairs+=("$(jq -cn --arg s "$sid" --arg v "$summary" '{session_id:$s, summary:$v}')")
  done < <(jq -c '.days[].projects[].sessions[]' <<<"$manifest")

  # Fold summaries back into the manifest and drop the raw bodies. The manifest
  # carries --bodies only as summarizer input; neither the JSON nor the render
  # path consumes .body, so strip it here to keep --json condensed and
  # consistent with the plumbing's lean-by-default output (raw bodies remain
  # available via `clast-plumbing --json retro --bodies`). The manifest can
  # exceed MAX_ARG_STRLEN — feed it via stdin and the summary pairs via
  # --slurpfile, never argv.
  local enriched="$manifest"
  if (( ${#summary_pairs[@]} > 0 )); then
    enriched="$(printf '%s' "$manifest" | jq -c --slurpfile pairs <(printf '%s\n' "${summary_pairs[@]}") '
      (reduce $pairs[] as $p ({}; .[$p.session_id] = $p.summary)) as $sum
      | .days |= map(.projects |= map(.sessions |= map(del(.body) + {summary: ($sum[.session_id] // null)})))
    ')"
  fi

  if (( as_json )); then
    printf '%s\n' "$enriched"
    return 0
  fi

  _clast_retrosum_render "$enriched"
}

# _clast_retrosum_render <manifest-with-summaries>
_clast_retrosum_render() {
  local manifest="$1"
  local from to window
  from="$(jq -r '.from // "(start)"' <<<"$manifest")"
  to="$(jq -r '.to // "(end)"' <<<"$manifest")"
  window="$(jq -r '.window' <<<"$manifest")"
  printf 'Retro: %s -> %s (%s)\n' "$from" "$to" "$window"

  local nd
  nd="$(jq '.days | length' <<<"$manifest")"
  if (( nd == 0 )); then
    printf '\n(no sessions in range)\n'
    return 0
  fi

  local di pj si day np project ns sess sid shortsid title summary note flag
  for (( di = 0; di < nd; di++ )); do
    day="$(jq -r ".days[$di].day" <<<"$manifest")"
    printf '\n== %s ==\n' "$day"
    note="$(jq -r --arg d "$day" '.days['"$di"'].curation_dates // [] |
      if . == [] or . == [$d] then empty
      else "  (filed " + (join(", ")) + "; work day reconstructed from session snapshots)"
      end' <<<"$manifest")"
    [[ -n "$note" ]] && printf '%s\n' "$note"
    np="$(jq ".days[$di].projects | length" <<<"$manifest")"
    for (( pj = 0; pj < np; pj++ )); do
      project="$(jq -r ".days[$di].projects[$pj].project_name // .days[$di].projects[$pj].project_path // \"(no project)\"" <<<"$manifest")"
      printf '\n[%s]\n' "$project"
      ns="$(jq ".days[$di].projects[$pj].sessions | length" <<<"$manifest")"
      for (( si = 0; si < ns; si++ )); do
        sess="$(jq -c ".days[$di].projects[$pj].sessions[$si]" <<<"$manifest")"
        sid="$(jq -r '.session_id' <<<"$sess")"
        shortsid="${sid:0:8}"
        title="$(jq -r '.title // "(untitled)"' <<<"$sess")"
        [[ -z "$title" || "$title" == "null" ]] && title="(untitled)"
        summary="$(jq -r '.summary // "(no summary)"' <<<"$sess")"
        flag=""
        [[ "$(jq -r '.interrupted // false' <<<"$sess")" == "true" ]] && flag="  [interrupted]"
        printf '\n  * %s  (%s)%s\n' "$title" "$shortsid" "$flag"
        printf '%s\n' "$summary"
      done
    done
  done
}
