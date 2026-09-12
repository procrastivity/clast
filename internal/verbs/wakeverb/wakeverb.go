// Package wakeverb implements the top-level `clast wake [--auto]` (SURFACE
// V9): the verb form of the wake shape. It is the third of the three
// shape verb forms to land (llm-verbs/step-06), following briefverb's
// (llm-verbs/step-04) and retroverb's (llm-verbs/step-05) own conventions:
// gather through the shape's plumbing document verb (internal/verbs/wake's
// own Run, never re-walking the journal or re-deriving its grouping), an
// empty-working-set stop before an llm.Client is ever constructed
// (mirrors briefverb's Result.Empty and retroverb's HasEntries), and a
// package-namespaced clasterr code for a request failure ("wake.*",
// mirroring brief.llm-request-failed/retro.llm-request-failed).
//
// New here: wake's flow (flows/wake.md §4/§6) writes — accept through
// `plumbing curate`, dismissal through `plumbing dismiss` — so this
// package composes directly on internal/verbs/curate's and
// internal/verbs/dismiss's own exported Run, the same "same-named
// plumbing document verb" reuse briefverb's own doc comment already
// argues for (V8), extended here to the write-path verbs the flow names
// explicitly.
//
// This file holds flows/wake.md §2 (read each session's context), §3
// (draft an entry), and the draft-parsing/entry-building steps a verb
// form needs to turn an LLM completion into a `curate`-acceptable
// entry.md document — kept separate from command.go's Cobra wiring and
// auto.go's/interactive.go's own §4/§5/§6 disposition loops.
package wakeverb

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/prompt"
	"github.com/procrastivity/clast/internal/source"
	breadcrumbsplumbing "github.com/procrastivity/clast/internal/verbs/breadcrumbs"
	"github.com/procrastivity/clast/internal/verbs/show"
)

// maxTurnChars is flows/wake.md §2's own stated cap: "show --transcript
// --max-turn-chars 2000" — a prompt-budget truncation the flow names
// explicitly, not this form's own tuning.
const maxTurnChars = 2000

// unprojectedLabel is how a projectless session's {{project}} placeholder
// reads in the rendered prompt — retroverb's own unprojectedLabel
// convention (a readable stand-in, never a raw sentinel leaking into the
// model's own input).
const unprojectedLabel = "(no project)"

// Draft implements flows/wake.md §2 (read the session's context: its
// transcript at --max-turn-chars 2000, plus yesterday's breadcrumbs for
// its frozen project — skipped entirely for a projectless session, §2's
// own stated case) and §3 (render the wake-draft prompt pair and call the
// endpoint), returning the assistant's completion verbatim. yesterday is
// resolved once by the caller (journal.ParseDay("yesterday", cutoff)) —
// the flow's own literal "--day yesterday" token, not relative to the
// session's own day.
func Draft(ctx context.Context, root string, cutoff journal.Cutoff, yesterday journal.Day, item journal.WalkItem, client *llm.Client) (string, error) {
	turns, err := show.Transcript(root, item, maxTurnChars)
	if err != nil {
		return "", err
	}

	var crumbs []journal.BreadcrumbEntry
	if item.Session.Project != nil {
		crumbs, err = breadcrumbsplumbing.Run(root, yesterday, cutoff, item.Session.Project.Slug, false)
		if err != nil {
			return "", err
		}
	}

	rendered, err := prompt.Render(prompt.WakeDraft, promptData(item, turns, crumbs))
	if err != nil {
		return "", clasterr.New("wake.prompt-unavailable",
			fmt.Sprintf("wake: resolving the wake-draft prompt pair: %v", err))
	}

	text, err := client.Complete(ctx, rendered.System, rendered.User)
	if err != nil {
		return "", clasterr.New("wake.llm-request-failed",
			fmt.Sprintf("wake: drafting session %s: %v", item.Key.DirName(), err))
	}
	return text, nil
}

