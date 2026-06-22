#!/usr/bin/env bash
# test-migrate-slug.sh — contrib/migrate-slug.sh against a synthetic journal.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# shellcheck source=test/helpers.sh
source test/helpers.sh
_CLAST_TEST_NAME="test-migrate-slug"

SCRIPT="$PWD/contrib/migrate-slug.sh"
# Make clast-plumbing resolvable so the script's post-migration doctor runs.
export PATH="$PWD/bin:$PATH"

# Seed a pre-model journal: 4 lines sharing a remote under the old slug with
# stale alias roll-ups and no labels, plus an unrelated project, plus entries.
_seed_pre_model() {
  setup_test_journal >/dev/null
  cat > "$CLAST_JOURNAL_DIR/projects.json" <<'EOF'
{"path":"/home/u/Workspaces/dev/xesapps","slug":"dev-xesapps","remote":"git@host:xesapps.git","first_seen":"2026-06-04","aliases":[]}
{"path":"/home/u/Workspaces/performance/xesapps","slug":"dev-xesapps","remote":"git@host:xesapps.git","first_seen":"2026-06-10","aliases":["/home/u/Workspaces/dev/xesapps"]}
{"path":"/home/u/Workspaces/review/xesapps","slug":"dev-xesapps","remote":"git@host:xesapps.git","first_seen":"2026-06-16","aliases":["/home/u/Workspaces/dev/xesapps","/home/u/Workspaces/performance/xesapps"]}
{"path":"/home/u/Workspaces/control/xesapps","slug":"dev-xesapps","remote":"git@host:xesapps.git","first_seen":"2026-06-16","aliases":["/home/u/Workspaces/dev/xesapps"]}
{"path":"/home/u/Code/other","slug":"other","remote":"git@host:other.git","first_seen":"2026-06-01","aliases":[]}
EOF
  mkdir -p "$CLAST_JOURNAL_DIR/entries"
  # Entry from the performance clone, no label, project=dev-xesapps.
  cat > "$CLAST_JOURNAL_DIR/entries/perf.md" <<'EOF'
---
date: 2026-06-17
time: 18:41
day_bucket: 2026-06-17
project: dev-xesapps
project_path: /home/u/Workspaces/performance/xesapps
project_remote: git@host:xesapps.git
branch: dev
author: u
tags: [mysql]
session_id: 11111111-1111-4111-8111-111111111111
session_slug: perf
snapshot_path: transcripts/x
machine: m
curated_source_mtime: "2026-06-17T18:41:00Z"
---

# Session: Perf

Body must stay byte-for-byte.
EOF
  # Unrelated entry — must be untouched.
  cat > "$CLAST_JOURNAL_DIR/entries/other.md" <<'EOF'
---
date: 2026-06-15
time: 09:00
day_bucket: 2026-06-15
project: other
project_path: /home/u/Code/other
author: u
tags: []
session_id: 22222222-2222-4222-8222-222222222222
session_slug: thing
snapshot_path: transcripts/z
machine: m
curated_source_mtime: "2026-06-15T09:00:00Z"
---

# Session: Other

Other body.
EOF
}

# --- dry-run: changes nothing -----------------------------------------------
_seed_pre_model
before="$(cat "$CLAST_JOURNAL_DIR/projects.json")"
out="$(bash "$SCRIPT" --journal-dir "$CLAST_JOURNAL_DIR" --dry-run dev-xesapps xesapps 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "dry-run: exits 0"
assert_eq "$before" "$(cat "$CLAST_JOURNAL_DIR/projects.json")" "dry-run: registry untouched"
assert_file_not_exists "$CLAST_JOURNAL_DIR/.migrations" "dry-run: no backups written"
case "$out" in
  *"Registry lines to rewrite: 4"*) _clast_test_pass "dry-run: counts 4 registry lines" ;;
  *) _clast_test_fail "dry-run: counts 4 registry lines"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- real run ----------------------------------------------------------------
_seed_pre_model
perf_body_before="$(awk 'f{print} /^---$/{n++; if(n==2)f=1}' "$CLAST_JOURNAL_DIR/entries/perf.md")"
out="$(bash "$SCRIPT" --journal-dir "$CLAST_JOURNAL_DIR" --yes dev-xesapps xesapps 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "real run: exits 0 (doctor clean)"

