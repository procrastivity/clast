package wakeverb

import (
	"context"
	"time"

	"github.com/procrastivity/clast/internal/entry"
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
func RunAuto(ctx context.Context, root string, cutoff journal.Cutoff, yesterday journal.Day, rows []wakeplumbing.Row, client *llm.Client, autoMinChars int) (Summary, error) {
	var summary Summary
	summary.Considered = len(rows)

	for _, row := range rows {
		raw, err := Draft(ctx, root, cutoff, yesterday, row.Item, client)
		if err != nil {
			// A draft that fails to generate is skipped, not retried —
			// there is no reviewer to ask in Auto mode (the flow's own
			// wording).
			summary.Skipped++
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
