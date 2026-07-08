# clast wake — LLM-powered interactive day curation.
#
# Replicates the /wake plugin skill using an OpenAI-compatible chat
# completions endpoint. Calls clast-plumbing for data, assembles prompts,
# calls the LLM via curl, presents drafts interactively.
#
# Usage: clast wake
# shellcheck shell=bash

# --- Small helpers -----------------------------------------------------------

_clast_wake_slugify() {
  local s="$1"
  s="${s,,}"
  s="$(printf '%s' "$s" | sed 's/[^a-z0-9]/-/g; s/--*/-/g; s/^-//; s/-$//')"
  printf '%s' "${s:0:60}"
}

_clast_wake_separator() {
  local label="$1"
  local width=72
  local pad=$(( width - ${#label} - 4 ))
  (( pad < 2 )) && pad=2
  local dashes=""
  local j
  for (( j = 0; j < pad; j++ )); do dashes+="─"; done
  printf '── %s %s\n' "$label" "$dashes"
}

# --- Usage / preflight -------------------------------------------------------

_clast_wake_usage() {
  cat <<'EOF'
Usage: clast wake [--auto]

Interactive day curation: for each uncurated session, generate a draft journal
entry with the LLM and accept/edit/dismiss/skip it.

Flags:
  --auto      Non-interactive: auto-accept every generated draft and write it.
              Skips the triage menu and the per-session prompt, and does not
              require a tty — suitable for cron/scripts. Sessions whose draft
              fails to generate are skipped. The scan window still honors
              CLAST_WAKE_SINCE (default -14d).
  -h, --help  Print this usage and exit.

Requires the CLAST_LLM_* env vars (see `clast --help`).
EOF
}

# _clast_wake_preflight <auto>
#   In interactive mode (auto=0) a tty is required so we can read choices; --auto
#   reads nothing from the terminal, so that check is skipped.
_clast_wake_preflight() {
  local auto="${1:-0}"
  if (( ! auto )) && [[ ! -t 0 ]]; then
    clast_porcelain_die "clast wake requires an interactive terminal (stdin is not a tty). Use --auto for non-interactive curation."
  fi
  clast_porcelain_preflight_llm
}

# --- User prompt -------------------------------------------------------------

_clast_wake_build_user_prompt() {
  local project="$1" branch="$2" start="$3" end="$4" msg_count="$5"
  local first_turns="$6" last_turns="$7" breadcrumbs="$8"

  local template_file template
  template_file="$(clast_porcelain_user_prompt_file wake-draft-user)"

  if [[ -n "$template_file" ]]; then
    template="$(cat "$template_file")"
    template="${template//\{\{project\}\}/${project}}"
    template="${template//\{\{branch\}\}/${branch:-unknown}}"
    template="${template//\{\{start\}\}/${start}}"
    template="${template//\{\{end\}\}/${end}}"
    template="${template//\{\{msg_count\}\}/${msg_count}}"
    template="${template//\{\{first_turns\}\}/${first_turns}}"
    template="${template//\{\{last_turns\}\}/${last_turns}}"
    template="${template//\{\{breadcrumbs\}\}/${breadcrumbs:-None.}}"
    printf '%s' "$template"
  else
    clast_porcelain_warn "user prompt template not found: wake-draft-user.md — using inline fallback"
    cat <<EOF
Session metadata:
- Project: ${project}
- Branch: ${branch:-unknown}
- Start: ${start}
- End: ${end}
- Approximate messages: ${msg_count}

First turns of the session:
${first_turns}

Last turns of the session:
${last_turns}

Breadcrumbs the user left during this session's day:
${breadcrumbs:-None.}
EOF
  fi
}

# --- Draft parsing -----------------------------------------------------------

_clast_wake_extract_title() {
  local draft="$1"
  grep -m1 '^# Session:' <<<"$draft" | sed 's/^# Session:[[:space:]]*//'
}

_clast_wake_extract_tags() {
  local draft="$1"
  local tags
  tags="$(grep -i '^Suggested tags:' <<<"$draft" | sed 's/^[Ss]uggested tags:[[:space:]]*//')"
  printf '%s' "$tags" | sed 's/[[:space:]]*,[[:space:]]*/,/g'
}

_clast_wake_strip_tags_trailer() {
  local draft="$1"
  printf '%s' "$draft" | sed '/^$/{ N; /\n[Ss]uggested tags:/d; }' | sed '/^[Ss]uggested tags:/d'
}

# --- Session slug ------------------------------------------------------------

_clast_wake_get_session_slug() {
  local snapshot_path="$1" draft_title="$2"
  local journal_dir
  journal_dir="$(clast-plumbing whereami 2>/dev/null | grep '^journal_dir:' | awk '{print $2}')" || true
  if [[ -z "$journal_dir" ]]; then
    journal_dir="${HOME}/.claude/journal"
  fi

  local abs_path="$journal_dir/$snapshot_path"

  if [[ -r "$abs_path" ]]; then
    local ai_title
    ai_title="$(jq -r 'select(.type == "ai-title") | .aiTitle' "$abs_path" 2>/dev/null | tail -1)" || true
    if [[ -n "$ai_title" && "$ai_title" != "null" ]]; then
      _clast_wake_slugify "$ai_title"
      return
    fi
  fi

  if [[ -n "$draft_title" ]]; then
    _clast_wake_slugify "$draft_title"
    return
  fi

  printf 'session'
}

# --- Interactive menu --------------------------------------------------------

_clast_wake_prompt_choice() {
  local choice
  printf '\n  [a] Accept    [e] Edit    [d] Dismiss    [s] Skip    [q] Stop here\n' >/dev/tty
  printf '  Choice: ' >/dev/tty
  read -r -n1 choice </dev/tty
  printf '\n' >/dev/tty
  printf '%s' "$choice"
}

_clast_wake_prompt_edit_feedback() {
  local feedback
  printf '\n  What should change? ' >/dev/tty
  read -r feedback </dev/tty
  printf '%s' "$feedback"
}

# --- Triage ------------------------------------------------------------------

_clast_wake_triage() {
  local uncurated="$1" total="$2" day_count="$3"
  local first_day="$4" last_day="$5" project_count="$6"

  clast_porcelain_info "Found $total uncurated session(s) across $day_count day(s) ($first_day to $last_day)." >/dev/tty
  printf '\n' >/dev/tty

  local breakdown
  breakdown="$(jq -r '
    group_by(.day_bucket) | sort_by(.[0].day_bucket) | .[] |
    "    \(.[0].day_bucket)  \(length) session(s)"
  ' <<<"$uncurated")"
  printf '%s\n' "$breakdown" >/dev/tty
  printf '\n' >/dev/tty

  local choice
  printf '  [a] Process all %s sessions\n' "$total" >/dev/tty
  printf '  [y] Process yesterday only\n' >/dev/tty
  printf '  [n] Choose how many days back\n' >/dev/tty
  printf '  [o] Dismiss everything older, then process the rest\n' >/dev/tty
  printf '  [q] Quit\n' >/dev/tty
  printf '  Choice: ' >/dev/tty
  read -r -n1 choice </dev/tty
  printf '\n' >/dev/tty

  case "$choice" in
    a|A)
      printf '%s' "$uncurated"
      ;;
    y|Y)
      local yesterday
      yesterday="$(date -d 'yesterday' +%Y-%m-%d)"
      jq -c "[.[] | select(.day_bucket == \"$yesterday\")]" <<<"$uncurated"
      ;;
    n|N)
      local days
      printf '  How many days back? ' >/dev/tty
      read -r days </dev/tty
      if ! [[ "$days" =~ ^[1-9][0-9]*$ ]]; then
        clast_porcelain_warn "invalid number — processing all sessions"
        printf '%s' "$uncurated"
        return
      fi
      local cutoff
      cutoff="$(date -d "-${days} days" +%Y-%m-%d 2>/dev/null)" || {
        clast_porcelain_warn "failed to compute date — processing all sessions"
        printf '%s' "$uncurated"
        return
      }
      jq -c "[.[] | select(.day_bucket >= \"$cutoff\")]" <<<"$uncurated"
      ;;
    o|O)
      local days_keep
      printf '  Keep how many days? (dismiss everything older) ' >/dev/tty
      read -r days_keep </dev/tty
      if ! [[ "$days_keep" =~ ^[1-9][0-9]*$ ]]; then
        clast_porcelain_warn "invalid number — processing all sessions"
        printf '%s' "$uncurated"
        return
      fi
      local cutoff_keep
      cutoff_keep="$(date -d "-${days_keep} days" +%Y-%m-%d 2>/dev/null)" || {
        clast_porcelain_warn "failed to compute date — processing all sessions"
        printf '%s' "$uncurated"
        return
      }
      local old_ids
      old_ids="$(jq -r "[.[] | select(.day_bucket < \"$cutoff_keep\")] | .[].session_id" <<<"$uncurated")"
      local dismiss_count=0
      local old_id
      while IFS= read -r old_id; do
        [[ -z "$old_id" ]] && continue
        clast-plumbing sessions dismiss "$old_id" --reason "bulk dismiss via clast wake triage" 2>/dev/null
        dismiss_count=$(( dismiss_count + 1 ))
      done <<<"$old_ids"
      if (( dismiss_count > 0 )); then
        clast_porcelain_info "  Dismissed $dismiss_count older session(s)." >/dev/tty
      fi
      jq -c "[.[] | select(.day_bucket >= \"$cutoff_keep\")]" <<<"$uncurated"
      ;;
    q|Q)
      printf '[]'
      ;;
    *)
      clast_porcelain_warn "unknown choice — processing all sessions"
      printf '%s' "$uncurated"
      ;;
  esac
}

