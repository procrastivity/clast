# clast-registry-lib.bash — projects.json read/write/resolve
#
# Sourced after clast-lib.bash and clast-decode-lib.bash. The registry is
# an append-only JSONL log at $(clast_journal_dir)/projects.json (the
# ".json" name is historical — semantically it is JSONL). See
# docs/cli-contract.md#registry-line-in-projectsjson for the on-disk
# schema and docs/cli-contract.md#clast-registry for the resolution rules.
# shellcheck shell=bash
# shellcheck source=lib/clast/clast-lib.bash
# shellcheck source=lib/clast/clast-decode-lib.bash

if [[ -n "${_CLAST_REGISTRY_LIB_SOURCED:-}" ]]; then
  return 0
fi
_CLAST_REGISTRY_LIB_SOURCED=1

# clast_registry_path — single chokepoint so CLAST_JOURNAL_DIR override
# redirects every read/write.
clast_registry_path() {
  printf '%s\n' "$(clast_journal_dir)/projects.json"
}

# clast_registry_list_json — print the registry as a compact JSON array.
# Missing file → []. Malformed lines are silently dropped (fromjson?).
clast_registry_list_json() {
  local path
  path="$(clast_registry_path)"
  if [[ ! -f "$path" ]]; then
    printf '[]\n'
    return 0
  fi
  jq -cR 'fromjson?' "$path" \
    | jq -cs 'map(select(type == "object" and .path != null))'
}

# clast_registry_match_remote <remote>
#   Print slug of first line whose .remote == <remote>; return 1 if none.
#   Empty <remote> → return 1 (does not match unset remotes).
clast_registry_match_remote() {
  local remote="${1:-}"
  if [[ -z "$remote" ]]; then
    return 1
  fi
  local arr slug
  arr="$(clast_registry_list_json)"
  slug="$(jq -r --arg r "$remote" 'map(select(.remote == $r)) | .[0].slug // empty' <<<"$arr")"
  if [[ -z "$slug" ]]; then
    return 1
  fi
  printf '%s\n' "$slug"
}

# clast_registry_resolve <path-or-segment>
#   Print slug on hit, return 1 silently on miss.
clast_registry_resolve() {
  local input="${1:-}"
  if [[ -z "$input" ]]; then
    return 1
  fi

  local arr
  arr="$(clast_registry_list_json)"

  # Segment input: starts with `-`. Decode (possibly ambiguous) then
  # resolve as a path.
  if [[ "$input" == -* ]]; then
    local -a candidates=()
    mapfile -t candidates < <(clast_decode_candidates "$input")
    local c slug
    # Prefer registry-matching candidate over filesystem-resolving one.
    for c in "${candidates[@]}"; do
      slug="$(_clast_registry_lookup_path "$c" "$arr")" || continue
      if [[ -n "$slug" ]]; then
        printf '%s\n' "$slug"
        return 0
      fi
    done
    return 1
  fi

  # Path input: canonicalize. `realpath -m` resolves non-existent paths
  # (whereami inside a non-git tmpdir). BSD realpath lacks -m; the nix
  # dev shell pulls in GNU coreutils.
  local canon
  canon="$(realpath -m "$input" 2>/dev/null || printf '%s' "$input")"
  local slug
  slug="$(_clast_registry_lookup_path "$canon" "$arr")" || return 1
  if [[ -z "$slug" ]]; then
    return 1
  fi
  printf '%s\n' "$slug"
}

# _clast_registry_lookup_path <path> <registry-json-array>
#   First match wins: scan .path, then .aliases[]. Print slug or empty.
_clast_registry_lookup_path() {
  local p="$1" arr="$2"
  jq -r --arg p "$p" '
    (map(select(.path == $p)) | .[0].slug)
    // (map(select(.aliases? // [] | index($p) != null)) | .[0].slug)
    // empty
  ' <<<"$arr"
}

