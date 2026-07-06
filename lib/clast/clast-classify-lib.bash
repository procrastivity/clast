# clast-classify-lib.bash — deterministic session classification
#
# A "no-op" session is one Claude Code captured but that holds no real work:
# the user opened a session and only ran slash commands (`/clear`, `/model`,
# `/config`, …) then quit, or typed a prompt and quit before Claude replied.
# These are worthless to curate, so `wake` auto-dismisses them without ever
# calling the LLM (see docs/reference/plugin.md + the wake flows).
#
# The classification is two counts computed from the transcript JSONL:
#   user_msg_count      real user prompts — user-role messages that are not
#                       meta, are non-empty, and are NOT slash-command wrappers.
#   assistant_msg_count assistant-role messages (presence only; a tool-only
#                       reply with no text still counts as real work).
#
# A session is *substantive* iff assistant_msg_count > 0 — i.e. Claude actually
# replied. That single test captures both no-op shapes: empty / slash-command-
# only sessions (/clear, /model, /config) and sessions where the user typed but
# quit before any response — both have assistant_msg_count == 0. It is
# deliberately NOT gated on user_msg_count: a custom slash command (e.g.
# /review) leaves zero prose prompts yet drives real assistant work, and must
# be kept. user_msg_count is still cached for diagnostics (show, sessions).
#
# Counts are computed once at snapshot time and cached on the manifest line
# (clast-manifest-lib.bash), mirroring msg_count/first_ts/last_ts, so readers
# never re-open the transcript.
# shellcheck shell=bash

if [[ -n "${_CLAST_CLASSIFY_LIB_SOURCED:-}" ]]; then
  return 0
fi
_CLAST_CLASSIFY_LIB_SOURCED=1

# Slash-command / local-command marker set. A user-role message whose text
# contains any of these is Claude Code bookkeeping (e.g. `/clear`, `/model`),
# not a real prompt. Centralized here so the classifier and show.bash's
# first_prompt/last_prompt extraction share one definition.
CLAST_COMMAND_MARKER_RE='<command-name>|<command-message>|<command-args>|<local-command-stdout>|<local-command-stderr>|<local-command-caveat>'
export CLAST_COMMAND_MARKER_RE

# clast_session_msg_counts <transcript-path>
#   Print "<user_msg_count>\t<assistant_msg_count>" (tab-separated) for the
#   given JSONL transcript. Missing/unreadable file prints "0\t0".
#
#   Streaming (`reduce inputs`), not slurp: a multi-megabyte transcript is
#   read one line at a time, O(1) memory. `fromjson?` silently drops malformed
#   lines, matching the tolerance in clast_manifest_iterate.
clast_session_msg_counts() {
  local path="$1"
  if [[ -z "$path" || ! -r "$path" ]]; then
    printf '0\t0\n'
    return 0
  fi
  jq -n -R -r --arg cmd_re "$CLAST_COMMAND_MARKER_RE" '
    def role: (.message.role // .role // .type);
    def text_str:
      (.message.content // .content // "")
      | if type == "array" then (map(.text? // "") | join(" ")) else (. | tostring) end;
    reduce (inputs | fromjson?) as $l ({u: 0, a: 0};
      if ($l.isMeta // false) == true then .
      elif ($l | role) == "user" then
        ( ($l | text_str) as $t
          | if ($t | gsub("\\s"; "")) != "" and ($t | test($cmd_re) | not)
            then .u += 1 else . end )
      elif ($l | role) == "assistant" then .a += 1
      else . end
    ) | "\(.u)\t\(.a)"
  ' "$path" 2>/dev/null || printf '0\t0\n'
}
