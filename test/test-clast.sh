#!/usr/bin/env bash
# test-clast.sh — top-level test aggregator. Invokes every test/test-*.sh
# script and exits non-zero if any of them fail.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

# Static checks on the Claude Code plugin assets. The plugin layer has no
# behavioral surface to integration-test — there's nothing to invoke and
# nothing to assert beyond "files exist and are well-formed."
_assert_skill_brief_frontmatter() {
  local skill=skills/brief/SKILL.md
  local ok=0
  if [[ ! -f "$skill" ]]; then
    printf 'brief SKILL.md: file not found\n' >&2
    return 1
  fi
  # Must start with ---
  if [[ "$(head -1 "$skill")" != '---' ]]; then
    printf 'brief SKILL.md: must begin with ---\n' >&2
    ok=1
  fi
  # Second --- must appear within first 50 lines
  if ! awk '/^---$/{n++; if(n==2){found=1; exit}} END{exit !found}' \
       <(head -50 "$skill"); then
    printf 'brief SKILL.md: closing --- not found within first 50 lines\n' >&2
    ok=1
  fi
  if ! grep -q 'name: brief' "$skill"; then
    printf 'brief SKILL.md: missing name: brief\n' >&2
    ok=1
  fi
  local desc_line
  desc_line=$(grep -c 'description:' "$skill" || true)
  if [[ "$desc_line" -lt 1 ]]; then
    printf 'brief SKILL.md: missing description field\n' >&2
    ok=1
  fi
  # description value must be substantial (>100 chars across the block)
  local desc_len
  desc_len=$(awk '/^description:/{p=1} p{s=s $0} /^---$/ && NR>1{p=0} END{print length(s)}' "$skill")
  if [[ "$desc_len" -lt 100 ]]; then
    printf 'brief SKILL.md: description field too short (%d chars)\n' "$desc_len" >&2
    ok=1
  fi
  return "$ok"
}

_assert_skill_brief_triggers() {
  local skill=skills/brief/SKILL.md
  local ok=0
  for phrase in '/brief' 'where was I' 'resume'; do
    if ! grep -q "$phrase" "$skill"; then
      printf 'brief SKILL.md: missing trigger phrase: %s\n' "$phrase" >&2
      ok=1
    fi
  done
  return "$ok"
}

_assert_skill_brief_cli_commands() {
  local skill=skills/brief/SKILL.md
  local ok=0
  for cmd in 'CLAST_BIN registry resolve' 'entries --project' 'CLAST_BIN entries read' \
             'CLAST_BIN breadcrumb --read' 'sessions --day today'; do
    if ! grep -q "$cmd" "$skill"; then
      printf 'brief SKILL.md: missing CLI command: %s\n' "$cmd" >&2
      ok=1
    fi
  done
  return "$ok"
}

_assert_skill_brief_readonly() {
  local skill=skills/brief/SKILL.md
  local ok=0
  # shellcheck disable=SC2016
  if grep -qE '(clast|\$CLAST_BIN) entries write' "$skill"; then
    printf 'brief SKILL.md: read-only invariant violated — found entries write\n' >&2
    ok=1
  fi
  # shellcheck disable=SC2016
  if grep -qE '(clast|\$CLAST_BIN) breadcrumb [^-]' "$skill"; then
    printf 'brief SKILL.md: read-only invariant violated — found write-form breadcrumb\n' >&2
    ok=1
  fi
  # shellcheck disable=SC2016
  if grep -qE '(clast|\$CLAST_BIN) snapshot' "$skill"; then
    printf 'brief SKILL.md: read-only invariant violated — found snapshot\n' >&2
    ok=1
  fi
  return "$ok"
}

_assert_skill_wake_frontmatter() {
  local skill=skills/wake/SKILL.md
  local ok=0
  if [[ ! -f "$skill" ]]; then
    printf 'wake SKILL.md: file not found\n' >&2
    return 1
  fi
  if [[ "$(head -1 "$skill")" != '---' ]]; then
    printf 'wake SKILL.md: must begin with ---\n' >&2
    ok=1
  fi
  if ! awk '/^---$/{n++; if(n==2){found=1; exit}} END{exit !found}' \
       <(head -50 "$skill"); then
    printf 'wake SKILL.md: closing --- not found within first 50 lines\n' >&2
    ok=1
  fi
  if ! grep -q '^name: wake$' "$skill"; then
    printf 'wake SKILL.md: missing name: wake\n' >&2
    ok=1
  fi
  local key_count
  key_count=$(awk '/^---$/{n++; next} n==1 && /^[A-Za-z_-]+:/{c++} END{print c+0}' "$skill")
  if [[ "$key_count" -ne 2 ]]; then
    printf 'wake SKILL.md: frontmatter must contain exactly two keys (found %d)\n' "$key_count" >&2
    ok=1
  fi
  local desc_line
  desc_line=$(grep -c '^description:' "$skill" || true)
  if [[ "$desc_line" -lt 1 ]]; then
    printf 'wake SKILL.md: missing description field\n' >&2
    ok=1
  fi
  local desc_len
  desc_len=$(awk '/^description:/{p=1} p{s=s $0} /^---$/ && NR>1{p=0} END{print length(s)}' "$skill")
  if [[ "$desc_len" -lt 100 ]]; then
    printf 'wake SKILL.md: description field too short (%d chars)\n' "$desc_len" >&2
    ok=1
  fi
  return "$ok"
}