# --- Main --------------------------------------------------------------------

clast_cmd_wake() {
  local auto=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --auto)     auto=1; shift ;;
      -h|--help)  _clast_wake_usage; return 0 ;;
      --) shift; break ;;
      *) clast_porcelain_log_error "wake: unknown argument '$1'"; return 2 ;;
    esac
  done

  _clast_wake_preflight "$auto"

  (( auto )) && clast_porcelain_info "Auto mode: drafts will be accepted without review."

  clast_porcelain_info "Snapshotting fresh transcripts..."
  if ! clast-plumbing snapshot 2>/dev/null; then
    clast_porcelain_warn "clast-plumbing snapshot failed — proceeding with existing data"
  fi

  clast_porcelain_info "Checking for uncurated or stale sessions..."
  # Window is configurable; a shorter default keeps the scan fast on large
  # journals (clast sessions cost scales with sessions in the window).
  local since="${CLAST_WAKE_SINCE:--14d}"
  local sessions_json
  sessions_json="$(clast-plumbing --json sessions --since "$since" 2>/dev/null)" || {
    clast_porcelain_die "failed to list sessions"
  }

  # Auto-dismiss no-op sessions before any LLM work: sessions where Claude
  # never replied (substantive == false) — empty sessions, slash-command-only
  # sessions (/clear, /model, /config), and sessions abandoned before any
  # response. This is a deterministic pre-filter — the LLM is never called for
  # them. Sessions driven by a custom slash command still have assistant
  # replies, so they are kept. Reversible via `clast undismiss <id>`. Opt out
  # by setting CLAST_WAKE_AUTODISMISS_NOOP=0.
  local auto_dismissed_count=0
  if [[ "${CLAST_WAKE_AUTODISMISS_NOOP:-1}" != "0" ]]; then
    local noop_ids noop_id
    noop_ids="$(jq -r '
      .[] | select(.substantive == false and .curated == false and .dismissed == false)
      | .session_id
    ' <<<"$sessions_json")"
    while IFS= read -r noop_id; do
      [[ -z "$noop_id" ]] && continue
      if clast-plumbing sessions dismiss "$noop_id" \
        --reason "auto: no substantive content (empty / slash-command-only)" >/dev/null 2>&1; then
        auto_dismissed_count=$(( auto_dismissed_count + 1 ))
      fi
    done <<<"$noop_ids"
    if (( auto_dismissed_count > 0 )); then
      clast_porcelain_info "Auto-dismissed $auto_dismissed_count no-op session(s) (empty / slash-command-only)."
      # Drop the just-dismissed rows from the working set so they don't reappear.
      sessions_json="$(jq -c '[.[] | select(.substantive != false or .curated == true)]' <<<"$sessions_json")"
    fi
  fi

  local uncurated
  uncurated="$(jq -c '[.[] | select(.curated == false or .stale == true)]' <<<"$sessions_json")"
  local total
  total="$(jq 'length' <<<"$uncurated")"

  if (( total == 0 )); then
    clast_porcelain_info "Nothing to curate — all sessions are curated or dismissed."
    return 0
  fi

  local day_count first_day last_day project_count
  day_count="$(jq '[.[].day_bucket] | unique | length' <<<"$uncurated")"
  first_day="$(jq -r '[.[].day_bucket] | sort | first' <<<"$uncurated")"
  last_day="$(jq -r '[.[].day_bucket] | sort | last' <<<"$uncurated")"
  project_count="$(jq '[.[].project] | unique | length' <<<"$uncurated")"

  # Triage is an interactive scope picker; --auto processes the whole window.
  if (( ! auto )) && (( day_count > 1 )); then
    uncurated="$(_clast_wake_triage "$uncurated" "$total" "$day_count" "$first_day" "$last_day" "$project_count")"
    total="$(jq 'length' <<<"$uncurated")"
    if (( total == 0 )); then
      clast_porcelain_info "Nothing left to curate."
      return 0
    fi
    project_count="$(jq '[.[].project] | unique | length' <<<"$uncurated")"
  fi

  clast_porcelain_info "Processing $total session(s) across $project_count project(s)."
  printf '\n'

  local curated_count=0 skipped_count=0 dismissed_count=0
  local -a curated_projects=()
  local i=0 stop=0
  # Cumulative model time across every draft generation (excludes the time the
  # reviewer spends at the menu), so the run's LLM cost is legible — mirrors the
  # per-call + total timing `clast retro` reports.
  local wake_model_total="0.0"

  while (( i < total && stop == 0 )); do
    local session
    session="$(jq -c ".[$i]" <<<"$uncurated")"

    local sid project branch start_ts end_ts msg_count snapshot_path
    sid="$(jq -r '.session_id' <<<"$session")"
    project="$(jq -r '.project' <<<"$session")"
    branch="$(jq -r '.branch // ""' <<<"$session")"
    start_ts="$(jq -r '.start' <<<"$session")"
    end_ts="$(jq -r '.end' <<<"$session")"
    msg_count="$(jq -r '.msg_count_approx' <<<"$session")"
    snapshot_path="$(jq -r '.snapshot_path' <<<"$session")"

    # Recorded date + time range so the reviewer can tell which day's work
    # this is (BDS-54). Render the session's own start/end instant in the
    # local timezone. Deliberately NOT day_bucket: that is clast's
    # cutoff-adjusted *filing* day, which differs from the instant's calendar
    # date for pre-cutoff sessions — pairing it with a clock time (and a "UTC"
    # label) yielded a timestamp wrong by a day. Fall back to the raw UTC
    # substrings if `date` can't parse the timestamp.
    local rec_date start_short end_short tz
    rec_date="$(date -d "$start_ts" +%Y-%m-%d 2>/dev/null)" || rec_date=""
    [[ -z "$rec_date" ]] && rec_date="${start_ts:0:10}"
    start_short="$(date -d "$start_ts" +%H:%M 2>/dev/null)" || start_short=""
    [[ -z "$start_short" ]] && start_short="${start_ts:11:5}"
    end_short="$(date -d "$end_ts" +%H:%M 2>/dev/null)" || end_short=""
    [[ -z "$end_short" ]] && end_short="${end_ts:11:5}"
    tz="$(date -d "$start_ts" +%Z 2>/dev/null)" || tz="UTC"
    [[ -z "$tz" ]] && tz="UTC"

    local recorded="$rec_date"
    if [[ -n "$start_short" ]]; then
      recorded="$recorded $start_short"
      [[ -n "$end_short" && "$end_short" != "$start_short" ]] && recorded="$recorded–$end_short"
      recorded="$recorded $tz"
    fi

    local is_stale
    is_stale="$(jq -r '.stale // false' <<<"$session")"
    local label="Session $((i+1))/$total: $project"
    [[ "$is_stale" == "true" ]] && label="$label [STALE]"
    [[ -n "$rec_date" ]] && label="$label ($rec_date $start_short"
    [[ -n "$branch" && "$branch" != "null" ]] && label="$label, $branch"
    label="$label)"

    _clast_wake_separator "$label"
    # Full session ID + recorded window: identifies exactly which session is
    # being reviewed (e.g. for `clast-plumbing sessions dismiss/undismiss <id>`).
    clast_porcelain_info "  id: $sid"
    clast_porcelain_info "  recorded: $recorded"
    clast_porcelain_info "Gathering context..."

    local show_json
    show_json="$(clast-plumbing --json show "$sid" --full --turns 8 2>/dev/null)" || {
      local rc=$? reason
      reason="$(jq -r '.error // empty' <<<"$show_json" 2>/dev/null || true)"
      [[ -z "$reason" ]] && reason="exit $rc"
      clast_porcelain_warn "failed to read session $sid ($reason) — skipping"
      skipped_count=$(( skipped_count + 1 ))
      i=$(( i + 1 ))
      continue
    }

    # Cap each turn's text: a single pathological turn (e.g. a huge pasted
    # blob or tool dump) would otherwise bloat the prompt — costly and liable
    # to exceed the model's context. show --full keeps the full text; only the
    # LLM-bound copy is bounded.
    local first_turns last_turns turn_cap=2000
    first_turns="$(jq -r --argjson cap "$turn_cap" '
      .first_turns // [] | .[] |
      (.text // "") as $t | ($t | length) as $n |
      "[\(.role)] \(if $n > $cap then $t[0:$cap] + "… [\($n - $cap) more chars truncated]" else $t end)"
    ' <<<"$show_json" 2>/dev/null)" || true

    last_turns="$(jq -r --argjson cap "$turn_cap" '
      .last_turns // [] | .[] |
      (.text // "") as $t | ($t | length) as $n |
      "[\(.role)] \(if $n > $cap then $t[0:$cap] + "… [\($n - $cap) more chars truncated]" else $t end)"
    ' <<<"$show_json" 2>/dev/null)" || true

    local breadcrumbs=""
    breadcrumbs="$(clast-plumbing breadcrumb --read --project "$project" --day yesterday 2>/dev/null)" || true

    local user_prompt
    user_prompt="$(_clast_wake_build_user_prompt "$project" "$branch" "$start_ts" "$end_ts" "$msg_count" \
      "$first_turns" "$last_turns" "$breadcrumbs")"

    local system_prompt
    system_prompt="$(clast_porcelain_load_system_prompt wake-draft-system)"

    local draft="" edit_extra=""
    local drafting=1

    while (( drafting )); do
      local full_system="$system_prompt"
      local full_user="$user_prompt"
      if [[ -n "$edit_extra" ]]; then
        full_user="${full_user}

Revisions requested by user: ${edit_extra}"
      fi

      clast_porcelain_info "Generating draft..."
      local gen_t0 gen_t1 gen_dt
      gen_t0="$(clast_porcelain_now)"
      if ! draft="$(clast_porcelain_llm_chat "$full_system" "$full_user")"; then
        printf '\n'
        clast_porcelain_warn "LLM call failed for session $sid"
        # No tty to prompt in auto mode — skip this session and move on.
        if (( auto )); then
          skipped_count=$(( skipped_count + 1 )); break
        fi
        local retry
        printf '  [r] Retry    [s] Skip    [q] Stop\n  Choice: '
        read -r -n1 retry </dev/tty
        printf '\n'
        case "$retry" in
          r|R) continue ;;
          q|Q) stop=1; break ;;
          *) skipped_count=$(( skipped_count + 1 )); break ;;
        esac
      fi
      # Only reached when the draft succeeded (every failure branch above
      # continues or breaks), so this times just the model call.
      gen_t1="$(clast_porcelain_now)"
      gen_dt="$(clast_porcelain_elapsed "$gen_t0" "$gen_t1")"
      wake_model_total="$(awk -v a="$wake_model_total" -v d="$gen_dt" 'BEGIN { printf "%.1f", a + d }')"
      clast_porcelain_info "  done in ${gen_dt}s (model total ${wake_model_total}s)"

      local choice
      if (( auto )); then
        # Auto-accept without printing the full draft or prompting — the write
        # result below records what landed. Reuses the `a` case verbatim.
        choice="a"
      else
        printf '\n%s\n' "$draft"
        printf '\n'
        choice="$(_clast_wake_prompt_choice)"
      fi

      case "$choice" in
        a|A)
          local title tags body slug
          title="$(_clast_wake_extract_title "$draft")"
          tags="$(_clast_wake_extract_tags "$draft")"
          body="$(_clast_wake_strip_tags_trailer "$draft")"
          slug="$(_clast_wake_get_session_slug "$snapshot_path" "$title")"

          local write_args=(entries write --session "$sid" --slug "$slug" --body-stdin)
          [[ -n "$tags" ]] && write_args+=(--tags "$tags")
          [[ -n "$title" ]] && write_args+=(--title "$title")

          local write_result
          if write_result="$(printf '%s\n' "$body" | clast-plumbing "${write_args[@]}" 2>&1)"; then
            clast_porcelain_info "  $write_result"
            curated_count=$(( curated_count + 1 ))
            curated_projects+=("$project")
          else
            clast_porcelain_warn "failed to write entry: $write_result"
          fi
          drafting=0
          ;;
        e|E)
          edit_extra="$(_clast_wake_prompt_edit_feedback)"
          ;;
        d|D)
          if clast-plumbing sessions dismiss "$sid" --reason "dismissed via clast wake" 2>/dev/null; then
            clast_porcelain_info "  Dismissed."
            dismissed_count=$(( dismissed_count + 1 ))
          else
            clast_porcelain_warn "failed to dismiss session $sid"
          fi
          drafting=0
          ;;
        s|S)
          skipped_count=$(( skipped_count + 1 ))
          drafting=0
          ;;
        q|Q)
          stop=1
          drafting=0
          ;;
        *)
          clast_porcelain_info "  Unknown choice '$choice' — skipping."
          skipped_count=$(( skipped_count + 1 ))
          drafting=0
          ;;
      esac
    done

    i=$(( i + 1 ))
    printf '\n'
  done

  local remaining=$(( total - curated_count - skipped_count - dismissed_count ))
  local unique_projects
  unique_projects="$(printf '%s\n' "${curated_projects[@]}" 2>/dev/null | sort -u | wc -l | tr -d ' ')" || true
  [[ -z "$unique_projects" || "$unique_projects" == "0" ]] && unique_projects=0

  printf '\n'
  _clast_wake_separator "Summary"
  clast_porcelain_info "  Curated: $curated_count session(s) across $unique_projects project(s)"
  if (( auto_dismissed_count > 0 )); then
    clast_porcelain_info "  Auto-dismissed (no-op): $auto_dismissed_count session(s)"
  fi
  if (( dismissed_count > 0 )); then
    clast_porcelain_info "  Dismissed: $dismissed_count session(s)"
  fi
  clast_porcelain_info "  Skipped: $skipped_count session(s)"
  if (( remaining > 0 )); then
    clast_porcelain_info "  Remaining: $remaining session(s) (stopped early)"
  fi
  clast_porcelain_info "  Model time: ${wake_model_total}s"
}
