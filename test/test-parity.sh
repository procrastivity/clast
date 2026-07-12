#!/usr/bin/env bash
# test-parity.sh — CLI<->skill parity drift guard, driven off test/parity.tsv.
#
# Function-level: sources the porcelain libs and calls each subcommand's
# usage function directly (test-wake-auto.sh's pattern) rather than
# subprocessing `clast <cmd> --help`.
#
# Assertions implemented here (1-5; 6 lands in a later commit as an
# additional function called from the bottom of this file, matching
# test-clast.sh's own plain-sequential-call style):
#   1. Bidirectional --help<->manifest diff for wake/brief/retro (the
#      subcommands with a usage_fn): every flag/env in --help must be a
#      manifest row (direction A), and every mirrored manifest row for that
#      subcommand must appear in its own --help (direction B).
#   2. Every mirrored flag/env row is mentioned in its skill_md_or_reason
#      SKILL.md file.
#   3. Every cli-only/skill_only/internal row has a non-empty, non-placeholder
#      reason.
#   4. The shared CLAST_WAKE_SINCE default matches between wake.bash and
#      skills/wake/SKILL.md.
#   5. Every bare CLAST_* env var read in lib/clast/**.bash is either an
#      `internal` manifest exemption or documented in
#      docs/reference/config.md; every `internal` exemption row must itself
#      still have a live read site (self-expiring).
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-parity"

# shellcheck source=lib/clast/clast-porcelain-lib.bash
source lib/clast/clast-porcelain-lib.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/wake.bash
source lib/clast/clast-porcelain-subcommands/wake.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/brief.bash
source lib/clast/clast-porcelain-subcommands/brief.bash
# shellcheck source=lib/clast/clast-porcelain-subcommands/retro.bash
source lib/clast/clast-porcelain-subcommands/retro.bash

PARITY_TSV="test/parity.tsv"

# --- Manifest helpers --------------------------------------------------------

# _parity_manifest_has <subcommand> <kind> <key> — true if an exact row exists.
_parity_manifest_has() {
  local sub="$1" kind="$2" key="$3"
  awk -F'\t' -v s="$sub" -v k="$kind" -v key="$key" '
    $0 !~ /^#/ && $1 == s && $2 == k && $3 == key { found = 1 }
    END { exit !found }
  ' "$PARITY_TSV"
}

# --- --help block parsing ----------------------------------------------------
#
# Usage heredocs list one flag/env per line under a "Flags:"/"Env:" header,
# indented by exactly two spaces; wrapped continuation text is indented
# further (14+ spaces). A blank line ends the block.

# _parity_block <help-text> <header> — the raw lines belonging to that block.
_parity_block() {
  local text="$1" header="$2"
  awk -v hdr="$header" '
    $0 == hdr { infound = 1; next }
    infound && $0 == "" { infound = 0 }
    infound { print }
  ' <<<"$text"
}

# _parity_item_specs — reads a block on stdin, prints the first whitespace-run
# -delimited field of each two-space-indented item line (skips continuations).
_parity_item_specs() {
  local line rest
  while IFS= read -r line; do
    [[ "$line" =~ ^\ \ [^\ ] ]] || continue
    rest="${line#  }"
    sed -E 's/ {2,}.*$//' <<<"$rest"
  done
}

# _parity_extract_flags <help-text> — long-flag tokens (e.g. --auto), one per
# line, including --help (callers filter it out).
_parity_extract_flags() {
  local spec tok
  while IFS= read -r spec; do
    for tok in $spec; do
      tok="${tok%,}"
      [[ "$tok" == --* ]] && printf '%s\n' "$tok"
    done
  done < <(_parity_block "$1" "Flags:" | _parity_item_specs)
}

# _parity_extract_envs <help-text> — env-var name tokens, one per line.
_parity_extract_envs() {
  _parity_block "$1" "Env:" | _parity_item_specs
}

# --- Assertion 1: bidirectional --help<->manifest diff ----------------------

