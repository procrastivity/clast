# clast-porcelain-lib.bash — shared helpers for the `clast` porcelain.
#
# The porcelain is the LLM-aware, user-facing layer that sits on top of
# `clast-plumbing`. This file holds helpers reused by every porcelain
# subcommand: error/info plumbing, an OpenAI-compatible chat call, and the
# prompt-template loader. Pure bash + jq + curl.
#
# Subcommand files live in $CLAST_LIB/clast-porcelain-subcommands/<name>.bash
# and each defines a single function `clast_cmd_<name>`.
# shellcheck shell=bash

# --- Helpers -----------------------------------------------------------------

clast_porcelain_die() {
  printf 'clast: %s\n' "$1" >&2
  exit "${2:-1}"
}

clast_porcelain_warn() { printf 'clast: warning: %s\n' "$1" >&2; }
clast_porcelain_info() { printf '%s\n' "$1"; }

clast_porcelain_log_error() { printf 'clast: error: %s\n' "$1" >&2; }

# --- Version / usage ---------------------------------------------------------

# Reuses the plumbing's clast_version helper if available (loaded by sourcing
# clast-lib.bash). When the porcelain is invoked stand-alone we still want a
# version string, so fall back to reading package.json directly.
clast_porcelain_version() {
  if declare -F clast_version >/dev/null 2>&1; then
    clast_version
    return
  fi
  local pkg_root pkg
  pkg_root="${CLAST_LIB:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
  if [[ -f "$pkg_root/package.json" ]]; then
    pkg="$pkg_root/package.json"
  elif [[ -f "$pkg_root/../../package.json" ]]; then
    pkg="$pkg_root/../../package.json"
  else
    clast_porcelain_log_error "clast_porcelain_version: package.json not found"
    return 1
  fi
  jq -r '.version' "$pkg"
}

clast_porcelain_usage() {
  cat <<'EOF'
clast — Claude Code session porcelain (LLM-aware)

Usage:
  clast [GLOBAL FLAGS] <subcommand> [ARGS...]

Subcommands:
  wake          Interactive day curation (standalone equivalent of /wake)
  brief         Project briefing (standalone equivalent of /brief)
  retro         Model-condensed work retrospective by work day → project

Global flags:
  -h, --help    Print this usage and exit
      --version Print version and exit

Env (required by `wake` and `brief`):
  CLAST_LLM_BASE_URL  e.g. https://api.openai.com/v1
  CLAST_LLM_API_KEY   bearer token
  CLAST_LLM_MODEL     e.g. gpt-4o, llama3

The deterministic core (whereami / snapshot / sessions / entries / …)
lives in `clast-plumbing`. Run `clast-plumbing --help` for that surface.
EOF
}

# --- Preflight ---------------------------------------------------------------

# clast_porcelain_preflight_llm — verify curl, jq, and the LLM env vars exist.
# Subcommands that don't call the LLM (none right now) can skip this.
clast_porcelain_preflight_llm() {
  for tool in clast-plumbing curl jq; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      clast_porcelain_die "required tool not found: $tool"
    fi
  done

  local missing=0
  if [[ -z "${CLAST_LLM_BASE_URL:-}" ]]; then
    clast_porcelain_warn "CLAST_LLM_BASE_URL not set"; missing=1
  fi
  if [[ -z "${CLAST_LLM_API_KEY:-}" ]]; then
    clast_porcelain_warn "CLAST_LLM_API_KEY not set"; missing=1
  fi
  if [[ -z "${CLAST_LLM_MODEL:-}" ]]; then
    clast_porcelain_warn "CLAST_LLM_MODEL not set"; missing=1
  fi

  if (( missing )); then
    cat >&2 <<'EOF'

Set these env vars before running clast wake / clast brief:

  export CLAST_LLM_BASE_URL="https://api.openai.com/v1"
  export CLAST_LLM_API_KEY="sk-..."
  export CLAST_LLM_MODEL="gpt-4o"

Or for a local model (ollama, vllm, etc.):

  export CLAST_LLM_BASE_URL="http://localhost:11434/v1"
  export CLAST_LLM_API_KEY="unused"
  export CLAST_LLM_MODEL="llama3"
EOF
    exit 1
  fi
}

# --- LLM call ----------------------------------------------------------------

