# clast-subcommands/show.bash — `clast show <session-id>`.
#
# Dump metadata + (optionally) first/last turns of a single captured
# session. See docs/cli-contract.md#clast-show.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

_clast_show_usage() {
  cat <<'EOF'
Usage: clast show <session-id> [--full] [--turns N]

Print metadata for a captured session.

Flags:
  --full        Include first/last N turns (text only, no tool calls).
  --turns N     Number of turns at each end when --full is set (default 5).
  -h, --help    Print this usage and exit.
EOF
}

_clast_show_err() {
  local msg="$1" code="${2:-2}"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" --argjson c "$code" '{error:$m, code:$c}'
  else
    clast_log_error "show: $msg"
  fi
}

clast_cmd_show() {
  local session_id=""
  local include_turns=0
  local turn_count=5

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --full) include_turns=1; shift ;;
      --turns)
        if [[ $# -lt 2 ]]; then _clast_show_err "--turns requires a value"; return 2; fi
        if ! [[ "$2" =~ ^[1-9][0-9]*$ ]]; then
          _clast_show_err "--turns must be a positive integer"; return 2
        fi
        turn_count="$2"; shift 2 ;;
      --turns=*)
        local v="${1#*=}"
        if ! [[ "$v" =~ ^[1-9][0-9]*$ ]]; then
          _clast_show_err "--turns must be a positive integer"; return 2
        fi
        turn_count="$v"; shift ;;
      -h|--help) _clast_show_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_show_err "unknown flag '$1'"; return 2 ;;
      *)
        if [[ -n "$session_id" ]]; then
          _clast_show_err "unexpected positional '$1'"; return 2
        fi
        session_id="$1"; shift ;;
    esac
  done

  if [[ -z "$session_id" ]]; then
    _clast_show_err "missing <session-id>"; return 2
  fi
  if ! [[ "$session_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
    _clast_show_err "'$session_id' is not a valid UUID"; return 2
  fi

  local line
  if ! line="$(clast_manifest_lookup "$session_id" 2>/dev/null)" || [[ -z "$line" ]]; then
    _clast_show_err "session '$session_id' not found in manifest" 1
    return 1
  fi

  local snapshot day_bucket source_mtime journal_dir abs_path
  snapshot="$(jq -r '.snapshot' <<<"$line")"
  day_bucket="$(jq -r '.day_bucket' <<<"$line")"
  source_mtime="$(jq -r '.source_mtime' <<<"$line")"
  journal_dir="$(clast_journal_dir)"
  abs_path="$journal_dir/$snapshot"

  if [[ ! -f "$abs_path" ]]; then
    _clast_show_err "snapshot file missing on disk (run 'clast doctor')" 1
    return 1
  fi

  local seg slug
  seg="$(awk -F/ 'NR==1{print $3}' <<<"$snapshot")"
  if slug="$(clast_registry_resolve "$seg" 2>/dev/null)" && [[ -n "$slug" ]]; then
    :
  else
    slug="$seg"
  fi

  local msgs first_ts last_ts start_ts end_ts
  msgs="$(wc -l <"$abs_path" 2>/dev/null | tr -d ' ')"
  [[ -z "$msgs" ]] && msgs=0
  first_ts="$(head -n1 "$abs_path" 2>/dev/null | jq -r '.timestamp // empty' 2>/dev/null || true)"
  last_ts="$(tail -n1 "$abs_path" 2>/dev/null | jq -r '.timestamp // empty' 2>/dev/null || true)"
  start_ts="${first_ts:-$source_mtime}"
  end_ts="${last_ts:-$source_mtime}"

  # first_prompt / last_prompt — best-effort scan of user messages.
  local first_prompt last_prompt
  first_prompt="$(_clast_show_user_messages "$abs_path" | head -n1)"
  last_prompt="$(_clast_show_user_messages "$abs_path" | tail -n1)"
  first_prompt="$(_clast_show_truncate "$first_prompt")"
  last_prompt="$(_clast_show_truncate "$last_prompt")"

  # duration
  local duration_str=""
  if [[ -n "$first_ts" && -n "$last_ts" ]]; then
    local s_epoch e_epoch delta
    if s_epoch="$(date -d "$first_ts" +%s 2>/dev/null)" \
      && e_epoch="$(date -d "$last_ts" +%s 2>/dev/null)" \
      && [[ -n "$s_epoch" && -n "$e_epoch" ]]; then
      delta=$((e_epoch - s_epoch))
      duration_str="$(_clast_show_format_duration "$delta")"
    fi
  fi

  local curated=false
  if [[ -d "$journal_dir/entries" ]]; then
    if grep -l "session_id: $session_id" "$journal_dir/entries"/*.md 2>/dev/null | head -n1 | grep -q .; then
      curated=true
    fi
  fi

  # TODO(step-10): branch field is best-effort; revisit when stats command lands.
  local branch=""

  # --- Build first_turns / last_turns for --full and --json ---------------
  local turns_json='[]' first_turns_json='[]' last_turns_json='[]'
  if (( include_turns == 1 )) || [[ -n "${CLAST_JSON:-}" ]]; then
    turns_json="$(_clast_show_collect_turns "$abs_path")"
    first_turns_json="$(jq -c --argjson n "$turn_count" '.[0:$n]' <<<"$turns_json")"
    last_turns_json="$(jq -c --argjson n "$turn_count" '.[-$n:]' <<<"$turns_json")"
  fi

  if [[ -n "${CLAST_JSON:-}" ]]; then
    local obj
    obj="$(jq -cn \
      --arg session_id "$session_id" \
      --arg project "$slug" \
      --arg segment "$seg" \
      --arg branch "$branch" \
      --arg start "$start_ts" \
      --arg end "$end_ts" \
      --arg duration "$duration_str" \
      --argjson msg_count_approx "$msgs" \
      --arg snapshot_path "$snapshot" \
      --arg day_bucket "$day_bucket" \
      --argjson curated "$curated" \
      --arg first_prompt "$first_prompt" \
      --arg last_prompt "$last_prompt" \
      '{
         session_id: $session_id,
         project: $project,
         segment: $segment,
         branch: (if $branch == "" then null else $branch end),
         start: $start,
         end: $end,
         duration: (if $duration == "" then null else $duration end),
         msg_count_approx: $msg_count_approx,
         snapshot_path: $snapshot_path,
         day_bucket: $day_bucket,
         curated: $curated,
         first_prompt: (if $first_prompt == "" then null else $first_prompt end),
         last_prompt: (if $last_prompt == "" then null else $last_prompt end)
       }')"
    if (( include_turns == 1 )); then
      obj="$(jq -c --argjson f "$first_turns_json" --argjson l "$last_turns_json" \
        '. + {first_turns:$f, last_turns:$l}' <<<"$obj")"
    fi
    printf '%s\n' "$obj"
    return 0
  fi

  if [[ -z "${CLAST_QUIET:-}" ]]; then
    printf 'session_id:       %s\n' "$session_id"
    printf 'project:          %s\n' "$slug"
    printf 'segment:          %s\n' "$seg"
    printf 'branch:           %s\n' "$branch"
    printf 'start:            %s\n' "$(_clast_show_human_ts "$start_ts")"
    printf 'end:              %s\n' "$(_clast_show_human_ts "$end_ts")"
    if [[ -n "$duration_str" ]]; then
      printf 'duration:         %s\n' "$duration_str"
    fi
    printf 'msg_count:        %s (approx)\n' "$msgs"
    printf 'snapshot:         %s\n' "$abs_path"
    if [[ "$curated" == "true" ]]; then
      printf 'curated:          yes\n'
    else
      printf 'curated:          no\n'
    fi
    if [[ -n "$first_prompt" ]]; then
      printf 'first_prompt:     %s\n' "$first_prompt"
    fi
    if [[ -n "$last_prompt" ]]; then
      printf 'last_prompt:      %s\n' "$last_prompt"
    fi

    if (( include_turns == 1 )); then
      printf '\n## First %s turns\n' "$turn_count"
      jq -r '.[] | "[\(.role)] \(.text)"' <<<"$first_turns_json"
      printf '\n## Last %s turns\n' "$turn_count"
      jq -r '.[] | "[\(.role)] \(.text)"' <<<"$last_turns_json"
    fi
  fi
}

# _clast_show_user_messages <abs_path>
#   Stream user-message text content, one per line. Empty lines dropped.
_clast_show_user_messages() {
  local path="$1"
  jq -r '
    select((.role // .message.role) == "user")
    | (.message.content // .content // empty)
    | if type == "array" then map(.text? // "") | join(" ") else . end
    | select(. != null and . != "")
  ' "$path" 2>/dev/null
}

# _clast_show_collect_turns <abs_path>
#   Emit a JSON array of {role, text} for user+assistant text messages.
_clast_show_collect_turns() {
  local path="$1"
  jq -sc '
    map(
      select((.role // .message.role) as $r | $r == "user" or $r == "assistant")
      | {
          role: (.role // .message.role),
          text: ((.message.content // .content // "") | if type == "array" then map(.text? // "") | join(" ") else . end)
        }
      | select(.text != null and .text != "")
    )
  ' "$path" 2>/dev/null || printf '[]'
}

# _clast_show_truncate <text>
#   Truncate to 120 chars + … if longer.
_clast_show_truncate() {
  local t="$1"
  if (( ${#t} > 120 )); then
    printf '%s…' "${t:0:120}"
  else
    printf '%s' "$t"
  fi
}

# _clast_show_human_ts <iso>
#   "2026-05-30T14:30:30Z" → "2026-05-30 14:30:30"
_clast_show_human_ts() {
  local ts="$1"
  if [[ -z "$ts" ]]; then printf ''; return 0; fi
  printf '%s %s' "${ts:0:10}" "${ts:11:8}"
}

# _clast_show_format_duration <seconds>
_clast_show_format_duration() {
  local s="$1"
  if (( s < 0 )); then s=0; fi
  local h=$((s / 3600))
  local m=$(((s % 3600) / 60))
  local sec=$((s % 60))
  if (( h > 0 )); then
    printf '%dh %dm' "$h" "$m"
  elif (( m > 0 )); then
    printf '%dm %ds' "$m" "$sec"
  else
    printf '%ds' "$sec"
  fi
}
