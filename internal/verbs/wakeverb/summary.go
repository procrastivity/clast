package wakeverb

import "github.com/procrastivity/clast/internal/journal"

// Summary tallies flows/wake.md §7's closing counts, across either
// disposition path (auto.go/interactive.go both accumulate into one of
// these) — schema UNFILLED for --json (C3.7: no consumer yet), the shape
// itself a wakeverb-owned finding recorded per the step brief.
type Summary struct {
	// Considered is §1's working-set size — every row wake.Run returned,
	// regardless of what happened to it afterward.
	Considered int
	// Drafted counts every session for which §3 produced a completion
	// (regardless of its later disposition) — a generation failure never
	// increments this.
	Drafted int
	// Accepted counts every session written through `curate` (§4's Accept
	// path, taken directly or via Auto mode's automatic accept).
	Accepted int
	// Dismissed counts every session written through `dismiss` (§4's
	// Dismiss path — interactive only; Auto mode never dismisses, per the
	// flow's Auto mode section).
	Dismissed int
	// Skipped counts every session left untouched: an interactive Skip
	// choice, a draft that failed to generate (either mode), or (Auto
	// mode) a draft below the length threshold. SkippedBelowThreshold is
	// the subset of this total attributable to the threshold specifically.
	Skipped               int
	SkippedBelowThreshold int
	// PromotedDecisions/PromotedCommonIssues/PromotedWorkflows count §5's
	// promoted sections, by kind, across the whole run (Auto mode never
	// promotes — §5 is skipped there entirely, per the flow).
	PromotedDecisions    int
	PromotedCommonIssues int
	PromotedWorkflows    int

	// projectsTouched is the set of project slugs (unprojectedLabel for a
	// projectless session) with at least one Accepted session — "however
	// many projects were touched" (§7), mirroring the old porcelain's own
	// curated_projects tally (feel input): a session merely considered,
	// dismissed, or skipped doesn't count as "touched".
	projectsTouched map[string]struct{}
}

// touchProject records item's project as touched (an Accepted write).
func (s *Summary) touchProject(item journal.WalkItem) {
	if s.projectsTouched == nil {
		s.projectsTouched = map[string]struct{}{}
	}
	key := unprojectedLabel
	if item.Session.Project != nil {
		key = item.Session.Project.Slug
	}
	s.projectsTouched[key] = struct{}{}
}

// ProjectsTouched returns the count of distinct projects touchProject
// recorded.
func (s Summary) ProjectsTouched() int {
	return len(s.projectsTouched)
}
