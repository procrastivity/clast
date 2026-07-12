# clast undismiss — restore session(s) dismissed during `clast wake`.
#
# A convenience verb over `clast-plumbing sessions undismiss`: `clast wake`
# now prints each session's id, so recovering an accidental [d] is a direct
# `clast undismiss <id>` without dropping down to the plumbing surface.
#
# Intentionally CLI-only: no skills/undismiss/SKILL.md. This is a thin,
# model-free passthrough to `clast-plumbing sessions undismiss` (see below)
# with no synthesis/judgment step an LLM porcelain would add. See
# .wip/initiatives/porcelain-parity/BRIEF.md "Confirmed decisions" for the
# full decision record.
# shellcheck shell=bash

clast_cmd_undismiss() {
  case "${1:-}" in
    -h|--help)
      cat <<'EOF'
Usage: clast undismiss <session-id> [<session-id>...]

Restore session(s) previously dismissed (e.g. an accidental [d] in
`clast wake`). Session ids are shown in the `clast wake` review header.
EOF
      return 0
      ;;
  esac

  if ! command -v clast-plumbing >/dev/null 2>&1; then
    clast_porcelain_die "required tool not found: clast-plumbing"
  fi

  # Thin passthrough: plumbing owns UUID validation and JSON/human output.
  clast-plumbing sessions undismiss "$@"
}