# clast_porcelain_llm_chat <system-msg> <user-msg>
#   POST a chat-completions request to $CLAST_LLM_BASE_URL/chat/completions
#   and echo the assistant's text on stdout. Returns nonzero on HTTP error or
#   empty response.
clast_porcelain_llm_chat() {
  local system_msg="$1"
  local user_msg="$2"

  # Build and send the request without ever passing the (possibly hundreds of
  # KB) prompt through argv: a single argument above MAX_ARG_STRLEN (128KB on
  # Linux) fails with "Argument list too long", even well under total ARG_MAX.
  # jq reads the strings via --rawfile (process substitution; printf is a
  # builtin with no argv limit) and curl reads the body from a file with @.
  local payload_file
  payload_file="$(mktemp)" || { clast_porcelain_warn "failed to create temp file"; return 1; }
  if ! jq -cn \
      --arg model "$CLAST_LLM_MODEL" \
      --rawfile system <(printf '%s' "$system_msg") \
      --rawfile user <(printf '%s' "$user_msg") \
      '{
        model: $model,
        messages: [
          {role: "system", content: $system},
          {role: "user", content: $user}
        ],
        temperature: 0.3
      }' >"$payload_file"; then
    rm -f "$payload_file"
    clast_porcelain_warn "failed to build LLM request payload"
    return 1
  fi

  local response http_code body
  response="$(curl -s -w '\n%{http_code}' \
    "${CLAST_LLM_BASE_URL}/chat/completions" \
    -H "Authorization: Bearer $CLAST_LLM_API_KEY" \
    -H "Content-Type: application/json" \
    --data-binary @"$payload_file" 2>&1)" || true
  rm -f "$payload_file"

  http_code="$(tail -n1 <<<"$response")"
  body="$(sed '$d' <<<"$response")"

  if [[ "$http_code" != "200" ]]; then
    clast_porcelain_warn "LLM API returned HTTP $http_code"
    if [[ -n "$body" ]]; then
      local err_msg
      err_msg="$(jq -r '.error.message // .error // .' <<<"$body" 2>/dev/null || echo "$body")"
      clast_porcelain_warn "$err_msg"
    fi
    return 1
  fi

  local content
  content="$(jq -r '.choices[0].message.content // empty' <<<"$body" 2>/dev/null)" || {
    clast_porcelain_warn "failed to parse LLM response"
    return 1
  }

  if [[ -z "$content" ]]; then
    clast_porcelain_warn "LLM returned empty content"
    return 1
  fi

  printf '%s' "$content"
}

# --- Prompt templates --------------------------------------------------------

# clast_porcelain_resolve_prompt_dir — echo the prompts dir, search order:
#   1. $CLAST_LIB/prompts  (in-repo or installed alongside the lib)
#   2. /usr/local/lib/clast/prompts
#   3. $HOME/.local/lib/clast/prompts
clast_porcelain_resolve_prompt_dir() {
  if [[ -n "${CLAST_LIB:-}" && -d "$CLAST_LIB/prompts" ]]; then
    printf '%s' "$CLAST_LIB/prompts"
    return
  fi
  local installed
  for installed in /usr/local/lib/clast/prompts "$HOME/.local/lib/clast/prompts"; do
    if [[ -d "$installed" ]]; then
      printf '%s' "$installed"
      return
    fi
  done
  clast_porcelain_die "cannot find prompts directory (checked \$CLAST_LIB/prompts and install paths)"
}

# clast_porcelain_load_system_prompt <basename>
#   Read the named system prompt (without extension) from the resolved
#   prompts dir, e.g. clast_porcelain_load_system_prompt brief-system.
clast_porcelain_load_system_prompt() {
  local name="$1"
  local prompt_dir file
  prompt_dir="$(clast_porcelain_resolve_prompt_dir)"
  file="$prompt_dir/${name}.md"
  if [[ ! -r "$file" ]]; then
    clast_porcelain_die "system prompt not found: $file"
  fi
  cat "$file"
}

# clast_porcelain_user_prompt_file <basename>
#   Echo the absolute path to the named user-prompt template, or empty string
#   if missing. Callers do their own placeholder substitution.
clast_porcelain_user_prompt_file() {
  local name="$1"
  local prompt_dir file
  prompt_dir="$(clast_porcelain_resolve_prompt_dir)"
  file="$prompt_dir/${name}.md"
  if [[ -r "$file" ]]; then
    printf '%s' "$file"
  fi
}
