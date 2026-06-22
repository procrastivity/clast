# clast-subcommands/registry.bash — `clast registry list|add|resolve|remove`.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-registry-lib.bash

_clast_registry_usage() {
  cat <<'EOF'
Usage:
  clast registry list [--json]
  clast registry add <path> [--slug NAME] [--remote URL] [--json]
  clast registry resolve <path-or-segment> [--json]
  clast registry remove <slug> [--json]
EOF
}

clast_cmd_registry() {
  if [[ $# -eq 0 ]]; then
    _clast_registry_usage >&2
    return 2
  fi

  local op="$1"; shift
  case "$op" in
    list)    _clast_registry_op_list "$@" ;;
    add)     _clast_registry_op_add "$@" ;;
    resolve) _clast_registry_op_resolve "$@" ;;
    remove)  _clast_registry_op_remove "$@" ;;
    -h|--help)
      _clast_registry_usage
      return 0
      ;;
    *)
      clast_log_error "registry: unknown op '$op'"
      _clast_registry_usage >&2
      return 2
      ;;
  esac
}

_clast_registry_op_list() {
  local json="${CLAST_JSON:-}"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --json) json=1; shift ;;
      -h|--help)
        cat <<'EOF'
Usage: clast registry list [--json]
EOF
        return 0
        ;;
      *)
        clast_log_error "registry list: unexpected arg '$1'"
        return 2
        ;;
    esac
  done

  local arr
  arr="$(clast_registry_list_json)"

  if [[ -n "$json" ]]; then
    printf '%s\n' "$arr"
    return 0
  fi

  printf '%-17s %-13s %-33s %-43s %s\n' slug label path remote aliases
  local n i slug label path remote aliases
  n="$(jq 'length' <<<"$arr")"
  for (( i = 0; i < n; i++ )); do
    slug="$(jq -r ".[$i].slug // \"\"" <<<"$arr")"
    label="$(jq -r ".[$i].label // \"\" | if . == \"\" then \"(none)\" else . end" <<<"$arr")"
    path="$(jq -r ".[$i].path // \"\"" <<<"$arr")"
    remote="$(jq -r ".[$i].remote // \"\"" <<<"$arr")"
    aliases="$(jq -r ".[$i].aliases // [] | if length == 0 then \"(none)\" else join(\",\") end" <<<"$arr")"
    printf '%-17s %-13s %-33s %-43s %s\n' "$slug" "$label" "$path" "$remote" "$aliases"
  done
}

_clast_registry_op_add() {
  local json="${CLAST_JSON:-}"
  local -a passthrough=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --json) json=1; shift ;;
      -h|--help)
        cat <<'EOF'
Usage: clast registry add <path> [--slug NAME] [--label NAME] [--remote URL] [--json]
EOF
        return 0
        ;;
      # TODO(v1.1): interactive --slug prompt when stdin is a TTY.
      --slug|--label|--remote)
        if [[ $# -lt 2 ]]; then
          clast_log_error "registry add: $1 requires a value"
          return 2
        fi
        passthrough+=("$1" "$2"); shift 2 ;;
      --slug=*|--label=*|--remote=*)
        passthrough+=("$1"); shift ;;
      *)
        passthrough+=("$1"); shift ;;
    esac
  done

  local line rc=0
  line="$(clast_registry_add "${passthrough[@]}")" || rc=$?
  if (( rc != 0 )); then
    return "$rc"
  fi

  if [[ -n "$json" ]]; then
    printf '%s\n' "$line"
  else
    local slug label path
    slug="$(jq -r '.slug' <<<"$line")"
    label="$(jq -r '.label // ""' <<<"$line")"
    path="$(jq -r '.path' <<<"$line")"
    if [[ -n "$label" ]]; then
      printf 'registered %s (%s) → %s\n' "$slug" "$label" "$path"
    else
      printf 'registered %s → %s\n' "$slug" "$path"
    fi
  fi
}

_clast_registry_op_resolve() {
  local json="${CLAST_JSON:-}"
  local input=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --json) json=1; shift ;;
      -h|--help)
        cat <<'EOF'
Usage: clast registry resolve <path-or-segment> [--json]

Prints the resolved slug. With --json, also includes the directory's
`label` when the matched line has one.
EOF
        return 0
        ;;
      *)
        if [[ -n "$input" ]]; then
          clast_log_error "registry resolve: unexpected arg '$1'"
          return 2
        fi
        input="$1"; shift
        ;;
    esac
  done

  if [[ -z "$input" ]]; then
    clast_log_error "registry resolve: <path-or-segment> is required"
    return 2
  fi

  # Resolve to the specific line so --json can surface the per-directory
  # label (not just the slug). Human output stays slug-only.
  local line
  if line="$(clast_registry_line_for_path "$input")" && [[ -n "$line" ]]; then
    local slug label
    slug="$(jq -r '.slug // empty' <<<"$line")"
    label="$(jq -r '.label // empty' <<<"$line")"
    if [[ -n "$json" ]]; then
      jq -cn --arg slug "$slug" --arg label "$label" \
        '{slug: $slug} + (if $label == "" then {} else {label: $label} end)'
    else
      printf '%s\n' "$slug"
    fi
    return 0
  fi

  if [[ -n "$json" ]]; then
    printf '%s\n' '{"error":"not registered"}'
  else
    clast_log_error "not registered"
  fi
  return 1
}

_clast_registry_op_remove() {
  local json="${CLAST_JSON:-}"
  local slug=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --json) json=1; shift ;;
      -h|--help)
        cat <<'EOF'
Usage: clast registry remove <slug> [--json]
EOF
        return 0
        ;;
      *)
        if [[ -n "$slug" ]]; then
          clast_log_error "registry remove: unexpected arg '$1'"
          return 2
        fi
        slug="$1"; shift
        ;;
    esac
  done

  if [[ -z "$slug" ]]; then
    clast_log_error "registry remove: <slug> is required"
    return 2
  fi

  if clast_registry_remove "$slug"; then
    if [[ -n "$json" ]]; then
      jq -cn --arg slug "$slug" '{removed: $slug}'
    else
      printf 'unregistered %s\n' "$slug"
    fi
    return 0
  fi

  if [[ -n "$json" ]]; then
    printf '%s\n' '{"error":"not registered"}'
  else
    clast_log_error "not registered"
  fi
  return 1
}
