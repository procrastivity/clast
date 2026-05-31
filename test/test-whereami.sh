#!/usr/bin/env bash
# test-whereami.sh — `clast whereami` output shape and content.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-whereami"

CLAST_BIN="$PWD/bin/clast"

# --- default human output has all 10 labels in order --------------------

human_out="$("$CLAST_BIN" whereami)"
expected_labels=(pwd git_root registered slug remote last_snapshot \
                 journal_dir projects_dir day_cutoff machine)
prev_line=0
order_ok=1
for label in "${expected_labels[@]}"; do
  # `grep -n` returns "<lineno>:<line>"; capture lineno of first match.
  line="$(printf '%s\n' "$human_out" | grep -n "^${label}:" | head -1 | cut -d: -f1)"
  if [[ -z "$line" ]]; then
    _clast_test_fail "human output missing field: $label"
    order_ok=0
    continue
  fi
  if (( line <= prev_line )); then
    _clast_test_fail "human output field out of order: $label (line $line after $prev_line)"
    order_ok=0
  fi
  prev_line="$line"
done
if (( order_ok == 1 )); then
  _clast_test_pass "human output: all 10 labels present and in documented order"
fi

# --- --json output is valid and has expected keys -----------------------

json_out="$("$CLAST_BIN" whereami --json)"
if jq -e . >/dev/null 2>&1 <<<"$json_out"; then
  _clast_test_pass "whereami --json emits valid JSON"
else
  _clast_test_fail "whereami --json emits valid JSON"
  printf '%s\n' "$json_out" >&2
fi

json_keys="$(jq -r 'keys_unsorted | join(",")' <<<"$json_out")"
expected_keys="pwd,git_root,registered,slug,remote,last_snapshot,journal_dir,projects_dir,day_cutoff,machine"
# jq's `keys` is sorted; `keys_unsorted` preserves insertion order from jq -n.
# Sort both sides for a set-equality check.
sorted_got="$(printf '%s\n' "$json_keys" | tr ',' '\n' | sort | paste -sd, -)"
sorted_expected="$(printf '%s\n' "$expected_keys" | tr ',' '\n' | sort | paste -sd, -)"
assert_eq "$sorted_expected" "$sorted_got" "whereami --json keys match contract"

assert_eq "$PWD" "$(jq -r .pwd <<<"$json_out")" "whereami --json: pwd equals \$PWD"

# --- non-git directory: git_root is null/em-dash ------------------------

tmp_nongit="$(mktemp -d -t clast.whereami.nongit.XXXXXX)"
(
  cd "$tmp_nongit" || exit 1
  ng_json="$("$CLAST_BIN" whereami --json)"
  gr="$(jq -r .git_root <<<"$ng_json")"
  if [[ "$gr" == "null" ]]; then
    _clast_test_pass "non-git dir: git_root is null in JSON"
  else
    _clast_test_fail "non-git dir: git_root is null in JSON (got: $gr)"
  fi

  ng_human="$("$CLAST_BIN" whereami)"
  case "$ng_human" in
    *"git_root:"*"—"*) _clast_test_pass "non-git dir: git_root rendered as em-dash" ;;
    *) _clast_test_fail "non-git dir: git_root rendered as em-dash"
       printf '%s\n' "$ng_human" >&2 ;;
  esac
)
rm -rf "$tmp_nongit"

# --- git directory: git_root reports the repo root ----------------------

tmp_git="$(mktemp -d -t clast.whereami.git.XXXXXX)"
(
  cd "$tmp_git" || exit 1
  git init -q
  expected_root="$(git rev-parse --show-toplevel)"
  g_json="$("$CLAST_BIN" whereami --json)"
  assert_eq "$expected_root" "$(jq -r .git_root <<<"$g_json")" \
    "git dir: git_root equals git rev-parse --show-toplevel"
)
rm -rf "$tmp_git"

# --- --quiet does not suppress whereami output --------------------------

q_out="$("$CLAST_BIN" --quiet whereami)"
case "$q_out" in
  *"pwd:"*) _clast_test_pass "--quiet does not suppress whereami stdout" ;;
  *) _clast_test_fail "--quiet does not suppress whereami stdout" ;;
esac

q_stderr="$("$CLAST_BIN" --quiet whereami 2>&1 1>/dev/null)"
case "$q_stderr" in
  *"clast: info:"*) _clast_test_fail "--quiet still leaked info logs to stderr" ;;
  *) _clast_test_pass "--quiet: no info-log chatter on stderr" ;;
esac

# --- CLAST_DAY_CUTOFF propagation --------------------------------------

cutoff_json="$(CLAST_DAY_CUTOFF=06:00 "$CLAST_BIN" whereami --json)"
assert_eq "06:00" "$(jq -r .day_cutoff <<<"$cutoff_json")" \
  "CLAST_DAY_CUTOFF=06:00 reflected in day_cutoff field"

clast_test_summary
