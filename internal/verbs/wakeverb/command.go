package wakeverb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/query"
	"github.com/procrastivity/clast/internal/surface"
	wakeplumbing "github.com/procrastivity/clast/internal/verbs/wake"
)

// Command constructs the top-level `clast wake [--auto]` verb (SURFACE
// V9): the wake shape's verb form. There is no `--since` flag at this
// tier — the step brief's own usage line (`clast wake [--auto] [--json]
// [-v]`) names none — so the working set always resolves the `since`
// config key's default, the same call `plumbing wake` makes when its own
// `--since` is left at its flag default (briefverb's own no-`--since`
// posture, repeated here for the same reason).
func Command(streams *iostreams.Streams) *cobra.Command {
	var auto bool

	cmd := &cobra.Command{
		Use:   "wake [--auto]",
		Short: "curate captured sessions into journal entries (shape, flows/wake.md)",
		Long: "wake curates the working set of captured sessions — every session in state captured, " +
			"plus every curated session that has gone stale (MODEL M7) — into durable journal entries. " +
			"For each session: its transcript (capped at 2000 chars/turn) and yesterday's breadcrumbs " +
			"for its project are read, a draft entry is generated through the configured LLM endpoint " +
			"(SURFACE V12) using the wake-draft prompt pair, and the draft is accepted, edited, " +
			"dismissed, or skipped. Accepting writes through `plumbing curate`; dismissing writes " +
			"through `plumbing dismiss`; a stale session is offered for re-curation, never revoked. " +
			"Interactively, each draft is presented with a number menu (accept/edit/dismiss/skip); " +
			"edit prompts for a one-line feedback description, regenerates the draft with it folded " +
			"into the request, and returns to the same menu with the revised draft. --auto runs " +
			"non-interactively: drafts shorter than the configured wake.auto_min_chars (default 60) " +
			"are skipped — not written, not dismissed, so they stay available for a later interactive " +
			"pass — qualifying drafts are accepted automatically, and nothing is ever dismissed or " +
			"promoted in this mode; a draft that fails to generate is skipped with a stderr diagnostic, " +
			"never a silent drop. An empty working set needs no llm configuration — no endpoint call " +
			"is ever made for it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cliflags.FromContext(cmd.Context())
			ctx := cmd.Context()

			cfg, err := config.Load()
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("loading config: %v", err))
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving journal root: %v", err))
			}
			cutoff, err := journal.ConfiguredCutoff(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving day cutoff: %v", err))
			}
			defaultSince, err := query.ConfiguredSince(cfg)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving the since config key: %v", err))
			}
			now := time.Now()
			sinceBound, err := query.ResolveSince("", defaultSince, cutoff, now)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving the since window: %v", err))
			}

			// flows/wake.md §1 — the working set, via plumbing wake's own
			// Run: same grouping/staleness rules, no second implementation.
			rows, err := wakeplumbing.Run(root, sinceBound, cutoff)
			if err != nil {
				return err
			}

			if len(rows) == 0 {
				// Stop cold, BEFORE constructing any LLM client (mirrors
				// briefverb's Empty check and retroverb's HasEntries): an
				// empty working set must never require llm configuration.
				if flags.JSON {
					return writeJSON(streams, Summary{})
				}
				_, err := fmt.Fprintln(streams.Out, "Nothing to curate — the working set is empty.")
				return err
			}

			autoMinChars, err := wakeplumbing.ConfiguredAutoMinChars(cfg)
			if err != nil {
				return err
			}
			client, err := llm.NewClient(cfg)
			if err != nil {
				return err
			}
			machine, err := journal.Hostname()
			if err != nil {
				return err
			}
			// flows/wake.md §2's literal "--day yesterday" token — relative
			// to when wake is run, not to any individual session's own day.
			yesterday, err := journal.ParseDay("yesterday", cutoff)
			if err != nil {
				return err
			}

			var summary Summary
			if auto {
				summary, err = RunAuto(ctx, streams, root, cutoff, yesterday, rows, client, autoMinChars)
			} else {
				summary, err = RunInteractive(ctx, streams, root, cutoff, yesterday, rows, client, machine)
			}
			if err != nil {
				return err
			}

			if flags.Verbose {
				if _, err := fmt.Fprintf(streams.Err,
					"wake: %d considered, %d drafted, %d accepted, %d dismissed, %d skipped\n",
					summary.Considered, summary.Drafted, summary.Accepted, summary.Dismissed, summary.Skipped,
				); err != nil {
					return err
				}
			}

			if flags.JSON {
				return writeJSON(streams, summary)
			}
			return writeHuman(streams, summary, auto)
		},
	}

	cmd.Flags().BoolVar(&auto, "auto", false,
		"non-interactive: accept drafts at or above wake.auto_min_chars, skip the rest, never dismiss or promote")

	// OutputSchema stays unfilled (C3.7: no consumer yet) — the same
	// no-speculative-schema posture briefverb's and retroverb's own
	// jsonPayload take.
	surface.Annotate(cmd, surface.LLM)
	return cmd
}

