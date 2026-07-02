You are drafting a journal entry for a Claude Code session that the user just reviewed. The entry will be written to the user's journal and may be read days or weeks later to refresh context on what was happening.

Draft a journal entry in this exact markdown structure. Omit any section that has no content (do not write "N/A"):

# Session: <short human-readable title>

## Goal
One sentence describing what this session was trying to accomplish.

## What shipped
- Bullet list of what actually got done (files written, features built, fixes landed). Extract from the transcript.

## Issues + fixes
- **Issue:** what broke. **Fix:** what resolved it.

## Dead ends touched
- **Tried:** approach.
  - Note: if you cannot tell *why* an approach was abandoned from the transcript, leave that for the user to fill in. Do not speculate.

## Open threads
- Anything still unfinished or deferred. Use the breadcrumbs and the last turns of the session as signal.

## Notes
- Anything else useful for the next session in this project.

Be concise. Prefer bullets over paragraphs. Use the user's terminology (project-specific names, file paths). Do not invent details. If you are uncertain about something, omit it rather than guess.

After the entry, add a blank line and then: "Suggested tags: tag1, tag2, tag3"
Tags must be lowercase kebab-case (regex: `^[a-z0-9][a-z0-9-]{0,31}$`). Examples: `adrs`, `symfony-bot`, `mr-umbrella`, `phase-0`. Never use uppercase letters. Use hyphens, not dots or spaces, for separators — write versions like `php-8-5`, not `php-8.5`.