_parity_assert_1_subcommand() {
  local sub="$1" help="$2"
  local flags envs

  flags="$(_parity_extract_flags "$help" | grep -Fxv -- '--help' || true)"
  envs="$(_parity_extract_envs "$help")"

  # Direction A: every --help flag/env must be a manifest row.
  local tok
  while IFS= read -r tok; do
    [[ -z "$tok" ]] && continue
    if _parity_manifest_has "$sub" flag "$tok"; then
      _clast_test_pass "assertion1 A: $sub $tok is in the manifest"
    else
      _clast_test_fail "assertion1 A: $sub $tok is in the manifest"
      printf '       ERROR: %s --help documents flag %s with no test/parity.tsv row\n' "$sub" "$tok" >&2
    fi
  done <<<"$flags"

  while IFS= read -r tok; do
    [[ -z "$tok" ]] && continue
    if _parity_manifest_has "$sub" env "$tok"; then
      _clast_test_pass "assertion1 A: $sub $tok is in the manifest"
    else
      _clast_test_fail "assertion1 A: $sub $tok is in the manifest"
      printf '       ERROR: %s --help documents env %s with no test/parity.tsv row\n' "$sub" "$tok" >&2
    fi
  done <<<"$envs"

  # Direction B: every mirrored manifest row for this subcommand must be in
  # its own --help output.
  local kind key
  while IFS=$'\t' read -r m_sub kind key _; do
    [[ "$m_sub" == "$sub" ]] || continue
    case "$kind" in
      flag)
        if grep -Fxq -- "$key" <<<"$flags"; then
          _clast_test_pass "assertion1 B: $sub $key ($kind) is in --help"
        else
          _clast_test_fail "assertion1 B: $sub $key ($kind) is in --help"
          printf '       ERROR: test/parity.tsv lists %s flag %s but %s --help does not document it\n' "$sub" "$key" "$sub" >&2
        fi
        ;;
      env)
        if grep -Fxq -- "$key" <<<"$envs"; then
          _clast_test_pass "assertion1 B: $sub $key ($kind) is in --help"
        else
          _clast_test_fail "assertion1 B: $sub $key ($kind) is in --help"
          printf '       ERROR: test/parity.tsv lists %s env %s but %s --help does not document it\n' "$sub" "$key" "$sub" >&2
        fi
        ;;
    esac
  done < <(grep -v '^#' "$PARITY_TSV")
}

assert_parity_1_help_vs_manifest() {
  _parity_assert_1_subcommand wake "$(_clast_wake_usage)"
  _parity_assert_1_subcommand brief "$(_clast_brief_usage)"
  _parity_assert_1_subcommand retro "$(_clast_retrosum_usage)"
}

# --- Assertion 2: mirrored flag/env mentioned in SKILL.md --------------------

assert_parity_2_mentioned_in_skill_md() {
  local sub kind key skill_md
  while IFS=$'\t' read -r sub kind key skill_md; do
    case "$sub" in
      wake|brief|retro) ;;
      *) continue ;;  # global "*" rows point at docs/reference/config.md (assertion 5's concern)
    esac
    case "$kind" in
      flag|env) ;;
      *) continue ;;
    esac
    if [[ ! -f "$skill_md" ]]; then
      _clast_test_fail "assertion2: $sub $key mentioned in $skill_md"
      printf '       ERROR: test/parity.tsv row %s/%s/%s points at missing file %s\n' "$sub" "$kind" "$key" "$skill_md" >&2
      continue
    fi
    if grep -Fq -- "$key" "$skill_md"; then
      _clast_test_pass "assertion2: $sub $key mentioned in $skill_md"
    else
      _clast_test_fail "assertion2: $sub $key mentioned in $skill_md"
      printf '       ERROR: %s (%s) is not mentioned in %s\n' "$key" "$sub" "$skill_md" >&2
    fi
  done < <(grep -v '^#' "$PARITY_TSV")
}

# --- Assertion 3: cli-only/skill_only rows have a non-empty reason ----------

_parity_is_placeholder_reason() {
  local reason="$1"
  local trimmed="${reason#"${reason%%[![:space:]]*}"}"
  trimmed="${trimmed%"${trimmed##*[![:space:]]}"}"
  case "$trimmed" in
    ""|n/a|N/A|NA|TODO|TBD|FIXME) return 0 ;;
    *) return 1 ;;
  esac
}

assert_parity_3_reason_nonempty() {
  local sub kind key reason
  while IFS=$'\t' read -r sub kind key reason; do
    case "$kind" in
      cli-only|skill_only|internal) ;;
      *) continue ;;
    esac
    if _parity_is_placeholder_reason "$reason"; then
      _clast_test_fail "assertion3: $sub $kind $key has a stated reason"
      printf '       ERROR: test/parity.tsv row %s/%s/%s has an empty/placeholder reason column\n' "$sub" "$kind" "$key" >&2
    else
      _clast_test_pass "assertion3: $sub $kind $key has a stated reason"
    fi
  done < <(grep -v '^#' "$PARITY_TSV")
}

# --- Assertion 4: shared CLAST_WAKE_SINCE default matches CLI<->skill -------

# _parity_wake_since_default <file> — the default token from that file's
# ${CLAST_WAKE_SINCE:-...} occurrence, or empty if not found.
_parity_wake_since_default() {
  # shellcheck disable=SC2016  # literal ${CLAST_WAKE_SINCE:-...} pattern, not expansion
  grep -o '${CLAST_WAKE_SINCE:-[^}]*}' "$1" | head -n1 | sed -E 's/^\$\{CLAST_WAKE_SINCE:-(.*)\}$/\1/'
}

