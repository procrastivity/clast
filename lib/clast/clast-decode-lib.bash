# clast-decode-lib.bash — segment ↔ path encoder/decoder
#
# Claude Code stores transcripts under ~/.claude/projects/<segment>/, where
# <segment> is the absolute project path with `/` replaced by `-`. The
# encoding is lossy: a literal `-` in the source path becomes a `-` in the
# segment, identical to a path separator. Decoding ambiguity is resolved
# against the filesystem (and, for git repos, a `git rev-parse` probe).
#
# See docs/explanation/data-model.md for the "segment" term and
# docs/reference/repo-bootstrap.md#libclastclast-decode-libbash for the algorithm.
# shellcheck shell=bash

if [[ -n "${_CLAST_DECODE_LIB_SOURCED:-}" ]]; then
  return 0
fi
_CLAST_DECODE_LIB_SOURCED=1

# clast_encode_path <absolute-path>
#   "/home/beau/code/xesapps" -> "-home-beau-code-xesapps"
#   "C:/Users/Beast/foo"      -> "C--Users-Beast-foo"
clast_encode_path() {
  local path="$1"
  if [[ -z "$path" ]]; then
    printf '\n'
    return 0
  fi
  # Windows-style drive letter: "C:/..." -> "C-/..." (one dash for the colon),
  # then the standard `/` -> `-` replacement turns the leading slash into a
  # second dash, giving the canonical "C--..." form.
  if [[ "$path" =~ ^([A-Za-z]):(/.*)$ ]]; then
    path="${BASH_REMATCH[1]}-${BASH_REMATCH[2]}"
  fi
  printf '%s\n' "${path//\//-}"
}

# _clast_split_segment <segment>
#   Strips a leading dash and (optionally) a Windows drive prefix. Sets
#   globals _CLAST_PREFIX and _CLAST_REST for the caller. Returns 0 on
#   success, 1 on empty input.
#
#   "-home-beau-foo"      -> prefix="/",    rest="home-beau-foo"
#   "C--Users-Beast-foo"  -> prefix="C:/",  rest="Users-Beast-foo"
#   "foo"                 -> prefix="/",    rest="foo"        (no leading dash)
_clast_split_segment() {
  local seg="$1"
  if [[ -z "$seg" ]]; then
    return 1
  fi
  if [[ "$seg" =~ ^([A-Za-z])--(.*)$ ]]; then
    _CLAST_PREFIX="${BASH_REMATCH[1]}:/"
    _CLAST_REST="${BASH_REMATCH[2]}"
  elif [[ "$seg" == -* ]]; then
    _CLAST_PREFIX="/"
    _CLAST_REST="${seg#-}"
  else
    _CLAST_PREFIX="/"
    _CLAST_REST="$seg"
  fi
  return 0
}

# clast_decode_candidates <segment>
#   Print every possible decoding, one per line. No filesystem checks.
#   Order is deterministic: separator-heavy decodings first (more `/`),
#   matching the naive decode at the top of the list.
clast_decode_candidates() {
  local seg="$1"
  if [[ -z "$seg" ]]; then
    printf '\n'
    return 0
  fi
  _clast_split_segment "$seg" || return 1
  local prefix="$_CLAST_PREFIX" rest="$_CLAST_REST"

  # Split rest on `-`. Each gap between tokens is either `/` (separator) or
  # `-` (literal). Iterate bitmasks 0..(2^gaps - 1); bit set => literal dash.
  local -a tokens=()
  local IFS='-'
  read -r -a tokens <<<"$rest"
  unset IFS
  local n=${#tokens[@]}
  if (( n == 0 )); then
    printf '%s\n' "$prefix"
    return 0
  fi
  local gaps=$((n - 1))
  local total=$((1 << gaps))
  local mask i candidate
  for (( mask = 0; mask < total; mask++ )); do
    candidate="${tokens[0]}"
    for (( i = 1; i < n; i++ )); do
      if (( (mask >> (i - 1)) & 1 )); then
        candidate+="-${tokens[i]}"
      else
        candidate+="/${tokens[i]}"
      fi
    done
    printf '%s%s\n' "$prefix" "$candidate"
  done
}

# _clast_naive_decode <segment>
#   The "all dashes are separators" decoding — the first candidate.
_clast_naive_decode() {
  local seg="$1"
  if [[ -z "$seg" ]]; then
    printf '\n'
    return 0
  fi
  _clast_split_segment "$seg" || return 1
  local rest="${_CLAST_REST//-/\/}"
  printf '%s%s\n' "$_CLAST_PREFIX" "$rest"
}

# clast_decode_segment <segment>
#   Print the resolved absolute path. Exit 0 if confident, 1 if the answer
#   is the naive decode but disk evidence is missing (caller may surface).
clast_decode_segment() {
  local seg="$1"
  if [[ -z "$seg" ]]; then
    printf '\n'
    return 0
  fi

  local naive
  naive="$(_clast_naive_decode "$seg")"

  # 1. Naive decode resolves on disk → done.
  if [[ -d "$naive" ]]; then
    printf '%s\n' "$naive"
    return 0
  fi

  # 2/3. Generate candidates, intersect with the filesystem.
  local -a candidates=() existing=()
  mapfile -t candidates < <(clast_decode_candidates "$seg")
  local c
  for c in "${candidates[@]}"; do
    if [[ -d "$c" ]]; then
      existing+=("$c")
    fi
  done

  if (( ${#existing[@]} == 1 )); then
    printf '%s\n' "${existing[0]}"
    return 0
  fi

  if (( ${#existing[@]} > 1 )); then
    # 3a. Consult sessions-index.json if present.
    local idx projects_dir project_path
    projects_dir="${CLAST_PROJECTS_DIR:-$HOME/.claude/projects}"
    idx="$projects_dir/$seg/sessions-index.json"
    if [[ -r "$idx" ]]; then
      project_path="$(jq -r '.projectPath // empty' "$idx" 2>/dev/null || true)"
      if [[ -n "$project_path" ]]; then
        for c in "${existing[@]}"; do
          if [[ "$c" == "$project_path" ]]; then
            printf '%s\n' "$c"
            return 0
          fi
        done
      fi
    fi
    # 3b. git rev-parse probe.
    local matches=()
    for c in "${existing[@]}"; do
      if git -C "$c" rev-parse --show-toplevel >/dev/null 2>&1; then
        matches+=("$c")
      fi
    done
    if (( ${#matches[@]} == 1 )); then
      printf '%s\n' "${matches[0]}"
      return 0
    fi
    # Still ambiguous — surface naive, caller decides.
    printf '%s\n' "$naive"
    return 1
  fi

  # 4. No candidate exists on disk.
  printf '%s\n' "$naive"
  return 1
}