# clast_registry_add <path> [--slug NAME] [--remote URL]
#   Append a single JSONL line. Default slug = basename(path). Default
#   remote = `git -C <path> remote get-url origin` (or absent). If the
#   resolved remote matches an existing entry's remote, append a new line
#   carrying that entry's slug and rolling its known paths into aliases.
#   Print the appended JSON on stdout. Exit 2 on bad args, 1 on write fail.
clast_registry_add() {
  local path="" slug="" remote="" remote_explicit=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --slug)
        if [[ $# -lt 2 ]]; then
          clast_log_error "clast_registry_add: --slug requires a value"
          return 2
        fi
        slug="$2"; shift 2 ;;
      --slug=*)  slug="${1#*=}"; shift ;;
      --remote)
        if [[ $# -lt 2 ]]; then
          clast_log_error "clast_registry_add: --remote requires a value"
          return 2
        fi
        remote="$2"; remote_explicit=1; shift 2 ;;
      --remote=*) remote="${1#*=}"; remote_explicit=1; shift ;;
      -*)
        clast_log_error "clast_registry_add: unknown flag '$1'"
        return 2
        ;;
      *)
        if [[ -n "$path" ]]; then
          clast_log_error "clast_registry_add: unexpected positional '$1'"
          return 2
        fi
        path="$1"; shift
        ;;
    esac
  done

  # Reject empty / whitespace-only paths.
  local trimmed="${path//[[:space:]]/}"
  if [[ -z "$trimmed" ]]; then
    clast_log_error "clast_registry_add: <path> is required"
    return 2
  fi

  # Canonicalize. -m tolerates non-existent paths.
  local canon
  canon="$(realpath -m "$path" 2>/dev/null || printf '%s' "$path")"

  # Resolve default remote if not given. Tolerate missing git / non-repo.
  if [[ $remote_explicit -eq 0 ]]; then
    if command -v git >/dev/null 2>&1 && [[ -d "$canon" ]]; then
      remote="$(git -C "$canon" remote get-url origin 2>/dev/null || true)"
    fi
  fi

  # Default slug from basename.
  if [[ -z "$slug" ]]; then
    slug="$(basename "$canon")"
  fi

  # Aliases roll-up only when remote matched an existing entry.
  local arr matched_slug
  arr="$(clast_registry_list_json)"
  local aliases_json='[]'
  if [[ -n "$remote" ]]; then
    matched_slug="$(jq -r --arg r "$remote" 'map(select(.remote == $r)) | .[0].slug // empty' <<<"$arr")"
    if [[ -n "$matched_slug" ]]; then
      slug="$matched_slug"
      # Collect every known path for this slug (excluding the new one),
      # plus their existing aliases.
      aliases_json="$(jq -c --arg s "$slug" --arg p "$canon" '
        [ .[] | select(.slug == $s) | [.path] + (.aliases // []) ]
        | add // []
        | map(select(. != $p))
        | unique
      ' <<<"$arr")"
    fi
  fi

  local journal_dir
  journal_dir="$(clast_journal_dir)"
  if ! mkdir -p "$journal_dir"; then
    clast_log_error "clast_registry_add: failed to create '$journal_dir'"
    return 1
  fi

  local first_seen
  first_seen="$(clast_today)"

  local line
  line="$(jq -c -n \
    --arg path "$canon" \
    --arg slug "$slug" \
    --arg remote "$remote" \
    --arg first_seen "$first_seen" \
    --argjson aliases "$aliases_json" \
    '{path: $path, slug: $slug, remote: $remote, first_seen: $first_seen}
     | with_entries(select(.value != null and .value != ""))
     | . + {aliases: $aliases}')" || {
    clast_log_error "clast_registry_add: jq failed to build registry line"
    return 1
  }

  local registry_path
  registry_path="$(clast_registry_path)"
  if ! printf '%s\n' "$line" >>"$registry_path" 2>/dev/null; then
    clast_log_error "clast_registry_add: failed to append to '$registry_path'"
    return 1
  fi

  printf '%s\n' "$line"
}

# clast_registry_remove <slug>
#   Whole-file rewrite via clast_atomic_write. Return 0 only when ≥ 1
#   line was removed; 1 otherwise. Never touches files other than
#   projects.json.
clast_registry_remove() {
  local slug="${1:-}"
  if [[ -z "$slug" ]]; then
    clast_log_error "clast_registry_remove: <slug> is required"
    return 2
  fi
  local registry_path
  registry_path="$(clast_registry_path)"
  if [[ ! -f "$registry_path" ]]; then
    return 1
  fi

  local before after kept
  before="$(jq -cR 'fromjson? | select(.path != null)' "$registry_path" | grep -c . || true)"
  kept="$(jq -cR --arg s "$slug" 'fromjson? | select(.path != null and .slug != $s)' "$registry_path")"
  after="$(printf '%s\n' "$kept" | grep -c . || true)"
  if (( before == after )); then
    return 1
  fi

  # Trailing newline only when content is non-empty.
  local content=""
  if [[ -n "$kept" ]]; then
    content="$kept"$'\n'
  fi
  if ! clast_atomic_write "$registry_path" "$content"; then
    return 1
  fi
  return 0
}
