# clast-subcommands/retro.bash — `clast-plumbing retro`.
#
# Round 1 of the retro feature: run the deterministic day→project manifest
# (clast_retro_manifest) and render it from the raw entry bodies, in work-day
# order. No LLM. `--json` emits the manifest verbatim; the human render is the
# deterministic retro document the porcelain `clast retro` will later condense.
# See .wip/initiatives/clast-retro/.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-retro-lib.bash
source "$CLAST_LIB/clast-retro-lib.bash"

_clast_retro_usage() {
  cat <<'EOF'
Usage: clast retro [--from DATE] [--to DATE] [--window work-days|file-dates]

Summarize work grouped by the day it actually happened → project, from the
curated journal entries. Deterministic; no model call.

Flags:
  --from DATE     Start of the window (inclusive). Default: corpus start.
  --to DATE       End of the window (inclusive). Default: corpus end.
  --window WHICH  work-days (default): keep sessions whose work day is in range.
                  file-dates: keep entries whose filename date is in range
                  (pulls earlier work days reachable from those files).
  --bodies        With --json only: add each session's merged entry `body`.
  -h, --help      Print this usage and exit.

DATE accepts ISO (YYYY-MM-DD), `today`, `yesterday`, `last-week`, `-Nd`, `-Nw`.
With --json, emits the raw day→project manifest instead of the rendered report.
EOF
}

_clast_retro_err() {
  local msg="$1" code="${2:-2}"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" --argjson c "$code" '{error:$m, code:$c}'
  else
    clast_log_error "retro: $msg"
  fi
}

clast_cmd_retro() {
  local from="" to="" window="work-days" bodies=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --bodies) bodies=1; shift ;;
      --from)
        if [[ $# -lt 2 ]]; then _clast_retro_err "--from requires a value"; return 2; fi
        if ! from="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_retro_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --from=*)
        if ! from="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_retro_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --to)
        if [[ $# -lt 2 ]]; then _clast_retro_err "--to requires a value"; return 2; fi
        if ! to="$(clast_parse_date "$2" 2>/dev/null)"; then
          _clast_retro_err "invalid date '$2'"; return 2
        fi
        shift 2 ;;
      --to=*)
        if ! to="$(clast_parse_date "${1#*=}" 2>/dev/null)"; then
          _clast_retro_err "invalid date '${1#*=}'"; return 2
        fi
        shift ;;
      --window)
        if [[ $# -lt 2 ]]; then _clast_retro_err "--window requires a value"; return 2; fi
        window="$2"; shift 2 ;;
      --window=*) window="${1#*=}"; shift ;;
      -h|--help) _clast_retro_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_retro_err "unknown flag '$1'"; return 2 ;;
      *)  _clast_retro_err "unexpected positional '$1'"; return 2 ;;
    esac
  done

  case "$window" in
    work-days|file-dates) ;;
    *) _clast_retro_err "--window must be 'work-days' or 'file-dates'"; return 2 ;;
  esac

  if [[ -n "$from" && -n "$to" && "$from" > "$to" ]]; then
    _clast_retro_err "--from must be <= --to"; return 2
  fi

  local manifest
  manifest="$(clast_retro_manifest \
    ${from:+--from "$from"} ${to:+--to "$to"} --window "$window")" || return $?

  if [[ -n "${CLAST_JSON:-}" ]]; then
    if (( bodies )); then
      manifest="$(_clast_retro_inject_bodies "$manifest")"
    fi
    printf '%s\n' "$manifest"
    return 0
  fi
  if (( bodies )); then
    _clast_retro_err "--bodies is only meaningful with --json"; return 2
  fi
  if [[ -n "${CLAST_QUIET:-}" ]]; then
    return 0
  fi

  _clast_retro_render "$manifest"
}

# _clast_retro_inject_bodies <manifest-json>
#   Add a `body` string (the merged trimmed entry bodies) to every session,
#   for `--json --bodies`. Reuses clast_retro_session_body so the body matches
#   the human render byte-for-byte.
_clast_retro_inject_bodies() {
  local manifest="$1"
  local -a pairs=()
  local sess sid body entry0 title
  while IFS= read -r sess; do
    [[ -z "$sess" ]] && continue
    sid="$(jq -r '.session_id' <<<"$sess")"
    body="$(clast_retro_session_body "$sess")"
    entry0="$(jq -r '.entries[0]' <<<"$sess")"
    title=""
    [[ -r "$entry0" ]] && title="$(clast_entry_title "$entry0")"
    pairs+=("$(jq -cn --arg s "$sid" --arg b "$body" --arg t "$title" \
      '{session_id:$s, body:$b, title:$t}')")
  done < <(jq -c '.days[].projects[].sessions[]' <<<"$manifest")

  if (( ${#pairs[@]} == 0 )); then
    printf '%s' "$manifest"
    return 0
  fi

  printf '%s\n' "${pairs[@]}" | jq -cs --argjson m "$manifest" '
    (reduce .[] as $p ({}; .[$p.session_id] = $p)) as $x
    | $m
    | .days |= map(.projects |= map(.sessions |= map(
        . + {body:  ($x[.session_id].body // null),
             title: ($x[.session_id].title // null)} )))
  '
}

# _clast_retro_render <manifest-json>
#   Render the day→project report to stdout.
_clast_retro_render() {
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

  local di pj si
  local day np project ns sess sid shortsid title entry
  for (( di = 0; di < nd; di++ )); do
    day="$(jq -r ".days[$di].day" <<<"$manifest")"
    printf '\n== %s ==\n' "$day"
    np="$(jq ".days[$di].projects | length" <<<"$manifest")"
    for (( pj = 0; pj < np; pj++ )); do
      project="$(jq -r ".days[$di].projects[$pj].project_path // \"(no project)\"" <<<"$manifest")"
      printf '\n[%s]\n' "$project"
      ns="$(jq ".days[$di].projects[$pj].sessions | length" <<<"$manifest")"
      for (( si = 0; si < ns; si++ )); do
        sess="$(jq -c ".days[$di].projects[$pj].sessions[$si]" <<<"$manifest")"
        sid="$(jq -r '.session_id' <<<"$sess")"
        shortsid="${sid:0:8}"
        entry="$(jq -r '.entries[0]' <<<"$sess")"
        title=""
        [[ -r "$entry" ]] && title="$(clast_entry_title "$entry")"
        [[ -z "$title" ]] && title="(untitled)"
        printf '\n  * %s  (%s)\n' "$title" "$shortsid"
        clast_retro_session_body "$sess"
      done
    done
  done
}