// promptData maps one session onto prompt.WakeDraft's user-template
// placeholders (assets/prompts/wake-draft-user.md): {{project}},
// {{branch}}, {{start}}, {{end}}, {{msg_count}}, {{transcript}},
// {{breadcrumbs}} — step-02's own carried mapping notes (matter
// findings), reused here rather than re-derived. start/end render as
// UTC RFC3339 — retroverb's own summaryPromptData convention for a
// timestamp placeholder, kept for the same reason: deterministic,
// locale-independent text reaching the model.
func promptData(item journal.WalkItem, turns []source.Turn, crumbs []journal.BreadcrumbEntry) map[string]string {
	project := unprojectedLabel
	if item.Session.Project != nil {
		project = item.Session.Project.Slug
	}
	branch := item.Session.Branch
	if branch == "" {
		branch = "unknown"
	}
	msgCount := item.Session.Counts.User + item.Session.Counts.Assistant

	return map[string]string{
		"project":     project,
		"branch":      branch,
		"start":       item.Session.StartedAt.UTC().Format(time.RFC3339),
		"end":         item.Session.LastActiveAt.UTC().Format(time.RFC3339),
		"msg_count":   strconv.Itoa(msgCount),
		"transcript":  renderTranscript(turns),
		"breadcrumbs": renderBreadcrumbs(crumbs),
	}
}

// renderTranscript renders §2's capped turns as "[role] text" lines, one
// per turn — the same shape the old bash porcelain's jq filter produced
// for its own first_turns/last_turns (feel input only).
func renderTranscript(turns []source.Turn) string {
	if len(turns) == 0 {
		return "(no transcript)"
	}
	var b strings.Builder
	for i, t := range turns {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s] %s", t.Role, t.Text)
	}
	return b.String()
}

// renderBreadcrumbs renders yesterday's breadcrumbs as briefverb's own
// expected bullet shape ("HH:MM — text"), local time.
func renderBreadcrumbs(crumbs []journal.BreadcrumbEntry) string {
	if len(crumbs) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, c := range crumbs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s — %s", c.At.Local().Format("15:04"), c.Text)
	}
	return b.String()
}

// --- Draft parsing (wakeverb-owned finding: the wake-draft-system.md
// prompt produces a plain markdown document — "# Session: <title>"
// followed by "## " sections, closing with a blank line and "Suggested
// tags: tag1, tag2, tag3" — not an entry.md document with YAML
// frontmatter. `curate` requires the latter (internal/entry's Parse), so
// this form must extract the title and tags out of the completion and
// build a frontmatter block itself, exactly the extraction the old bash
// porcelain performed (_clast_wake_extract_title/_clast_wake_extract_tags/
// _clast_wake_strip_tags_trailer, main:lib/clast/clast-porcelain-
// subcommands/wake.bash, feel input) reimplemented against the entry.md
// shape rather than a --title/--tags/--body-stdin CLI call. ---

// titleLineRe matches the wake-draft-system.md prompt's own title line
// exactly: "# Session: <title>".
var titleLineRe = regexp.MustCompile(`(?m)^#\s*Session:\s*(.+?)\s*$`)

// tagsLineRe matches the prompt's closing "Suggested tags: a, b, c" line,
// case-insensitively (the old porcelain's own grep -i posture).
var tagsLineRe = regexp.MustCompile(`(?im)^Suggested tags:\s*(.*)$`)

// ParsedDraft is one LLM completion, split into what `curate` needs (a
// title, tags, and a body with neither the title heading nor the
// suggested-tags trailer) plus LengthCheck: the auto section's own
// measure (flows/wake.md Auto mode — "take the draft body without its
// suggested-tags trailer, trim surrounding whitespace" — deliberately
// NOT the same string as Body, since Body also drops the title heading
// while the auto length check does not).
type ParsedDraft struct {
	Title       string
	Tags        []string
	Body        string
	LengthCheck string
}

// ParseDraft parses raw per the convention above. fallbackTitle is used
// when the completion carries no "# Session: …" line at all (a
// wakeverb-owned finding: curate refuses an empty title outright
// (validation.entry-title), so a draft this form itself constructs must
// never hand it one — fallbackTitle is FallbackTitle's own session-keyed
// text, always non-empty).
func ParseDraft(raw, fallbackTitle string) ParsedDraft {
	withoutTags := stripTagsTrailer(raw)

	title := extractTitle(raw)
	if title == "" {
		title = fallbackTitle
	}

	body := strings.TrimSpace(stripTitleHeading(withoutTags))

	return ParsedDraft{
		Title:       title,
		Tags:        extractTags(raw),
		Body:        body,
		LengthCheck: strings.TrimSpace(withoutTags),
	}
}

