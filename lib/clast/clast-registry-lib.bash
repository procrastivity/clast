# clast-registry-lib.bash — projects.json read/write/resolve
#
# Sourced after clast-lib.bash and clast-decode-lib.bash. The registry is
# an append-only JSONL log at $(clast_journal_dir)/projects.json (the
# ".json" name is historical — semantically it is JSONL). See
# docs/reference/cli.md#registry-line-in-projectsjson for the on-disk
# schema and docs/reference/cli.md#clast-registry for the resolution rules.
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

  # Segment input: starts with `-`. The candidate set is the raw segment
  # (handles segments registered as-is) followed by every dash-decoded
  # filesystem path. Test them all in ONE jq pass — first candidate in
  # order to match a registry .path/.aliases wins — instead of forking one
  # jq per candidate, which for deep paths meant ~hundreds/thousands of
  # forks per resolve (the dominant cost of `clast wake` startup).
  if [[ "$input" == -* ]]; then
    local -a candidates=("$input")
    local -a decoded=()
    mapfile -t decoded < <(clast_decode_candidates "$input")
    candidates+=("${decoded[@]}")
    local cands_json slug
    cands_json="$(printf '%s\n' "${candidates[@]}" | jq -Rn '[inputs]')"
    slug="$(_clast_registry_lookup_paths "$cands_json" "$arr" 2>/dev/null)" || true
    if [[ -n "$slug" ]]; then
      printf '%s\n' "$slug"
      return 0
    fi
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

# _clast_registry_lookup_paths <candidates-json-array> <registry-json-array>
#   Batched form of _clast_registry_lookup_path: given an ordered JSON array
#   of candidate paths, return the slug for the FIRST candidate (in order)
#   that matches a registry entry's .path or .aliases — path before alias,
#   matching the single-path helper. Print slug or empty. One jq pass for
#   the whole candidate set.
_clast_registry_lookup_paths() {
  local cands_json="$1" arr="$2"
  jq -r --argjson cands "$cands_json" '
    . as $arr
    | first(
        $cands[] as $c
        | ( ($arr | map(select(.path == $c)) | .[0].slug)
            // ($arr | map(select((.aliases? // []) | index($c) != null)) | .[0].slug) )
        | select(. != null)
      ) // empty
  ' <<<"$arr"
}

# _clast_registry_slugify <string>
#   Lowercase, map non-[a-z0-9] runs to single dashes, trim, cap at 32.
#   Used for auto-derived labels (e.g. a parent-directory basename).
_clast_registry_slugify() {
  local s
  s="$(printf '%s' "${1,,}" | sed 's/[^a-z0-9]/-/g; s/--*/-/g; s/^-//; s/-$//')"
  s="${s:0:32}"
  s="${s%-}"
  printf '%s' "$s"
}

# clast_registry_add <path> [--slug NAME] [--label NAME] [--remote URL]
#   Append a single JSONL line. The remote is a *grouping hint*, not an
#   identity: when it matches an existing entry's remote, an absent --slug
#   adopts that entry's slug (so clones of one repo share a project), but an
#   explicit --slug always wins (a divergent one warns). Default slug =
#   basename(path). Default label = basename(dirname(path)). Default remote
#   = `git -C <path> remote get-url origin` (or absent). New lines carry
#   `aliases: []` — sibling paths are no longer rolled up; a shared slug
#   across distinct .path lines is the supported multi-directory shape.
#   Print the appended JSON on stdout. Exit 2 on bad args, 1 on write fail.
clast_registry_add() {
  local path="" slug="" slug_explicit=0 label_raw="" label_explicit=0
  local remote="" remote_explicit=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --slug)
        if [[ $# -lt 2 ]]; then
          clast_log_error "clast_registry_add: --slug requires a value"
          return 2
        fi
        slug="$2"; slug_explicit=1; shift 2 ;;
      --slug=*)  slug="${1#*=}"; slug_explicit=1; shift ;;
      --label)
        if [[ $# -lt 2 ]]; then
          clast_log_error "clast_registry_add: --label requires a value"
          return 2
        fi
        label_raw="$2"; label_explicit=1; shift 2 ;;
      --label=*) label_raw="${1#*=}"; label_explicit=1; shift ;;
      --remote)
        if [[ $# -lt 2 ]]; then
          clast_log_error "clast_registry_add: --remote requires a value"
          return 2
        fi
        remote="$2"; remote_explicit=1; shift 2 ;;
      --remote=*) remote="${1#*=}"; remote_explicit=1; shift ;;
      --)
        shift; break ;;
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

  # Consume remaining positional after --
  if [[ $# -gt 0 ]]; then
    if [[ -n "$path" ]]; then
      clast_log_error "clast_registry_add: unexpected positional '$1'"
      return 2
    fi
    path="$1"; shift
  fi
  if [[ $# -gt 0 ]]; then
    clast_log_error "clast_registry_add: unexpected positional '$1'"
    return 2
  fi

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

  # Resolve the slug against any existing entry sharing this remote. The
  # remote groups clones of one repo under a single logical project, but it
  # is only a hint: an explicit --slug always wins. A divergent explicit
  # slug warns (you are splitting the remote into two projects on purpose);
  # the default slug silently adopts the match.
  local matched_slug=""
  if [[ -n "$remote" ]]; then
    matched_slug="$(clast_registry_match_remote "$remote" 2>/dev/null || true)"
  fi

  if (( slug_explicit == 1 )); then
    if [[ -n "$matched_slug" && "$matched_slug" != "$slug" ]]; then
      clast_log_warn "registry: remote '$remote' is already registered under slug '$matched_slug'; registering '$canon' under requested slug '$slug' as a separate project (pass --slug '$matched_slug' to group them)"
    fi
  elif [[ -n "$matched_slug" ]]; then
    slug="$matched_slug"
    clast_log_info "registry: grouping '$canon' under existing slug '$slug' (shared remote; pass --slug to override)"
  else
    slug="$(basename "$canon")"
  fi

  # Per-directory label. An explicit --label is lowercased and validated;
  # otherwise derive from the parent directory's basename (e.g.
  # ~/Workspaces/performance/xesapps → "performance"). The label only
  # distinguishes clones of one slug; a single-directory project never
  # surfaces it, so a default like "code" is harmless.
  local label=""
  if (( label_explicit == 1 )); then
    label="${label_raw,,}"
    if [[ ! "$label" =~ ^[a-z0-9][a-z0-9-]{0,31}$ ]]; then
      clast_log_error "clast_registry_add: invalid --label '$label_raw' (expected [a-z0-9][a-z0-9-]{0,31})"
      return 2
    fi
  else
    label="$(_clast_registry_slugify "$(basename "$(dirname "$canon")")")"
  fi

  # Sibling paths are no longer rolled into aliases: each directory is its
  # own line keyed by .path, and a shared slug is the supported way to span
  # directories. `aliases` stays present (reserved for genuine alternate
  # paths of the same checkout) but empty.
  local aliases_json='[]'

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
    --arg label "$label" \
    --arg remote "$remote" \
    --arg first_seen "$first_seen" \
    --argjson aliases "$aliases_json" \
    '{path: $path, slug: $slug, label: $label, remote: $remote, first_seen: $first_seen}
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
