# clast-subcommands/doctor.bash — `clast doctor`.
#
# Run six sanity checks against the journal and report findings. With
# `--fix`, perform the two safe repairs (manifest rebuild from disk,
# orphan-snapshot removal). See docs/cli-contract.md#clast-doctor.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-manifest-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

_clast_doctor_usage() {
  cat <<'EOF'
Usage: clast doctor [--fix] [--yes]

Sanity-check the journal: manifest validity, registry validity, orphan
snapshots, missing snapshots, day-bucket consistency, day-cutoff sanity.

Flags:
  --fix        Apply safe repairs (rebuild manifest, remove orphans).
  --yes, -y    Skip the interactive confirmation prompt for orphan removal.
  -h, --help   Print this usage and exit.

Exit codes: 0 ok, 1 warnings, 2 usage error, 4 critical corruption.
EOF
}

_clast_doctor_err() {
  local msg="$1" code="${2:-2}"
  if [[ -n "${CLAST_JSON:-}" ]]; then
    jq -cn --arg m "$msg" --argjson c "$code" '{error:$m, code:$c}'
  else
    clast_log_error "doctor: $msg"
  fi
}

# Shared accumulator. Reset at the top of every check pass.
_CLAST_DOCTOR_FINDINGS=()

# _clast_doctor_emit <check> <severity> <message> [items...]
_clast_doctor_emit() {
  local check="$1" severity="$2" message="$3"
  shift 3
  local items_json='[]'
  if (( $# > 0 )); then
    items_json="$(printf '%s\n' "$@" | jq -R . | jq -cs .)"
  fi
  local finding
  finding="$(jq -cn \
    --arg check "$check" \
    --arg severity "$severity" \
    --arg message "$message" \
    --argjson items "$items_json" \
    '{check:$check, severity:$severity, message:$message, items:$items}')"
  _CLAST_DOCTOR_FINDINGS+=("$finding")
}

# ---------------------------------------------------------------------------
# Checks
# ---------------------------------------------------------------------------

# 9a — manifest_validity
_clast_doctor_check_manifest_validity() {
  local path
  path="$(clast_manifest_path)"
  if [[ ! -f "$path" ]]; then
    _clast_doctor_emit "manifest_validity" "ok" "no manifest yet (0 entries)"
    return 0
  fi

  local lineno=0 total=0 valid=0
  local -a bad_lines=()
  local critical=0
  local line parsed ok
  while IFS= read -r line || [[ -n "$line" ]]; do
    lineno=$((lineno + 1))
    [[ -z "$line" ]] && continue
    total=$((total + 1))
    if ! parsed="$(jq -c . <<<"$line" 2>/dev/null)" || [[ -z "$parsed" ]]; then
      bad_lines+=("line $lineno: unparseable JSON")
      critical=1
      continue
    fi
    ok="$(jq -r '
      (has("session_id") and has("source") and has("snapshot")
       and has("captured_at") and has("source_mtime")
       and has("source_size") and has("day_bucket"))
    ' <<<"$parsed" 2>/dev/null)"
    if [[ "$ok" == "true" ]]; then
      valid=$((valid + 1))
    else
      bad_lines+=("line $lineno: missing required field(s)")
    fi
  done <"$path"

  if (( critical == 1 )); then
    _clast_doctor_emit "manifest_validity" "critical" \
      "$total line(s), unparseable line(s) detected" "${bad_lines[@]}"
    return 0
  fi
  if (( ${#bad_lines[@]} > 0 )); then
    _clast_doctor_emit "manifest_validity" "warn" \
      "$total line(s), $valid valid, ${#bad_lines[@]} with missing fields" \
      "${bad_lines[@]}"
    return 0
  fi
  _clast_doctor_emit "manifest_validity" "ok" \
    "$total entries, all valid"
}

# 9b — registry_validity
_clast_doctor_check_registry_validity() {
  local path
  path="$(clast_registry_path)"
  if [[ ! -f "$path" ]]; then
    _clast_doctor_emit "registry_validity" "ok" "no registry yet (0 entries)"
    return 0
  fi

  local lineno=0 total=0
  local -a entries=()
  local -a issues=()
  local critical=0
  local line parsed ok
  while IFS= read -r line || [[ -n "$line" ]]; do
    lineno=$((lineno + 1))
    [[ -z "$line" ]] && continue
    total=$((total + 1))
    if ! parsed="$(jq -c . <<<"$line" 2>/dev/null)" || [[ -z "$parsed" ]]; then
      issues+=("line $lineno: unparseable JSON")
      critical=1
      continue
    fi
    ok="$(jq -r '
      (has("path") and has("slug") and has("first_seen") and has("aliases"))
    ' <<<"$parsed" 2>/dev/null)"
    if [[ "$ok" != "true" ]]; then
      issues+=("line $lineno: missing required field(s)")
      continue
    fi
    entries+=("$parsed")
  done <"$path"

  if (( critical == 1 )); then
    _clast_doctor_emit "registry_validity" "critical" \
      "$total line(s), unparseable line(s) detected" "${issues[@]}"
    return 0
  fi

  # Duplicate slug + alias collisions, computed across the parseable subset.
  local entries_json='[]'
  if (( ${#entries[@]} > 0 )); then
    entries_json="$(printf '%s\n' "${entries[@]}" | jq -cs .)"
  fi

  local dup_lines
  dup_lines="$(jq -r '
    [.[] | .slug] | group_by(.)
    | map(select(length > 1) | .[0]) | .[]
  ' <<<"$entries_json")"
  local dup_slug
  while IFS= read -r dup_slug; do
    [[ -z "$dup_slug" ]] && continue
    issues+=("duplicate slug: $dup_slug")
  done <<<"$dup_lines"

  local collisions
  collisions="$(jq -r '
    . as $arr
    | [
        # alias collides with another entry'"'"'s slug
        ( range(0; length) as $i
          | range(0; length) as $j
          | select($i != $j)
          | ($arr[$i].aliases // []) as $aliases
          | select($aliases | index($arr[$j].slug) != null)
          | "alias collision: " + $arr[$i].slug + " aliases slug " + $arr[$j].slug
        ),
        # alias collides with another entry'"'"'s alias (shared alias across two slugs)
        ( range(0; length) as $i
          | range(0; length) as $j
          | select($i < $j)
          | ($arr[$i].aliases // []) as $ai
          | ($arr[$j].aliases // []) as $aj
          | ($ai | map(select(. as $x | $aj | index($x) != null))) as $shared
          | select($shared | length > 0)
          | "alias collision: " + $arr[$i].slug + " and " + $arr[$j].slug + " share alias " + ($shared[0])
        )
      ]
    | unique[]?
  ' <<<"$entries_json")"
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    issues+=("$line")
  done <<<"$collisions"

  local valid_count="${#entries[@]}"
  if (( ${#issues[@]} > 0 )); then
    _clast_doctor_emit "registry_validity" "warn" \
      "$total line(s), $valid_count valid, ${#issues[@]} issue(s)" \
      "${issues[@]}"
    return 0
  fi
  _clast_doctor_emit "registry_validity" "ok" \
    "$valid_count projects, no duplicates"
}

# Compute the deduped (most-recent per session_id) manifest rows.
# Writes JSON array to stdout; empty manifest → '[]'.
_clast_doctor_dedup_manifest() {
  local path
  path="$(clast_manifest_path)"
  if [[ ! -f "$path" ]]; then
    printf '[]\n'
    return 0
  fi
  jq -cR 'fromjson?' "$path" \
    | jq -cs 'group_by(.session_id) | map(max_by(.captured_at))'
}

# 9c — orphan_snapshots
_clast_doctor_check_orphan_snapshots() {
  local journal_dir transcripts
  journal_dir="$(clast_journal_dir)"
  transcripts="$journal_dir/transcripts"
  if [[ ! -d "$transcripts" ]]; then
    _clast_doctor_emit "orphan_snapshots" "ok" "none (no transcripts directory)"
    return 0
  fi

  # Collect every known session_id from the manifest (full set, not deduped —
  # orphan check only cares whether the sid appears anywhere). Tolerate a
  # missing manifest file: a fresh-or-partially-synced journal with stray
  # transcripts on disk should still report orphans, not abort.
  local manifest_path known='[]'
  manifest_path="$(clast_manifest_path)"
  if [[ -f "$manifest_path" ]]; then
    known="$(jq -cR 'fromjson? | .session_id' "$manifest_path" 2>/dev/null \
      | jq -Rs 'split("\n") | map(select(length > 0) | fromjson? // .)
                | map(select(type == "string")) | unique')"
    [[ -z "$known" ]] && known='[]'
  fi

  local -a orphans=()
  local snapshot sid rel found
  while IFS= read -r snapshot; do
    [[ -z "$snapshot" ]] && continue
    sid="$(basename "$snapshot" .jsonl)"
    found="$(jq -r --arg s "$sid" 'index($s) // empty' <<<"$known")"
    if [[ -z "$found" ]]; then
      rel="${snapshot#"$journal_dir/"}"
      orphans+=("$rel")
    fi
  done < <(find "$transcripts" -mindepth 3 -maxdepth 3 -type f -name '*.jsonl' 2>/dev/null | sort)

  if (( ${#orphans[@]} > 0 )); then
    _clast_doctor_emit "orphan_snapshots" "warn" \
      "${#orphans[@]}" "${orphans[@]}"
    return 0
  fi
  _clast_doctor_emit "orphan_snapshots" "ok" "none"
}

# 9d — missing_snapshots
_clast_doctor_check_missing_snapshots() {
  local deduped journal_dir
  deduped="$(_clast_doctor_dedup_manifest)"
  journal_dir="$(clast_journal_dir)"

  local n
  n="$(jq 'length' <<<"$deduped")"

  local -a missing=()
  local i snapshot abs
  for (( i = 0; i < n; i++ )); do
    snapshot="$(jq -r ".[$i].snapshot // empty" <<<"$deduped")"
    [[ -z "$snapshot" ]] && continue
    abs="$journal_dir/$snapshot"
    if [[ ! -e "$abs" ]]; then
      missing+=("$snapshot")
    fi
  done

  if (( ${#missing[@]} > 0 )); then
    _clast_doctor_emit "missing_snapshots" "warn" \
      "${#missing[@]}" "${missing[@]}"
    return 0
  fi
  _clast_doctor_emit "missing_snapshots" "ok" "none"
}

# 9e — day_bucket_consistency
_clast_doctor_check_day_bucket_consistency() {
  local deduped
  deduped="$(_clast_doctor_dedup_manifest)"
  local n
  n="$(jq 'length' <<<"$deduped")"

  local -a mismatches=()
  local i snapshot day_bucket sday
  for (( i = 0; i < n; i++ )); do
    snapshot="$(jq -r ".[$i].snapshot // empty" <<<"$deduped")"
    day_bucket="$(jq -r ".[$i].day_bucket // empty" <<<"$deduped")"
    [[ -z "$snapshot" || -z "$day_bucket" ]] && continue
    case "$snapshot" in
      transcripts/*) ;;
      *) continue ;;
    esac
    sday="$(awk -F/ 'NR==1{print $2}' <<<"$snapshot")"
    if [[ -n "$sday" && "$sday" != "$day_bucket" ]]; then
      mismatches+=("$snapshot vs day_bucket=$day_bucket")
    fi
  done

  if (( ${#mismatches[@]} > 0 )); then
    _clast_doctor_emit "day_bucket_consistency" "warn" \
      "${#mismatches[@]} mismatch(es)" "${mismatches[@]}"
    return 0
  fi
  _clast_doctor_emit "day_bucket_consistency" "ok" "ok"
}

# 9f — day_cutoff_sanity
_clast_doctor_check_day_cutoff_sanity() {
  local path
  path="$(clast_manifest_path)"
  if [[ ! -f "$path" ]]; then
    _clast_doctor_emit "day_cutoff_sanity" "ok" "ok (empty manifest)"
    return 0
  fi

  local cutoff="${CLAST_DAY_CUTOFF:-04:00}"
  local ch cm cutoff_secs
  ch="${cutoff%%:*}"
  cm="${cutoff##*:}"
  ch=$((10#$ch))
  cm=$((10#$cm))
  cutoff_secs=$((ch * 3600 + cm * 60))

  local total=0 near=0
  local ts secs diff a b
  while IFS= read -r ts; do
    [[ -z "$ts" ]] && continue
    if ! secs="$(date -d "$ts" +%H:%M:%S 2>/dev/null)" || [[ -z "$secs" ]]; then
      continue
    fi
    local hh mm ss
    hh="${secs%%:*}"; secs="${secs#*:}"
    mm="${secs%%:*}"; ss="${secs#*:}"
    hh=$((10#$hh)); mm=$((10#$mm)); ss=$((10#$ss))
    local cap_secs=$((hh * 3600 + mm * 60 + ss))
    a=$((cap_secs - cutoff_secs))
    if (( a < 0 )); then a=$((-a)); fi
    b=$((86400 - a))
    diff=$a
    if (( b < diff )); then diff=$b; fi
    total=$((total + 1))
    if (( diff <= 1800 )); then
      near=$((near + 1))
    fi
  done < <(jq -rR 'fromjson? | .captured_at // empty' "$path")

  if (( total == 0 )); then
    _clast_doctor_emit "day_cutoff_sanity" "ok" "ok (empty manifest)"
    return 0
  fi
  # >5% within ±30min of cutoff hour → warn.
  if (( near * 20 > total )); then
    local pct=$(( near * 100 / total ))
    _clast_doctor_emit "day_cutoff_sanity" "warn" \
      "$near/$total ($pct%) captures within ±30min of $cutoff; consider tuning ~/.config/clast/config.toml day_cutoff"
    return 0
  fi
  _clast_doctor_emit "day_cutoff_sanity" "ok" "ok"
}

# Run all six checks (resets the accumulator).
_clast_doctor_run_all() {
  _CLAST_DOCTOR_FINDINGS=()
  _clast_doctor_check_manifest_validity
  _clast_doctor_check_registry_validity
  _clast_doctor_check_orphan_snapshots
  _clast_doctor_check_missing_snapshots
  _clast_doctor_check_day_bucket_consistency
  _clast_doctor_check_day_cutoff_sanity
}

# Aggregate the current findings into a severity bucket: critical / warn / ok.
_clast_doctor_overall_severity() {
  local f sev has_critical=0 has_warn=0
  for f in "${_CLAST_DOCTOR_FINDINGS[@]+"${_CLAST_DOCTOR_FINDINGS[@]}"}"; do
    sev="$(jq -r '.severity' <<<"$f")"
    case "$sev" in
      critical) has_critical=1 ;;
      warn)     has_warn=1 ;;
    esac
  done
  if (( has_critical == 1 )); then printf 'critical\n'
  elif (( has_warn == 1 )); then printf 'warn\n'
  else printf 'ok\n'
  fi
}

# Look up a finding by check id; print its JSON or empty.
_clast_doctor_finding_for() {
  local check="$1" f
  for f in "${_CLAST_DOCTOR_FINDINGS[@]+"${_CLAST_DOCTOR_FINDINGS[@]}"}"; do
    if [[ "$(jq -r '.check' <<<"$f")" == "$check" ]]; then
      printf '%s\n' "$f"
      return 0
    fi
  done
  return 1
}

# ---------------------------------------------------------------------------
# Entry
# ---------------------------------------------------------------------------

clast_cmd_doctor() {
  local fix_mode=0 assume_yes=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --fix)      fix_mode=1; shift ;;
      --yes|-y)   assume_yes=1; shift ;;
      -h|--help)  _clast_doctor_usage; return 0 ;;
      --) shift; break ;;
      -*) _clast_doctor_err "unknown flag '$1'"; return 2 ;;
      *)  _clast_doctor_err "unexpected positional '$1'"; return 2 ;;
    esac
  done

  local -a fixed=()
  _clast_doctor_run_all
  local overall
  overall="$(_clast_doctor_overall_severity)"

  if (( fix_mode == 1 )); then
    # Manifest critical → one rebuild attempt, then re-run all checks.
    if [[ "$overall" == "critical" ]]; then
      local mf
      mf="$(_clast_doctor_finding_for manifest_validity || true)"
      if [[ -n "$mf" && "$(jq -r '.severity' <<<"$mf")" == "critical" ]]; then
        if clast_manifest_rebuild_from_disk; then
          fixed+=("rebuilt manifest from disk")
          _clast_doctor_run_all
          overall="$(_clast_doctor_overall_severity)"
        fi
      fi
    fi

    # Orphan removal — gather post-rebuild orphan list, if any. Skip when a
    # non-manifest critical finding remains (e.g. unparseable projects.json):
    # destructive cleanup should not run while unresolved corruption may
    # affect the analysis.
    local of orphan_items orphan_count=0
    if [[ "$overall" != "critical" ]]; then
      of="$(_clast_doctor_finding_for orphan_snapshots || true)"
      if [[ -n "$of" && "$(jq -r '.severity' <<<"$of")" == "warn" ]]; then
        orphan_items="$(jq -r '.items[]?' <<<"$of")"
        orphan_count="$(jq '.items | length' <<<"$of")"
      fi
    fi
    if (( orphan_count > 0 )); then
      local proceed=0
      if (( assume_yes == 1 )); then
        proceed=1
      else
        # Interactive prompts would corrupt --json output. Force --yes when
        # JSON mode is requested.
        if [[ -n "${CLAST_JSON:-}" ]]; then
          _clast_doctor_err "--fix needs --yes when --json is set"
          return 2
        fi
        if [[ ! -t 0 ]] || ! { exec 3</dev/tty; } 2>/dev/null; then
          _clast_doctor_err "--fix needs --yes when stdin is not a TTY"
          return 2
        fi
        printf 'Found %d orphan snapshot(s):\n' "$orphan_count"
        while IFS= read -r _orphan_item; do
          [[ -z "$_orphan_item" ]] && continue
          printf '  %s\n' "$_orphan_item"
        done <<<"$orphan_items"
        printf 'Remove these %d file(s)? [y/N] ' "$orphan_count"
        local ans=""
        IFS= read -r ans <&3 || true
        exec 3<&-
        case "$ans" in y|Y) proceed=1 ;; esac
      fi
      if (( proceed == 1 )); then
        local journal_dir rel abs removed=0
        journal_dir="$(clast_journal_dir)"
        while IFS= read -r rel; do
          [[ -z "$rel" ]] && continue
          abs="$journal_dir/$rel"
          if rm -f "$abs" 2>/dev/null; then
            removed=$((removed + 1))
          fi
        done <<<"$orphan_items"
        fixed+=("removed $removed orphan snapshot(s)")
        clast_log_info "removed $removed orphan snapshot(s)"
        _clast_doctor_run_all
        overall="$(_clast_doctor_overall_severity)"
      fi
    fi
  fi

  # Compute exit code from final overall severity.
  local exit_code
  case "$overall" in
    critical) exit_code=4 ;;
    warn)     exit_code=1 ;;
    *)        exit_code=0 ;;
  esac

  # Determine if there are any auto-fixable findings still pending (for hint).
  local hint_fixable=0
  local of2
  of2="$(_clast_doctor_finding_for orphan_snapshots || true)"
  if [[ -n "$of2" && "$(jq -r '.severity' <<<"$of2")" == "warn" ]]; then
    hint_fixable=1
  fi
  local mf2
  mf2="$(_clast_doctor_finding_for manifest_validity || true)"
  if [[ -n "$mf2" && "$(jq -r '.severity' <<<"$mf2")" == "critical" ]]; then
    hint_fixable=1
  fi

  if [[ -n "${CLAST_JSON:-}" ]]; then
    local findings_json fixed_json
    if (( ${#_CLAST_DOCTOR_FINDINGS[@]} > 0 )); then
      findings_json="$(printf '%s\n' "${_CLAST_DOCTOR_FINDINGS[@]}" | jq -cs .)"
    else
      findings_json='[]'
    fi
    if (( ${#fixed[@]} > 0 )); then
      fixed_json="$(printf '%s\n' "${fixed[@]}" | jq -R . | jq -cs .)"
    else
      fixed_json='[]'
    fi
    # Re-order findings to canonical sequence.
    findings_json="$(jq -c '
      def by_check(c): map(select(.check == c)) | .[0];
      [
        by_check("manifest_validity"),
        by_check("registry_validity"),
        by_check("orphan_snapshots"),
        by_check("missing_snapshots"),
        by_check("day_bucket_consistency"),
        by_check("day_cutoff_sanity")
      ] | map(select(. != null))
    ' <<<"$findings_json")"
    jq -cn \
      --argjson findings "$findings_json" \
      --argjson exit_code "$exit_code" \
      --argjson fixed "$fixed_json" \
      '{findings:$findings, exit_code:$exit_code, fixed:$fixed}'
    return "$exit_code"
  fi

  if [[ -z "${CLAST_QUIET:-}" ]]; then
    local order=(manifest_validity registry_validity orphan_snapshots \
                 missing_snapshots day_bucket_consistency day_cutoff_sanity)
    local label sev msg prefix items item check
    for check in "${order[@]}"; do
      local f
      f="$(_clast_doctor_finding_for "$check" || true)"
      [[ -z "$f" ]] && continue
      sev="$(jq -r '.severity' <<<"$f")"
      msg="$(jq -r '.message' <<<"$f")"
      case "$sev" in
        ok)       prefix='✓' ;;
        warn)     prefix='!' ;;
        critical) prefix='✗' ;;
        *)        prefix='?' ;;
      esac
      case "$check" in
        manifest_validity)        label="Manifest" ;;
        registry_validity)        label="Registry" ;;
        orphan_snapshots)         label="Orphan snapshots" ;;
        missing_snapshots)        label="Missing snapshots" ;;
        day_bucket_consistency)   label="Day-bucket consistency" ;;
        day_cutoff_sanity)        label="Day-cutoff sanity" ;;
      esac
      printf '%s %s: %s\n' "$prefix" "$label" "$msg"
      if [[ "$sev" == "warn" || "$sev" == "critical" ]]; then
        items="$(jq -r '.items[]?' <<<"$f")"
        if [[ -n "$items" ]]; then
          while IFS= read -r item; do
            [[ -z "$item" ]] && continue
            printf '  %s\n' "$item"
          done <<<"$items"
        fi
      fi
    done

    if (( ${#fixed[@]} > 0 )); then
      printf '\nFixed: %s\n' "$(IFS='; '; printf '%s' "${fixed[*]}")"
    elif (( hint_fixable == 1 && fix_mode == 0 )); then
      # shellcheck disable=SC2016
      printf '\n%s\n' 'Run `clast doctor --fix` to clean up auto-fixable findings.'
    fi
  fi

  return "$exit_code"
}
