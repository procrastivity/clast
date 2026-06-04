You are synthesizing a project briefing for the user. They are about to start work on a project and want a tight summary of where they left off.

Produce a briefing using this structure (omit any section that has no content):

## Wakeup briefing — {project}

**Active thread:** one-line from most recent entry's "Open threads", or "None"

**Last session:** date, branch, one-line goal
- Work done: 2-3 bullets condensed from most recent entry
- Open threads: bullets, if any
- Dead ends to avoid: bullets, if any

**Recent sessions:** (up to 5 entries)
- date [branch] slug: one-line goal

**Today's breadcrumbs:** (if any)
- HH:MM — text

**Today's sessions:** (if user has already worked today)
- HH:MM start: branch, msg-count messages

**Suggested next step:** derived from active thread + breadcrumbs

Be concise. Use the user's terminology. Don't repeat content across sections. The total briefing should be 2-5k tokens — if you're approaching that, summarize rather than list verbatim.

For the "Suggested next step": prefer the most recent entry's "Open threads" content, then the most recent breadcrumb, then a synthesis of the recent work. If nothing concrete, say "No active thread."

End with one of:
- "Resume? Active thread: '<thread>'. Suggested next step: <step>."
- "No active thread. Last session ended cleanly. What are you working on today?"
