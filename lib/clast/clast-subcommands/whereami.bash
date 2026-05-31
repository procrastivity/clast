# clast-subcommands/whereami.bash — `clast whereami`.
#
# Reports what clast sees about the current directory: pwd, git root,
# registry status (stubbed until step 05), last snapshot (stubbed until
# step 04), and effective journal/projects/cutoff/machine settings.
# shellcheck shell=bash

clast_cmd_whereami() {
  # Subcommand-level flag parsing. The dispatcher may have already set
  # CLAST_JSON=1, but accept `--json` here too for ergonomics.
  local json="${CLAST_JSON:-}"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --json) json=1; shift ;;
      -h|--help)
        cat <<'EOF'
Usage: clast whereami [--json]

Show what clast sees about the current directory.
EOF
        return 0
        ;;
      *)
        clast_log_error "whereami: unexpected arg '$1'"
        return 2
        ;;
    esac
  done

  local pwd_v git_root remote registered slug last_snapshot
  local journal_dir projects_dir day_cutoff machine

  pwd_v="$PWD"

  git_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"

  remote=""
  if [[ -n "$git_root" ]]; then
    remote="$(git -C "$git_root" config --get remote.origin.url 2>/dev/null || true)"
  fi

  # TODO(step-05): consult the registry lib to resolve slug from git_root
  # (or pwd) and set registered=yes when found.
  registered="no"
  slug=""

  # TODO(step-04): look up the most recent manifest entry for the
  # resolved project and report its captured_at timestamp.
  last_snapshot=""

  journal_dir="$(clast_journal_dir)"
  projects_dir="$(clast_projects_dir)"
  day_cutoff="${CLAST_DAY_CUTOFF:-04:00}"

  # Short hostname on both macOS and Linux. `hostname -s` works on both
  # but is missing on some minimal images; fall back to plain `hostname`
  # with any domain suffix stripped.
  machine="$(hostname -s 2>/dev/null || hostname | cut -d. -f1)"

  if [[ -n "$json" ]]; then
    jq -n \
      --arg pwd "$pwd_v" \
      --arg git_root "$git_root" \
      --arg registered "$registered" \
      --arg slug "$slug" \
      --arg remote "$remote" \
      --arg last_snapshot "$last_snapshot" \
      --arg journal_dir "$journal_dir" \
      --arg projects_dir "$projects_dir" \
      --arg day_cutoff "$day_cutoff" \
      --arg machine "$machine" \
      '{
        pwd:           $pwd,
        git_root:      (if $git_root      == "" then null else $git_root      end),
        registered:    $registered,
        slug:          (if $slug          == "" then null else $slug          end),
        remote:        (if $remote        == "" then null else $remote        end),
        last_snapshot: (if $last_snapshot == "" then null else $last_snapshot end),
        journal_dir:   $journal_dir,
        projects_dir:  $projects_dir,
        day_cutoff:    $day_cutoff,
        machine:       $machine
      }'
    return 0
  fi

  # Human output: labeled key/value block, em-dash for nulls.
  local dash="—"
  _clast_whereami_row() {
    local label="$1" value="$2"
    if [[ -z "$value" ]]; then value="$dash"; fi
    printf '%-15s %s\n' "$label:" "$value"
  }

  _clast_whereami_row "pwd"           "$pwd_v"
  _clast_whereami_row "git_root"      "$git_root"
  _clast_whereami_row "registered"    "$registered"
  _clast_whereami_row "slug"          "$slug"
  _clast_whereami_row "remote"        "$remote"
  _clast_whereami_row "last_snapshot" "$last_snapshot"
  _clast_whereami_row "journal_dir"   "$journal_dir"
  _clast_whereami_row "projects_dir"  "$projects_dir"
  _clast_whereami_row "day_cutoff"    "$day_cutoff"
  _clast_whereami_row "machine"       "$machine"
}
