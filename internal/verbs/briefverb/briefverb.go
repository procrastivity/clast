// Package briefverb implements the top-level `clast brief [<project>]`
// (SURFACE V9/V10/V26–V29): the verb form of the brief shape. It is the
// first of the three shape verb forms to land (llm-verbs/step-04), so it
// sets this Matter's own conventions for wake/retro's own verb forms to
// follow: gather through the shape's plumbing document verb (never
// re-walking the journal), stop cold on an empty payload before an LLM
// client is ever constructed (V7/V10), and wrap the endpoint's own plain
// errors in a package-owned clasterr code so a request failure is an exit-1
// user failure (V34), never a Cobra usage error.
//
// This file holds flows/brief.md §3-§4's synthesis step — rendering the
// brief prompt pair and calling the endpoint — kept separate from
// command.go's Cobra wiring and flag/config resolution, the same split
// internal/verbs/breadcrumb draws between its own Run and its command.go.
// §1 (gather) and §2 (the empty stop) live in command.go: gathering is a
// direct call into internal/verbs/brief's own exported Run (the plumbing
// brief verb), never a second implementation of its resolution/grouping
// logic (SURFACE V7's push-down rule already puts that logic below the
// line; a porcelain verb re-deriving it would be exactly the drift V7
// exists to prevent). Composing directly over another verb's package this
// way is new here — every existing plumbing verb avoids importing a sibling
// plumbing verb's package (see internal/verbs/brief's own gatherBreadcrumbs
// doc) — but that convention guards against same-tier (plumbing-to-plumbing)
// duplication; a porcelain verb calling down into its own namesake plumbing
// verb's Run is the opposite direction, and is exactly what V8's "same-named
// plumbing document verb" is for.
package briefverb

import (
	"context"
	"fmt"
	"strings"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/prompt"
	briefplumbing "github.com/procrastivity/clast/internal/verbs/brief"
)

// Synthesize implements flows/brief.md §3 (synthesize the working brief)
// for a gathered, non-empty result: it fills the brief prompt pair
// (prompt.Brief) from result per the flow's placeholder mapping and calls
// client.Complete, returning the assistant's briefing text verbatim (§4:
// "present the synthesized briefing as this flow's output" — presentation
// itself is command.go's concern).
//
// Callers must never call Synthesize for an Empty result (§2's stop) and
// must not construct client until after that check — Synthesize takes an
// already-built *llm.Client rather than a config.Config precisely so the
// empty-before-client ordering is enforced at the call site (command.go),
// not buried inside this function.
func Synthesize(ctx context.Context, result briefplumbing.Result, client *llm.Client) (string, error) {
	rendered, err := prompt.Render(prompt.Brief, promptData(result))
	if err != nil {
		return "", clasterr.New("brief.prompt-unavailable",
			fmt.Sprintf("brief: resolving the brief prompt pair: %v", err))
	}

	text, err := client.Complete(ctx, rendered.System, rendered.User)
	if err != nil {
		return "", clasterr.New("brief.llm-request-failed",
			fmt.Sprintf("brief: synthesizing the briefing: %v", err))
	}
	return text, nil
}

// promptData maps plumbing brief's gathered Result onto prompt.Brief's
// user-template placeholders (assets/prompts/brief-user.md): {{project}},
// {{current_label}}, {{entries}}, {{breadcrumbs}}, {{sessions}}. This
// mapping is a porcelain-owned finding (flows/brief.md §3 names which
// facts to fill from, not their exact rendered text) — the entries
// rendering in particular follows the templates' own stated convention
// (brief-system.md/brief-user.md: workspace headers appear only when the
// project spans more than one workspace) rather than always emitting
// them, so a single-workspace project reads to the model exactly as a
// single-workspace project.
func promptData(result briefplumbing.Result) map[string]string {
	return map[string]string{
		"project":       result.ProjectSlug,
		"current_label": result.CurrentWorkspace,
		"entries":       renderEntries(result.Groups),
		"breadcrumbs":   renderBreadcrumbs(result.Breadcrumbs),
		"sessions":      renderSessions(result.Sessions),
	}
}

// renderEntries renders groups as the templates describe: a "## Workspace:
// <label>" header per group only when more than one group is present (a
// single-workspace project gets no headers at all, per brief-system.md);
// each entry as its title, day, tags, and body — the three facts §3 names
// ("each with its title, tags, and body"), day added for temporal context
// the same way plumbing brief's own human document carries it.
func renderEntries(groups []briefplumbing.Group) string {
	if len(groups) == 0 {
		return "(none)"
	}

	multi := len(groups) > 1
	var b strings.Builder
	first := true
	for _, g := range groups {
		if multi {
			if !first {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "## Workspace: %s\n", g.Workspace)
		}
		for _, e := range g.Entries {
			if !first {
				b.WriteString("\n")
			}
			first = false
			tags := "none"
			if len(e.Entry.Tags) > 0 {
				tags = strings.Join(e.Entry.Tags, ", ")
			}
			fmt.Fprintf(&b, "### %s (%s)\nTags: %s\n\n%s\n", e.Entry.Title, e.Day, tags, e.Entry.Body)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderBreadcrumbs renders today's breadcrumbs as brief-system.md's own
// expected bullet shape ("HH:MM — text"), local time — the same
// HH:MM local-time rendering plumbing brief's own human document uses.
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

// renderSessions renders today's sessions as brief-system.md's own expected
// bullet shape ("HH:MM start: branch, msg-count messages").
func renderSessions(rows []briefplumbing.SessionRow) string {
	if len(rows) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		msgCount := r.Item.Session.Counts.User + r.Item.Session.Counts.Assistant
		fmt.Fprintf(&b, "- %s start: %s, %d messages", r.Item.Session.StartedAt.Local().Format("15:04"), r.Item.Session.Branch, msgCount)
	}
	return b.String()
}
