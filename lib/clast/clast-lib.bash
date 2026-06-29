# clast-lib.bash — common helpers
#
# Sourced by the dispatcher and (transitively) by subcommands and tests.
# Requires bash 4.4+, jq, and GNU coreutils `date` on PATH.
# shellcheck shell=bash

# Guard against double-sourcing.
if [[ -n "${_CLAST_LIB_SOURCED:-}" ]]; then
  return 0
fi
_CLAST_LIB_SOURCED=1

# Hard dependency check: jq must be on PATH. Exit 3 (env problem) per
# docs/explanation/conventions.md#exit-codes if missing — no grep/sed fallback.
if ! command -v jq >/dev/null 2>&1; then
  echo "clast: error: required dependency 'jq' not found on PATH" >&2
  exit 3
fi

# --- Path helpers --------------------------------------------------------

clast_journal_dir() {
  printf '%s\n' "${CLAST_JOURNAL_DIR:-$HOME/.claude/journal}"
}

clast_projects_dir() {
  printf '%s\n' "${CLAST_PROJECTS_DIR:-$HOME/.claude/projects}"
}

# --- Logging -------------------------------------------------------------

# CLAST_QUIET=1 silences info logs. The global --quiet flag (step 03)
# will set this before any subcommand runs.
clast_log_info() {
  if [[ -z "${CLAST_QUIET:-}" ]]; then
    printf 'clast: info: %s\n' "$*" >&2
  fi
}

clast_log_warn() {
  printf 'clast: warn: %s\n' "$*" >&2
}

clast_log_error() {
  printf 'clast: error: %s\n' "$*" >&2
}

# --- JSON ----------------------------------------------------------------

# clast_json_get <jq-expression> <input>
#   Thin wrapper around jq. <input> is a JSON string (not a file).
#   Use process substitution for file input: clast_json_get '.x' "$(cat f.json)".
clast_json_get() {
  local expr="$1" input="$2"
  jq -r "$expr" <<<"$input"
}

# --- Front-matter / YAML -------------------------------------------------
#
# Curated journal entries are Markdown with a leading YAML front-matter block
# fenced by `---`. These two primitives are the single source of truth for
# reading that block; `entries.bash` and `clast-retro-lib.bash` both build on
# them.

# clast_read_frontmatter <path>
#   Emit the raw front-matter lines (between the first two `---` fences) to
#   stdout. Nothing is emitted for a file without a front-matter block.
clast_read_frontmatter() {
  local path="$1"
  awk '
    BEGIN { in_fm = 0; seen = 0 }
    /^---[[:space:]]*$/ {
      if (!seen) { in_fm = 1; seen = 1; next }
      if (in_fm) { exit }
    }
    in_fm { print }
  ' "$path"
}

# clast_yaml_unquote <string>
#   Strip surrounding double quotes and unescape \", \\, \n on a YAML scalar.
#   A bare (unquoted) value is returned unchanged.
clast_yaml_unquote() {
  local v="$1"
  if [[ "${v:0:1}" == '"' && "${v: -1}" == '"' && ${#v} -ge 2 ]]; then
    v="${v:1:${#v}-2}"
    # Process escapes in order: \\ → placeholder, \" → ", \n → LF, placeholder → \
    v="${v//\\\\/$'\x01'}"
    v="${v//\\\"/\"}"
    v="${v//\\n/$'\n'}"
    v="${v//$'\x01'/\\}"
  fi
  printf '%s' "$v"
}

# --- Date math -----------------------------------------------------------
#
# Uses GNU `date -d` for relative-date math. The nix dev shell pulls in
# coreutils, so this works on macOS too when invoked from inside the shell.
# BSD `date` is NOT supported.

# _clast_now_epoch — overridable hook so tests can inject a fixed "now".
# Tests set CLAST_NOW_EPOCH=<seconds> to freeze time.
_clast_now_epoch() {
  if [[ -n "${CLAST_NOW_EPOCH:-}" ]]; then
    printf '%s\n' "$CLAST_NOW_EPOCH"
  else
    date +%s
  fi
}

# clast_today — local YYYY-MM-DD, adjusted by CLAST_DAY_CUTOFF (HH:MM, default 04:00).
# A session starting before today's cutoff belongs to yesterday's bucket.
clast_today() {
  local cutoff="${CLAST_DAY_CUTOFF:-04:00}"
  local cutoff_hours cutoff_mins cutoff_secs now adjusted
  cutoff_hours="${cutoff%%:*}"
  cutoff_mins="${cutoff##*:}"
  # Strip leading zeros so bash arithmetic doesn't treat them as octal.
  cutoff_hours=$((10#$cutoff_hours))
  cutoff_mins=$((10#$cutoff_mins))
  cutoff_secs=$((cutoff_hours * 3600 + cutoff_mins * 60))
  now="$(_clast_now_epoch)"
  adjusted=$((now - cutoff_secs))
  date -d "@$adjusted" +%Y-%m-%d
}

# clast_parse_date <input> — print YYYY-MM-DD on stdout, exit non-zero on bad input.
# Accepts:
#   - ISO date: 2026-05-30
#   - Keywords: today, yesterday, last-week
#   - Offsets:  -1d, -3d, -1w, -2w (negative-only; future dates not supported)
clast_parse_date() {
  local input="$1"
  if [[ -z "$input" ]]; then
    clast_log_error "clast_parse_date: empty input"
    return 2
  fi

  case "$input" in
    today)
      clast_today
      return 0
      ;;
    yesterday)
      _clast_offset_date 1 d
      return 0
      ;;
    last-week)
      _clast_offset_date 1 w
      return 0
      ;;
    -[0-9]*d|-[0-9]*w)
      local n unit
      n="${input#-}"
      unit="${n: -1}"
      n="${n%?}"
      if ! [[ "$n" =~ ^[0-9]+$ ]]; then
        clast_log_error "clast_parse_date: invalid offset '$input'"
        return 2
      fi
      _clast_offset_date "$n" "$unit"
      return 0
      ;;
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9])
      # Validate by round-tripping through GNU date.
      if ! date -d "$input" +%Y-%m-%d 2>/dev/null; then
        clast_log_error "clast_parse_date: invalid ISO date '$input'"
        return 2
      fi
      return 0
      ;;
    *)
      clast_log_error "clast_parse_date: unrecognized '$input'"
      return 2
      ;;
  esac
}