// promotedJSON is --json's nested "promoted" object.
type promotedJSON struct {
	Decisions    int `json:"decisions"`
	CommonIssues int `json:"common_issues"`
	Workflows    int `json:"workflows"`
}

// jsonPayload is wake's --json run summary — schema UNFILLED (C3.7),
// shape recorded here per the step brief's own ask: "sessions considered,
// drafted, accepted, skipped, dismissed", plus the threshold/promotion
// breakdowns flows/wake.md §7 and its Auto mode section both call for.
type jsonPayload struct {
	Considered            int          `json:"considered"`
	Drafted               int          `json:"drafted"`
	Accepted              int          `json:"accepted"`
	Dismissed             int          `json:"dismissed"`
	Skipped               int          `json:"skipped"`
	SkippedBelowThreshold int          `json:"skipped_below_threshold"`
	SkippedDraftFailed    int          `json:"skipped_draft_failed"`
	ProjectsTouched       int          `json:"projects_touched"`
	Promoted              promotedJSON `json:"promoted"`
}

func writeJSON(streams *iostreams.Streams, s Summary) error {
	payload := jsonPayload{
		Considered:            s.Considered,
		Drafted:               s.Drafted,
		Accepted:              s.Accepted,
		Dismissed:             s.Dismissed,
		Skipped:               s.Skipped,
		SkippedBelowThreshold: s.SkippedBelowThreshold,
		SkippedDraftFailed:    s.SkippedDraftFailed,
		ProjectsTouched:       s.ProjectsTouched(),
		Promoted: promotedJSON{
			Decisions:    s.PromotedDecisions,
			CommonIssues: s.PromotedCommonIssues,
			Workflows:    s.PromotedWorkflows,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman renders flows/wake.md §7's closing summary — no byte promise
// (HANDOFF §8): sessions curated/dismissed/skipped, across however many
// projects were touched, plus counts of anything promoted; Auto mode
// additionally reports the below-threshold subset (the Auto mode
// section's own extra line).
func writeHuman(streams *iostreams.Streams, s Summary, auto bool) error {
	if _, err := fmt.Fprintln(streams.Out, "\nWake complete."); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(streams.Out, "Curated: %d session(s) across %d project(s).\n", s.Accepted, s.ProjectsTouched()); err != nil {
		return err
	}
	if s.Dismissed > 0 {
		if _, err := fmt.Fprintf(streams.Out, "Dismissed: %d session(s).\n", s.Dismissed); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(streams.Out, "Skipped: %d session(s).\n", s.Skipped); err != nil {
		return err
	}
	if auto {
		if _, err := fmt.Fprintf(streams.Out, "Skipped (below length threshold): %d session(s).\n", s.SkippedBelowThreshold); err != nil {
			return err
		}
		if s.SkippedDraftFailed > 0 {
			if _, err := fmt.Fprintf(streams.Out, "Skipped (draft generation failed): %d session(s).\n", s.SkippedDraftFailed); err != nil {
				return err
			}
		}
	}
	if total := s.PromotedDecisions + s.PromotedCommonIssues + s.PromotedWorkflows; total > 0 {
		if _, err := fmt.Fprintln(streams.Out, "Promoted:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(streams.Out, "  Decisions: %d\n", s.PromotedDecisions); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(streams.Out, "  Common issues: %d\n", s.PromotedCommonIssues); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(streams.Out, "  Workflows: %d\n", s.PromotedWorkflows); err != nil {
			return err
		}
	}
	return nil
}
