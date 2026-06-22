#!/usr/bin/env bash
# contrib/migrate-slug.sh — rename a registry slug and backfill per-directory
# labels onto existing journal data.
#
# Brings a journal created before the shared-slug + label model (steps 22-23)
# onto it: registry lines with `slug == <old>` are re-slugged to <new>, given
# a `label` derived from each path's parent directory (when absent), and have
# their stale `aliases` roll-ups cleared; curated entries with `project ==
# <old>` are re-projected to <new> and have `label` backfilled from each
# entry's own `project_path`.
#
# Bodies are never touched; only frontmatter keys change. Non-matching and
# malformed registry lines are preserved verbatim. A timestamped backup of
# every file about to change is written under <journal>/.migrations/ first.
#
# Usage:
#   migrate-slug.sh [--journal-dir DIR] [--dry-run] [--yes] <old-slug> <new-slug>
set -euo pipefail

PROG="$(basename "$0")"

usage() {
  cat <<EOF
Usage: $PROG [--journal-dir DIR] [--dry-run] [--yes] <old-slug> <new-slug>

Rename registry slug <old-slug> to <new-slug> and backfill per-directory
labels onto matching registry lines and curated entries.

Options:
  --journal-dir DIR   Journal root (default: \$CLAST_JOURNAL_DIR or ~/.claude/journal)
  --dry-run           Show what would change; write nothing (no backups).
  --yes               Skip the confirmation prompt.
  -h, --help          This help.
EOF
}

die() { printf '%s: %s\n' "$PROG" "$1" >&2; exit "${2:-1}"; }

# slugify <string> — lowercase, non-[a-z0-9] runs → '-', trim, cap 32.
slugify() {
  local s
  s="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' \
    | sed 's/[^a-z0-9]/-/g; s/--*/-/g; s/^-//; s/-$//')"
  s="${s:0:32}"
  printf '%s' "${s%-}"
}

# fm_get <file> <key> — first frontmatter value for <key>, surrounding double
# quotes stripped. Empty if absent.
fm_get() {
  local file="$1" want="$2" val
  val="$(awk -v want="$want" '
    BEGIN { infm = 0; seen = 0 }
    /^---[[:space:]]*$/ { if (!seen) { infm = 1; seen = 1; next } if (infm) exit }
    infm {
      key = $0; sub(/:.*/, "", key)
      if (key == want) { v = $0; sub(/^[^:]*:[[:space:]]*/, "", v); print v; exit }
    }
  ' "$file")"
  # Strip one layer of surrounding double quotes.
  if [[ "${val:0:1}" == '"' && "${val: -1}" == '"' && ${#val} -ge 2 ]]; then
    val="${val:1:${#val}-2}"
  fi
  printf '%s' "$val"
}

# --- Parse args --------------------------------------------------------------
journal_dir="${CLAST_JOURNAL_DIR:-$HOME/.claude/journal}"
dry_run=0
assume_yes=0
old=""
new=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --journal-dir) [[ $# -lt 2 ]] && die "--journal-dir requires a value" 2; journal_dir="$2"; shift 2 ;;
    --journal-dir=*) journal_dir="${1#*=}"; shift ;;
    --dry-run) dry_run=1; shift ;;
    --yes) assume_yes=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) die "unknown flag '$1'" 2 ;;
    *)
      if [[ -z "$old" ]]; then old="$1"
      elif [[ -z "$new" ]]; then new="$1"
      else die "unexpected argument '$1'" 2
      fi
      shift ;;
  esac
done

[[ -z "$old" || -z "$new" ]] && { usage >&2; exit 2; }
[[ "$old" == "$new" ]] && die "old-slug and new-slug are identical" 2
[[ "$new" =~ ^[a-z0-9][a-z0-9-]{0,63}$ ]] || die "new-slug '$new' is not a valid slug ([a-z0-9][a-z0-9-]{0,63})" 2
[[ -d "$journal_dir" ]] || die "journal dir not found: $journal_dir" 1