# _clast_offset_date <n> <d|w>  — print today's bucket minus N days/weeks.
_clast_offset_date() {
  local n="$1" unit="$2" days base
  case "$unit" in
    d) days="$n" ;;
    w) days=$((n * 7)) ;;
    *)
      clast_log_error "_clast_offset_date: bad unit '$unit'"
      return 2
      ;;
  esac
  base="$(clast_today)"
  date -d "$base -$days days" +%Y-%m-%d
}

# --- Atomic write --------------------------------------------------------

# clast_atomic_write <path> <content>
#   Writes <content> to <path>.tmp.$$ then renames over <path>.
#   On any failure during the temp write, leaves the original untouched and
#   removes the temp file.
clast_atomic_write() {
  local path="$1" content="$2" tmp
  tmp="$path.tmp.$$"
  if ! printf '%s' "$content" >"$tmp" 2>/dev/null; then
    rm -f "$tmp" 2>/dev/null || true
    clast_log_error "clast_atomic_write: failed to write '$tmp'"
    return 1
  fi
  if ! mv -f "$tmp" "$path" 2>/dev/null; then
    rm -f "$tmp" 2>/dev/null || true
    clast_log_error "clast_atomic_write: failed to rename onto '$path'"
    return 1
  fi
  return 0
}

# --- Version / usage -----------------------------------------------------

_CLAST_VERSION_CACHE=""

clast_version() {
  if [[ -n "$_CLAST_VERSION_CACHE" ]]; then
    printf '%s\n' "$_CLAST_VERSION_CACHE"
    return 0
  fi
  local pkg pkg_root
  pkg_root="${CLAST_LIB:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
  # CLAST_LIB points at lib/clast; the package.json sits two levels up.
  if [[ -f "$pkg_root/package.json" ]]; then
    pkg="$pkg_root/package.json"
  elif [[ -f "$pkg_root/../../package.json" ]]; then
    pkg="$pkg_root/../../package.json"
  else
    clast_log_error "clast_version: package.json not found"
    return 1
  fi
  _CLAST_VERSION_CACHE="$(jq -r '.version' "$pkg")"
  printf '%s\n' "$_CLAST_VERSION_CACHE"
}

# clast_usage — print top-level usage to stdout. The dispatcher redirects
# to stderr for usage errors.
clast_usage() {
  cat <<'EOF'
clast-plumbing — Claude Code session journal (deterministic core)

Usage:
  clast-plumbing [GLOBAL FLAGS] <subcommand> [ARGS...]

Subcommands:
  whereami      Show current path, registry, and journal state
  snapshot      Capture new transcripts into the journal
  projects      List projects with activity in a window
  sessions      List sessions in a window
  show          Dump session metadata
  entries       List or read curated journal entries
  breadcrumb    Append a one-line in-flight hint
  registry      Manage the project registry
  stats         Token/duration/session-count stats
  doctor        Sanity-check the journal

Global flags:
  -h, --help            Print this usage and exit
      --version         Print version and exit
      --json            Machine-readable JSON output
  -v, --verbose         Extra diagnostic output to stderr
  -q, --quiet           Suppress informational stdout output
      --journal-dir P   Override ~/.claude/journal/ (env: CLAST_JOURNAL_DIR)
      --projects-dir P  Override ~/.claude/projects/ (env: CLAST_PROJECTS_DIR)

The user-facing porcelain (LLM-aware) is `clast`. Run `clast --help` for
the `wake` and `brief` subcommands.
EOF
}
