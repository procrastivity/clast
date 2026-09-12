package wakeverb

import (
	"context"
	"fmt"
	"time"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/verbs/curate"
	wakeplumbing "github.com/procrastivity/clast/internal/verbs/wake"
)

// RunAuto implements flows/wake.md's Auto mode section for the entire
// working set: §5 (promotion) is skipped entirely — "promotion requires a
// human choice" — and every drafted entry is measured against
// autoMinChars before writing; a draft that fails to generate, or lands
// below the threshold, is skipped (never dismissed, never retried — "no
// reviewer to ask"). A write failure through `curate` is a real error
// (disk, permissions, a validation defect this form's own construction
// should have prevented) and aborts the run, propagated to the caller —
// mirroring every other plumbing composition in this tree (curate/
// dismiss's own command.go), rather than silently downgrading it to a
// skip.
//
// RunAuto takes no machine parameter, unlike RunInteractive: Auto mode
// never dismisses (the flow's own Auto mode section), so it never needs
// dismiss.Run's machine argument.
//
// streams is used for exactly one thing (llm-verbs seal sweep F3): a
// per-session stderr diagnostic when a draft fails to generate. Before
// this fix, a failed draft was counted as a plain skip with nothing
// printed anywhere — indistinguishable, in --json or in the human
// summary, from a deliberate below-threshold skip, and silent on a
// misconfigured or flaky endpoint that fails every request in the run.
// The run's own exit code is unchanged (still 0): the flow's skip
// semantics stand — a failed draft is skipped, not a run failure — this
// only makes the skip visible and separately countable
// (SkippedDraftFailed vs. SkippedBelowThreshold vs. a deliberate skip).
func RunAuto(ctx context.Context, streams *iostreams.Streams, root string, cutoff journal.Cutoff, yesterday journal.Day, rows []wakeplumbing.Row, client *llm.Client, autoMinChars int) (Summary, error) {
	var summary Summary
	summary.Considered = len(rows)

	for _, row := range rows {
		raw, err := Draft(ctx, root, cutoff, yesterday, row.Item, client)
		if err != nil {
			// A draft that fails to generate is skipped, not retried —
			// there is no reviewer to ask in Auto mode (the flow's own
			// wording) — but it must never be SILENTLY skipped: print
			// one diagnostic line naming the session and the error.
			if _, werr := fmt.Fprintf(streams.Err, "wake --auto: session %s: draft generation failed: %v — skipping\n",
				row.Item.Key.DirName(), err); werr != nil {
				return summary, werr
			}
			summary.Skipped++
			summary.SkippedDraftFailed++
			continue
		}
		summary.Drafted++

		parsed := ParseDraft(raw, FallbackTitle(row.Item))
		if BelowThreshold(parsed.LengthCheck, autoMinChars) {
			// Skip — do not write, do not dismiss — so the session stays
			// in the working set for a later interactive pass.
			summary.Skipped++
			summary.SkippedBelowThreshold++
			continue
		}

		doc, err := BuildEntryDocument(entry.Frontmatter{Title: parsed.Title, Tags: parsed.Tags}, parsed.Body)
		if err != nil {
			return summary, err
		}
		if _, err := curate.Run(root, row.Item.Key.DirName(), doc, time.Now()); err != nil {
			return summary, err
		}
		summary.Accepted++
		summary.touchProject(row.Item)
	}

	return summary, nil
}