reg="$(cat "$CLAST_JOURNAL_DIR/projects.json")"
# All four dev-xesapps lines re-slugged; the `other` line untouched.
xes_count="$(jq -rR 'fromjson? | select(.slug=="xesapps") | .path' <<<"$reg" | grep -c . || true)"
assert_eq "4" "$xes_count" "real run: 4 lines now slug=xesapps"
old_count="$(jq -rR 'fromjson? | select(.slug=="dev-xesapps") | .path' <<<"$reg" | grep -c . || true)"
assert_eq "0" "$old_count" "real run: no dev-xesapps lines remain"
other_ok="$(jq -rR 'fromjson? | select(.path=="/home/u/Code/other") | .slug' <<<"$reg" | tail -1)"
assert_eq "other" "$other_ok" "real run: unrelated line untouched"

# Labels derived from each path's parent directory; aliases cleared.
for pair in "dev:/home/u/Workspaces/dev/xesapps" \
            "performance:/home/u/Workspaces/performance/xesapps" \
            "review:/home/u/Workspaces/review/xesapps" \
            "control:/home/u/Workspaces/control/xesapps"; do
  want_label="${pair%%:*}"; want_path="${pair#*:}"
  got_label="$(jq -rR --arg p "$want_path" 'fromjson? | select(.path==$p) | .label // "MISSING"' <<<"$reg" | tail -1)"
  assert_eq "$want_label" "$got_label" "real run: label for $want_path"
  got_aliases="$(jq -cR --arg p "$want_path" 'fromjson? | select(.path==$p) | .aliases' <<<"$reg" | tail -1)"
  assert_eq "[]" "$got_aliases" "real run: aliases cleared for $want_path"
done

# Entry re-projected + label backfilled from its own project_path; body intact.
perf="$(cat "$CLAST_JOURNAL_DIR/entries/perf.md")"
case "$perf" in
  *"project: xesapps"*) _clast_test_pass "real run: entry project rewritten" ;;
  *) _clast_test_fail "real run: entry project rewritten"; printf '%s\n' "$perf" >&2 ;;
esac
case "$perf" in
  *"label: performance"*) _clast_test_pass "real run: entry label backfilled from project_path" ;;
  *) _clast_test_fail "real run: entry label backfilled from project_path"; printf '%s\n' "$perf" >&2 ;;
esac
perf_body_after="$(awk 'f{print} /^---$/{n++; if(n==2)f=1}' "$CLAST_JOURNAL_DIR/entries/perf.md")"
assert_eq "$perf_body_before" "$perf_body_after" "real run: entry body unchanged"

# Unrelated entry untouched.
case "$(cat "$CLAST_JOURNAL_DIR/entries/other.md")" in
  *"project: other"*) _clast_test_pass "real run: unrelated entry untouched" ;;
  *) _clast_test_fail "real run: unrelated entry untouched" ;;
esac

# Backup created with the changed files.
backup_root="$CLAST_JOURNAL_DIR/.migrations"
assert_file_exists "$backup_root" "real run: backup dir created"
bcount="$(find "$backup_root" -type f -name '*.json' -o -type f -name '*.md' 2>/dev/null | grep -c . || true)"
case "$bcount" in
  0) _clast_test_fail "real run: backup contains files (got 0)" ;;
  *) _clast_test_pass "real run: backup contains files ($bcount)" ;;
esac
teardown_test_journal

# --- idempotent re-run -------------------------------------------------------
_seed_pre_model
bash "$SCRIPT" --journal-dir "$CLAST_JOURNAL_DIR" --yes dev-xesapps xesapps >/dev/null 2>&1
out="$(bash "$SCRIPT" --journal-dir "$CLAST_JOURNAL_DIR" --yes dev-xesapps xesapps 2>&1)" && rc=$? || rc=$?
assert_eq "0" "$rc" "re-run: exits 0"
case "$out" in
  *"nothing to migrate"*) _clast_test_pass "re-run: reports nothing to migrate" ;;
  *) _clast_test_fail "re-run: reports nothing to migrate"; printf '%s\n' "$out" >&2 ;;
esac
teardown_test_journal

# --- arg validation ----------------------------------------------------------
_seed_pre_model
bash "$SCRIPT" --journal-dir "$CLAST_JOURNAL_DIR" --yes same same >/dev/null 2>&1 && rc=$? || rc=$?
assert_eq "2" "$rc" "identical slugs: exits 2"
bash "$SCRIPT" --journal-dir "$CLAST_JOURNAL_DIR" --yes old 'Bad Slug' >/dev/null 2>&1 && rc=$? || rc=$?
assert_eq "2" "$rc" "invalid new-slug: exits 2"
teardown_test_journal

clast_test_summary
