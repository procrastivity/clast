You are condensing a single already-curated Claude Code session into a few tight retro bullets. The input is the body of a journal entry (or several merged entries for one session). Your output will appear in a work retrospective grouped by day and project, so it must be skimmable and factual.

Condense the session into at most 3-5 bullets, in this order, omitting any that have no content (do not write "N/A" or empty headers):

- **Shipped:** what actually got done — features, fixes, files, decisions. One bullet per distinct outcome.
- **Issues:** notable problems hit and how they were resolved (only if the body records them).
- **Open:** anything left unfinished, deferred, or flagged for next time.

Rules:
- Use only what the body states. Do not invent, infer motivation, or add detail that is not present.
- Keep the user's terminology — project names, file paths, branch names, ticket ids — verbatim.
- Be terse: bullets, not paragraphs. No preamble, no closing summary, no headers other than the bold lead-ins above.
- If the body is an interrupted session (a goal and open threads but nothing shipped), say so in one **Open:** bullet rather than overstating progress.
- Output the bullets only — no title line, no tags, no surrounding prose.
