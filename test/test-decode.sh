#!/usr/bin/env bash
# test-decode.sh — exercises clast-decode-lib.bash.
set -euo pipefail

# Run from repo root regardless of how we were invoked.
cd "$(dirname "$0")/.." || exit 1

_CLAST_TEST_NAME="test-decode"
# shellcheck source=test/helpers.sh
source test/helpers.sh
# shellcheck source=lib/clast/clast-lib.bash
source lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash
source lib/clast/clast-decode-lib.bash

# --- Encode/decode round-trip (no literal dashes) ----------------------------
encoded="$(clast_encode_path /home/beau/code/xesapps)"
assert_eq "-home-beau-code-xesapps" "$encoded" "encode /home/beau/code/xesapps"

# Deep path
encoded2="$(clast_encode_path /a/b/c/d/e/f)"
assert_eq "-a-b-c-d-e-f" "$encoded2" "encode deep path"

# Single component
encoded3="$(clast_encode_path /foo)"
assert_eq "-foo" "$encoded3" "encode /foo"

# --- Naive decode resolves when the path exists ------------------------------
setup_test_journal >/dev/null
trap 'teardown_test_journal; rm -rf /tmp/clast /tmp/clast-foo' EXIT

# Use a real-on-disk path under the test tmpdir. We need a segment whose
# naive decode points at the tmpdir tree.
mkdir -p "$_CLAST_TEST_TMPDIR/proj"
seg_existing="$(clast_encode_path "$_CLAST_TEST_TMPDIR/proj")"
decoded="$(clast_decode_segment "$seg_existing")"
assert_eq "$_CLAST_TEST_TMPDIR/proj" "$decoded" "naive decode resolves on disk"

# --- Empty segment -----------------------------------------------------------
empty_decoded="$(clast_decode_segment "" || true)"
assert_eq "" "$empty_decoded" "empty segment decodes empty"

# --- Single-component segment (no dashes other than leading) -----------------
single="$(clast_decode_segment "-foo" || true)"
assert_eq "/foo" "$single" "single-component segment decodes to /foo"

# --- Nonexistent path: naive decode + exit 1 ---------------------------------
naive_out="$(clast_decode_segment "-definitely-not-a-real-path-xyzzy" 2>/dev/null || true)"
assert_eq "/definitely/not/a/real/path/xyzzy" "$naive_out" "nonexistent → naive"
assert_exit_code 1 clast_decode_segment "-definitely-not-a-real-path-xyzzy"

# --- Windows / WSL2 syntactic decode -----------------------------------------
# Pure syntactic transform: we don't expect C:/Users/... to exist on the
# Linux test host, so the decoder will return naive (with exit 1) but the
# prefix logic should still emit "C:/Users/Beast/foo".
win="$(clast_decode_segment "C--Users-Beast-foo" 2>/dev/null || true)"
assert_eq "C:/Users/Beast/foo" "$win" "windows segment decodes with C:/ prefix"

# Encode round-trip for windows path
win_enc="$(clast_encode_path "C:/Users/Beast/foo")"
assert_eq "C--Users-Beast-foo" "$win_enc" "windows encode"

# --- Candidate enumeration ---------------------------------------------------
mapfile -t cands < <(clast_decode_candidates "-a-b-c")
# 3 tokens → 2 gaps → 4 candidates.
assert_eq "4" "${#cands[@]}" "3-token segment has 4 candidates"
assert_eq "/a/b/c" "${cands[0]}" "mask 0 is the naive decode"

# --- Ambiguous decode: two on-disk candidates, git-repo signal ---------------
rm -rf /tmp/clast /tmp/clast-foo
mkdir -p /tmp/clast/foo/bar/baz
mkdir -p /tmp/clast-foo/bar/baz
git init -q /tmp/clast/foo/bar/baz

ambig_seg="-tmp-clast-foo-bar-baz"
ambig_decoded="$(clast_decode_segment "$ambig_seg")"
assert_eq "/tmp/clast/foo/bar/baz" "$ambig_decoded" "ambiguous segment resolved via git signal"

# Cleanup happens via the EXIT trap above.

clast_test_summary