_assert_skill_wake_triggers() {
  local skill=skills/wake/SKILL.md
  local ok=0
  for phrase in '/wake' 'morning briefing' 'catch me up on yesterday'; do
    if ! grep -q "$phrase" "$skill"; then
      printf 'wake SKILL.md: missing trigger phrase: %s\n' "$phrase" >&2
      ok=1
    fi
  done
  return "$ok"
}

_assert_skill_wake_cli_commands() {
  local skill=skills/wake/SKILL.md
  local ok=0
  for cmd in 'CLAST_BIN snapshot' 'CLAST_BIN --json sessions --since' 'CLAST_BIN --json show' \
             'CLAST_BIN breadcrumb --read' 'CLAST_BIN entries write' 'CLAST_BIN sessions dismiss'; do
    if ! grep -q "$cmd" "$skill"; then
      printf 'wake SKILL.md: missing CLI command: %s\n' "$cmd" >&2
      ok=1
    fi
  done
  return "$ok"
}

_assert_plugin_assets() {
  local ok=0
  if ! jq -e . .claude-plugin/plugin.json >/dev/null; then
    printf 'plugin asset check: .claude-plugin/plugin.json is not valid JSON\n' >&2
    ok=1
  fi
  if ! jq -e '.hooks | type == "array" and length == 1' hooks/hooks.json >/dev/null; then
    printf 'plugin asset check: hooks/hooks.json must have exactly one hook entry\n' >&2
    ok=1
  fi
  if ! shellcheck --shell=bash hooks/snapshot.sh; then
    printf 'plugin asset check: hooks/snapshot.sh failed shellcheck\n' >&2
    ok=1
  fi
  printf 'plugin asset check: brief/SKILL.md frontmatter\n'
  if ! _assert_skill_brief_frontmatter; then ok=1; fi
  printf 'plugin asset check: brief/SKILL.md trigger phrases\n'
  if ! _assert_skill_brief_triggers; then ok=1; fi
  printf 'plugin asset check: brief/SKILL.md CLI commands\n'
  if ! _assert_skill_brief_cli_commands; then ok=1; fi
  printf 'plugin asset check: brief/SKILL.md read-only invariant\n'
  if ! _assert_skill_brief_readonly; then ok=1; fi
  printf 'plugin asset check: wake/SKILL.md frontmatter\n'
  if ! _assert_skill_wake_frontmatter; then ok=1; fi
  printf 'plugin asset check: wake/SKILL.md trigger phrases\n'
  if ! _assert_skill_wake_triggers; then ok=1; fi
  printf 'plugin asset check: wake/SKILL.md CLI commands\n'
  if ! _assert_skill_wake_cli_commands; then ok=1; fi
  return "$ok"
}

declare -a suites=(
  test/test-lib.sh
  test/test-decode.sh
  test/test-dispatcher.sh
  test/test-whereami.sh
  test/test-manifest.sh
  test/test-registry.sh
  test/test-registry-cmd.sh
  test/test-snapshot.sh
  test/test-query.sh
  test/test-entries.sh
  test/test-retro.sh
  test/test-retro-manifest.sh
  test/test-retro-cmd.sh
  test/test-retro-summary.sh
  test/test-brief.sh
  test/test-breadcrumb.sh
  test/test-doctor.sh
  test/test-stats.sh
  test/test-install.sh
  test/test-migrate-slug.sh
)

fail=0
printf '== plugin assets ==\n'
if ! _assert_plugin_assets; then
  fail=$((fail + 1))
fi

for suite in "${suites[@]}"; do
  printf '== %s ==\n' "$suite"
  if ! bash "$suite"; then
    fail=$((fail + 1))
  fi
done

if (( fail > 0 )); then
  printf '\n%d test suite(s) failed\n' "$fail" >&2
  exit 1
fi
printf '\nall test suites passed\n'