// FallbackTitle is ParseDraft's fallback title for a completion that
// carries no "# Session: …" heading (a wakeverb-owned finding — the flow
// doesn't name one, since the flow assumes the prompt's own instructed
// shape always appears; this form still needs a non-empty title to hand
// `curate`, since the LLM's output is never trusted blindly).
func FallbackTitle(item journal.WalkItem) string {
	return "Session " + item.Key.DirName()
}

func extractTitle(raw string) string {
	m := titleLineRe.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	return m[1]
}

func extractTags(raw string) []string {
	m := tagsLineRe.FindStringSubmatch(raw)
	if m == nil {
		return nil
	}
	var tags []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			tags = append(tags, part)
		}
	}
	return tags
}

// stripTagsTrailer removes the "Suggested tags: …" line, and the single
// blank line the system prompt asks the model to place before it, from
// raw — returning everything else verbatim. A completion with no tags
// line at all is returned unchanged.
func stripTagsTrailer(raw string) string {
	lines := strings.Split(raw, "\n")
	idx := -1
	for i, line := range lines {
		if tagsLineRe.MatchString(line) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return raw
	}
	end := idx
	if idx > 0 && strings.TrimSpace(lines[idx-1]) == "" {
		end = idx - 1
	}
	return strings.Join(lines[:end], "\n")
}

// stripTitleHeading removes the "# Session: …" heading line, and one
// following blank line, from text — since the extracted title already
// rides in the entry.md frontmatter, repeating it as the body's own H1
// would duplicate it (curate/curate_test.go's own fixtures show a plain
// prose body with no such heading — this mirrors that shape). A
// completion with no title heading is returned unchanged.
func stripTitleHeading(text string) string {
	lines := strings.Split(text, "\n")
	idx := -1
	for i, line := range lines {
		if titleLineRe.MatchString(line) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return text
	}
	rest := lines[idx+1:]
	if len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	return strings.Join(rest, "\n")
}

// BelowThreshold is the Auto mode length guard (flows/wake.md): text (the
// draft body without its suggested-tags trailer, i.e. ParsedDraft.
// LengthCheck) is measured in runes, not bytes — "characters", the flow's
// own word — so a below-threshold call never miscounts multi-byte text. A
// minChars of 0 already never triggers (a rune count is never negative),
// so no separate "0 disables the guard" branch is needed — the same
// behavior the old porcelain's own CLAST_WAKE_AUTO_MIN_CHARS=0 case had,
// arrived at here for free.
func BelowThreshold(text string, minChars int) bool {
	return utf8.RuneCountInString(text) < minChars
}

// BuildEntryDocument builds a complete entry.md document (frontmatter,
// delimited by "---" lines, plus body) from fm and body — exactly the
// shape internal/entry.Parse (and so `curate`) accepts. Frontmatter is
// produced via yaml.Marshal over a structured entry.Frontmatter rather
// than raw string interpolation, which is what makes this form's own
// "invalid frontmatter" failure mode structurally unreachable: a title
// containing a colon, a quote, or any other YAML-special character is
// still valid input to yaml.Marshal, so curate's validate() can only ever
// see well-formed frontmatter from a wake-produced draft (a wakeverb-owned
// finding, recorded per the step brief's "draft-invalid-frontmatter
// handling" ask). The one condition curate.validate does still enforce —
// a non-empty title — is guaranteed by ParseDraft's own fallback, so it
// too never fires from this path.
func BuildEntryDocument(fm entry.Frontmatter, body string) ([]byte, error) {
	if fm.Tags == nil {
		fm.Tags = []string{}
	}
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("wake: marshalling entry frontmatter: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// PromoteSection implements flows/wake.md §5: fold one promoted item into
// body as its own section — "## Decision", "## Common issue", or
// "## Workflow" (kind carries the flow's own exact singular, capitalized
// spelling — the old porcelain's step-12 addition, carried here as feel
// input), followed by the item's title ("###") and its content, appended
// before the entry is written. Promoting multiple items in one session
// calls this repeatedly, each fold landing after the previous one.
func PromoteSection(body, kind, title, content string) string {
	var b strings.Builder
	trimmed := strings.TrimRight(body, "\n")
	b.WriteString(trimmed)
	if trimmed != "" {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "## %s\n\n### %s\n\n%s\n", kind, title, content)
	return strings.TrimRight(b.String(), "\n")
}