projects="$journal_dir/projects.json"
entries_dir="$journal_dir/entries"

# --- Survey registry lines ---------------------------------------------------
reg_new=""
reg_count=0
declare -a reg_preview=()
if [[ -f "$projects" ]]; then
  while IFS= read -r raw || [[ -n "$raw" ]]; do
    [[ -z "$raw" ]] && continue
    this_slug="$(printf '%s' "$raw" | jq -rR 'fromjson? | .slug // empty' 2>/dev/null || true)"
    if [[ "$this_slug" == "$old" ]]; then
      transformed="$(printf '%s' "$raw" | jq -cR --arg new "$new" '
        def slugify: ascii_downcase | gsub("[^a-z0-9]+"; "-") | sub("^-"; "") | sub("-$"; "") | .[0:32];
        fromjson
        | .slug = $new
        | .aliases = []
        | (if (.label // "") == ""
           then (((.path // "") | split("/") | (.[-2] // "")) | slugify) as $d
                | (if $d == "" then . else . + {label: $d} end)
           else . end)
      ')"
      reg_new+="$transformed"$'\n'
      reg_count=$((reg_count + 1))
      reg_preview+=("$(printf '%s' "$transformed" | jq -r '"  \(.path)  →  slug=\(.slug) label=\(.label // "(none)") aliases=[]"')")
    else
      reg_new+="$raw"$'\n'
    fi
  done < "$projects"
fi

# --- Survey entries ----------------------------------------------------------
declare -a affected_entries=()
if [[ -d "$entries_dir" ]]; then
  while IFS= read -r f; do
    [[ -z "$f" ]] && continue
    if [[ "$(fm_get "$f" project)" == "$old" ]]; then
      affected_entries+=("$f")
    fi
  done < <(find "$entries_dir" -mindepth 1 -maxdepth 1 -type f -name '*.md' 2>/dev/null | sort)
fi

# --- Nothing to do? ----------------------------------------------------------
if (( reg_count == 0 && ${#affected_entries[@]} == 0 )); then
  printf '%s: nothing to migrate — no registry lines or entries with slug/project "%s".\n' "$PROG" "$old"
  exit 0
fi

# --- Report ------------------------------------------------------------------
printf 'Migration plan: slug "%s" → "%s"\n' "$old" "$new"
printf '  Journal:  %s\n' "$journal_dir"
printf '  Registry lines to rewrite: %d\n' "$reg_count"
for line in "${reg_preview[@]+"${reg_preview[@]}"}"; do
  printf '%s\n' "$line"
done
printf '  Entries to rewrite: %d\n' "${#affected_entries[@]}"
if (( ${#affected_entries[@]} > 0 )); then
  for f in "${affected_entries[@]}"; do
    pp="$(fm_get "$f" project_path)"
    existing_label="$(fm_get "$f" label)"
    if [[ -n "$existing_label" && "$existing_label" != "null" ]]; then
      derived="$existing_label (kept)"
    elif [[ -n "$pp" && "$pp" != "null" ]]; then
      derived="$(slugify "$(basename -- "$(dirname -- "$pp")")")"
      [[ -z "$derived" ]] && derived="(none)"
    else
      derived="(none — no project_path)"
    fi
    printf '    %s  →  label=%s\n' "$(basename "$f")" "$derived"
  done
fi

if (( dry_run == 1 )); then
  printf '\n--dry-run: no files changed.\n'
  exit 0
fi

# --- Confirm -----------------------------------------------------------------
ts="$(date +%Y%m%d-%H%M%S)"
backup_dir="$journal_dir/.migrations/${ts}-${old}-to-${new}"
printf '\n  Backup location: %s\n' "$backup_dir"
if (( assume_yes == 0 )); then
  if [[ ! -t 0 ]]; then
    die "refusing to proceed without a TTY; re-run with --yes" 1
  fi
  printf '  Proceed? [y/N] '
  read -r reply
  case "$reply" in
    y|Y|yes|YES) ;;
    *) printf 'Aborted.\n'; exit 0 ;;
  esac
fi

# --- Backup ------------------------------------------------------------------
mkdir -p "$backup_dir"
[[ -f "$projects" ]] && cp -p "$projects" "$backup_dir/projects.json"
if (( ${#affected_entries[@]} > 0 )); then
  mkdir -p "$backup_dir/entries"
  for f in "${affected_entries[@]}"; do
    cp -p "$f" "$backup_dir/entries/$(basename "$f")"
  done
fi
printf 'Backed up to %s\n' "$backup_dir"

# --- Apply registry ----------------------------------------------------------
if (( reg_count > 0 )); then
  tmp="$projects.tmp.$$"
  printf '%s' "$reg_new" > "$tmp"
  mv -f "$tmp" "$projects"
  printf 'Rewrote %d registry line(s).\n' "$reg_count"
fi

# --- Apply entries -----------------------------------------------------------
entries_done=0
for f in "${affected_entries[@]+"${affected_entries[@]}"}"; do
  pp="$(fm_get "$f" project_path)"
  derived=""
  if [[ -n "$pp" && "$pp" != "null" ]]; then
    derived="$(slugify "$(basename -- "$(dirname -- "$pp")")")"
  fi

  # Does the entry already carry a label line? (present vs. absent — distinct
  # from empty value, which fm_get can't tell apart.)
  has_label=0
  if awk '
    BEGIN { infm = 0; seen = 0 }
    /^---[[:space:]]*$/ { if (!seen) { infm = 1; seen = 1; next } if (infm) exit }
    infm { k = $0; sub(/:.*/, "", k); if (k == "label") { found = 1; exit } }
    END { exit !found }
  ' "$f"; then
    has_label=1
  fi

  # Surgical frontmatter rewrite via awk: only the first frontmatter block,
  # body untouched. project → new; an existing label is rewritten in place
  # (when empty/null); an absent label is inserted right after project_path
  # to match the order `entries write` produces (fallback: before the closing
  # fence if there is no project_path line).
  tmp="$f.tmp.$$"
  awk -v newslug="$new" -v derived="$derived" -v has_label="$has_label" '
    function label_line() { return (derived == "" ? "label: null" : "label: " derived) }
    BEGIN { infm = 0; seen = 0; done_fm = 0; inserted = 0 }
    {
      if (!done_fm && $0 ~ /^---[[:space:]]*$/) {
        if (!seen) { seen = 1; infm = 1; print; next }
        if (infm) {
          if (has_label == "0" && !inserted) { print label_line(); inserted = 1 }
          infm = 0; done_fm = 1; print; next
        }
      }
      if (infm) {
        key = $0; sub(/:.*/, "", key)
        if (key == "project") { print "project: " newslug; next }
        if (key == "project_path") {
          print
          if (has_label == "0" && !inserted) { print label_line(); inserted = 1 }
          next
        }
        if (key == "label") {
          val = $0; sub(/^[^:]*:[[:space:]]*/, "", val)
          if (val == "" || val == "null") { print label_line() } else { print }
          next
        }
        print; next
      }
      print
    }
  ' "$f" > "$tmp"
  mv -f "$tmp" "$f"
  entries_done=$((entries_done + 1))
done
if (( entries_done > 0 )); then
  printf 'Rewrote %d entry frontmatter block(s).\n' "$entries_done"
fi

# --- Validate ----------------------------------------------------------------
printf '\n'
if command -v clast-plumbing >/dev/null 2>&1; then
  CLAST_JOURNAL_DIR="$journal_dir" clast-plumbing doctor || {
    rc=$?
    printf '%s: doctor reported issues (exit %d) — review above.\n' "$PROG" "$rc" >&2
    exit "$rc"
  }
else
  printf '%s: clast-plumbing not on PATH; skipping post-migration doctor check.\n' "$PROG"
fi

printf '\nDone. To roll back: restore files from %s\n' "$backup_dir"
