package briefverb

import (
	"encoding/json"
	"fmt"
	"os"
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
	briefplumbing "github.com/procrastivity/clast/internal/verbs/brief"
)

// jsonPayload is brief's --json shape (V35/C2.3: every verb honors
// --json). Schema UNFILLED (C3.7 — no consumer yet; manifest.SetOutputSchema
// is deliberately not called here, the same "no speculative schema" posture
// every other not-yet-consumed verb in this tree already takes). The shape
// itself is this step's own call: Empty mirrors plumbing brief's own
// `empty: true` fact (§2's stop is visible in --json too, not just human
// output), and Brief carries the synthesized text ("" in the empty case —
// present and empty rather than absent, so a consumer need not special-case
// a missing key).
type jsonPayload struct {
	Project string `json:"project"`
	Empty   bool   `json:"empty"`
	Brief   string `json:"brief"`
}

// Command constructs the top-level `clast brief [<project>]` verb (SURFACE
// V9/V10/V26–V29): the brief shape's verb form. <project> is a registry
// locator (id or slug); omitted, the project defaults from the cwd (the
// whereami path, V10) — both resolved by internal/verbs/brief's own Run,
// reused here rather than re-implemented (this package's own doc comment).
// There is no --since here: V9/V10's top-level usage table names only the
// positional; the `since` config key still bounds "recent" the same way it
// does for `plumbing brief`, just with no per-call override at this tier.
func Command(streams *iostreams.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brief [<project>]",
		Short: "working brief for a project (shape, flows/brief.md)",
		Long: "brief synthesizes a briefing for one project so you can resume work without " +
			"re-explaining context: recent curated entries, today's breadcrumbs, and today's " +
			"sessions, gathered and read back through the configured LLM endpoint (SURFACE V12). " +
			"<project> is a registry locator (id or slug); omitted, the project defaults from the " +
			"current working directory (the whereami path, V10) — an unregistered cwd refuses " +
			"(refusal.unknown-clone, naming `clast init`), a cwd outside any git repository is " +
			"validation.not-a-git-repo. When there is nothing to brief from (no curated entries, " +
			"breadcrumbs, or sessions), brief reports that and stops — no LLM call is made, and no " +
			"llm configuration is required for that case. Read only: brief writes nothing, anywhere.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())
			ctx := cmd.Context()

			var projectLocator string
			if len(args) == 1 {
				projectLocator = args[0]
			}

			dir, err := os.Getwd()
			if err != nil {
				return err
			}
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
			// No --since flag at this tier (V9/V10): always resolve the
			// config-key default, the same call `plumbing brief` makes when
			// its own --since is left at its flag default ("").
			sinceBound, err := query.ResolveSince("", defaultSince, cutoff, now)
			if err != nil {
				return clasterr.New("validation.config", fmt.Sprintf("resolving the since window: %v", err))
			}
			today := cutoff.DayOf(now)

			// flows/brief.md §1 — gather the material, via plumbing brief's
			// own Run: same positional/cwd resolution, no second
			// implementation of it here.
			result, err := briefplumbing.Run(ctx, root, dir, projectLocator, cutoff, sinceBound, today)
			if err != nil {
				return err
			}

			// flows/brief.md §2 — stop on an empty payload, BEFORE
			// constructing any LLM client: an empty brief must never fail
			// for want of llm.base_url/model/API key, since no endpoint
			// call is ever made for it.
			if result.Empty {
				if flags.JSON {
					return writeJSON(streams, jsonPayload{Project: result.ProjectSlug, Empty: true})
				}
				return writeEmptyHuman(streams, result.ProjectSlug)
			}

			client, err := llm.NewClient(cfg)
			if err != nil {
				return err
			}

			// flows/brief.md §3 — synthesize the working brief.
			text, err := Synthesize(ctx, result, client)
			if err != nil {
				return err
			}

			// flows/brief.md §4 — present the synthesized briefing.
			if flags.JSON {
				return writeJSON(streams, jsonPayload{Project: result.ProjectSlug, Brief: text})
			}
			_, err = fmt.Fprintln(streams.Out, text)
			return err
		},
	}

	surface.Annotate(cmd, surface.LLM)
	return cmd
}

// writeEmptyHuman states flows/brief.md §2's empty-state guidance
// readably — the porcelain's own presentation (non-normative in the flow):
// no curated entries, breadcrumbs, or sessions for project.
func writeEmptyHuman(streams *iostreams.Streams, project string) error {
	_, err := fmt.Fprintf(streams.Out,
		"Nothing to brief for %s — no curated entries, breadcrumbs, or sessions. "+
			"Run `clast wake` to curate recent sessions.\n", project)
	return err
}

func writeJSON(streams *iostreams.Streams, payload jsonPayload) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}