assert_parity_4_wake_since_default() {
  local cli_file="lib/clast/clast-porcelain-subcommands/wake.bash"
  local skill_file="skills/wake/SKILL.md"
  local cli_default skill_default

  cli_default="$(_parity_wake_since_default "$cli_file")"
  skill_default="$(_parity_wake_since_default "$skill_file")"

  if [[ -z "$cli_default" ]]; then
    _clast_test_fail "assertion4: \${CLAST_WAKE_SINCE:-...} found in $cli_file"
    # shellcheck disable=SC2016  # literal ${CLAST_WAKE_SINCE:-...} pattern, not expansion
    printf '       ERROR: no ${CLAST_WAKE_SINCE:-...} occurrence found in %s\n' "$cli_file" >&2
    return
  fi
  if [[ -z "$skill_default" ]]; then
    _clast_test_fail "assertion4: \${CLAST_WAKE_SINCE:-...} found in $skill_file"
    # shellcheck disable=SC2016  # literal ${CLAST_WAKE_SINCE:-...} pattern, not expansion
    printf '       ERROR: no ${CLAST_WAKE_SINCE:-...} occurrence found in %s\n' "$skill_file" >&2
    return
  fi

  if [[ "$cli_default" == "$skill_default" ]]; then
    _clast_test_pass "assertion4: CLAST_WAKE_SINCE default matches ($cli_default)"
  else
    _clast_test_fail "assertion4: CLAST_WAKE_SINCE default matches"
    printf '       ERROR: %s default is %q but %s default is %q\n' \
      "$cli_file" "$cli_default" "$skill_file" "$skill_default" >&2
  fi
}

# --- Assertion 5: CLAST_* value reads vs docs/reference/config.md -----------
#
# Scoped per Decision 6: bare (no leading underscore) ${CLAST_X...}/$CLAST_X
# value reads across lib/clast/**.bash. Two exemption mechanisms:
#   - the _SOURCED family (module-sourcing guards, e.g. _CLAST_LIB_SOURCED)
#     is a suffix *pattern*, not an enumerable name, so it stays a
#     code-level exclusion here rather than a manifest row. In practice
#     these are already excluded by the "bare, no leading underscore"
#     scoping (the guards are named _CLAST_*_SOURCED), but the suffix check
#     is kept as an explicit belt-and-suspenders guard against a
#     differently-prefixed guard being added later.
#   - everything else exempt (dispatcher-internal plumbing, test-only hooks)
#     is data: an `internal` manifest row, self-expiring below.

# _parity_scan_clast_var_reads — bare CLAST_* names read (not assigned) in
# lib/clast/**.bash, one per line, deduplicated.
_parity_scan_clast_var_reads() {
  # shellcheck disable=SC2016  # literal ${CLAST_...}/$CLAST_... patterns, not expansion
  grep -rhoE '\$\{CLAST_[A-Z_]+[:}]|\$CLAST_[A-Z_]+' lib/clast --include='*.bash' \
    | sed -E 's/^\$\{?//; s/[:}]$//' \
    | sort -u
}

assert_parity_5_env_vars_documented() {
  local docs_file="docs/reference/config.md"
  local var
  local -A internal_seen=()

  while IFS= read -r var; do
    [[ -z "$var" ]] && continue
    case "$var" in
      *_SOURCED) continue ;;  # module-sourcing guard family, not an enumerable name
    esac
    if _parity_manifest_has '*' internal "$var"; then
      internal_seen["$var"]=1
      _clast_test_pass "assertion5: $var is an internal exemption (test/parity.tsv)"
      continue
    fi
    if grep -Fq -- "$var" "$docs_file"; then
      _clast_test_pass "assertion5: $var documented in $docs_file"
    else
      _clast_test_fail "assertion5: $var documented in $docs_file"
      printf '       ERROR: %s is read from the environment in lib/clast/**.bash but not documented in %s\n' "$var" "$docs_file" >&2
    fi
  done < <(_parity_scan_clast_var_reads)

  # Self-expiry: every `internal` manifest row must have matched a live read
  # site above, or the exemption is stale and must be deleted.
  local sub kind key reason
  while IFS=$'\t' read -r sub kind key reason; do
    [[ "$kind" == "internal" ]] || continue
    if [[ -n "${internal_seen[$key]:-}" ]]; then
      _clast_test_pass "assertion5: internal exemption $key has a live read site"
    else
      _clast_test_fail "assertion5: internal exemption $key has a live read site"
      printf '       ERROR: stale exemption %s: no read sites found in lib/clast/**.bash, delete this row\n' "$key" >&2
    fi
  done < <(grep -v '^#' "$PARITY_TSV")
}

# --- Run ----------------------------------------------------------------------

assert_parity_1_help_vs_manifest
assert_parity_2_mentioned_in_skill_md
assert_parity_3_reason_nonempty
assert_parity_4_wake_since_default
assert_parity_5_env_vars_documented

clast_test_summary
