package show

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/source"
	"github.com/procrastivity/clast/internal/surface"
)

// outputSchema is show's default-mode --json payload shape (V35: filled).
// --transcript's --json shape ({"turns": [...]}) is a distinct, simpler
// payload not covered here — this schema documents the facts+curation+
// entry view, show's own primary consumed shape (a flow's step 1).
const outputSchema = `{
	"type": "object",
	"properties": {
		"session": {
			"type": "object",
			"properties": {
				"schema_version": {"type": "integer"},
				"harness": {"type": "string"},
				"session_id": {"type": "string"},
				"machine": {"type": "string"},
				"project": {
					"type": "object",
					"properties": {
						"id": {"type": "string"},
						"slug": {"type": "string"},
						"clone": {"type": "string"},
						"label": {"type": "string"},
						"path": {"type": "string"}
					},
					"required": ["id", "slug", "clone", "label", "path"]
				},
				"worktree": {"type": "string"},
				"branch": {"type": "string"},
				"started_at": {"type": "string"},
				"last_active_at": {"type": "string"},
				"captured_at": {"type": "string"},
				"source_path": {"type": "string"},
				"counts": {
					"type": "object",
					"properties": {
						"user": {"type": "integer"},
						"assistant": {"type": "integer"}
					},
					"required": ["user", "assistant"]
				},
				"substantive": {"type": "boolean"},
				"transcript": {
					"type": "object",
					"properties": {
						"format": {"type": "string"},
						"lines": {"type": "integer"},
						"sha256": {"type": "string"}
					},
					"required": ["format", "lines", "sha256"]
				}
			},
			"required": [
				"schema_version", "harness", "session_id", "machine", "worktree",
				"branch", "started_at", "last_active_at", "captured_at",
				"source_path", "counts", "substantive", "transcript"
			]
		},
		"curation": {
			"type": "object",
			"properties": {
				"state": {"type": "string"},
				"at": {"type": "string"},
				"machine": {"type": "string"},
				"reason": {"type": ["string", "null"]},
				"transcript_at_curation": {
					"type": "object",
					"properties": {
						"lines": {"type": "integer"},
						"sha256": {"type": "string"}
					},
					"required": ["lines", "sha256"]
				}
			},
			"required": ["state"]
		},
		"stale": {"type": "boolean"},
		"entry": {
			"type": ["object", "null"],
			"properties": {
				"title": {"type": "string"},
				"tags": {"type": "array", "items": {"type": "string"}},
				"body": {"type": "string"}
			},
			"required": ["title", "tags", "body"]
		}
	},
	"required": ["session", "curation", "stale", "entry"]
}`

// Command constructs the `clast plumbing show <session>` verb (V18). The
// Use line records the positional verbatim (C3.8).
func Command(streams *iostreams.Streams) *cobra.Command {
	var transcript bool
	var maxTurnChars int

	cmd := &cobra.Command{
		Use:   "show <session>",
		Short: "show one session's facts, curation, and entry — or its transcript",
		Long: "show <session> resolves the locator (the full session directory name, or any unique " +
			"prefix — V4) and prints its facts, curation state (+staleness), and the curated entry's " +
			"body when one exists. --transcript renders the turns instead, through the session's own " +
			"transcript.format renderer — the one sanctioned transcript read (M9); --max-turn-chars <n> " +
			"caps each turn's text at that many runes for prompt budgets (V7). A format with no " +
			"renderer on this build refuses (validation.unknown-transcript-format). --json emits " +
			"{session, curation, stale, entry} in default mode, or {turns} under --transcript.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			root, err := journal.Root(cfg)
			if err != nil {
				return err
			}

			result, err := Run(root, args[0])
			if err != nil {
				return err
			}

			if transcript {
				turns, err := Transcript(root, result.Item, maxTurnChars)
				if err != nil {
					return err
				}
				if flags.JSON {
					return writeTranscriptJSON(streams, turns)
				}
				return writeTranscriptHuman(streams, turns)
			}

			if flags.JSON {
				return writeJSON(streams, result)
			}
			return writeHuman(streams, result)
		},
	}

	cmd.Flags().BoolVar(&transcript, "transcript", false, "render the transcript's turns instead of facts")
	cmd.Flags().IntVar(&maxTurnChars, "max-turn-chars", 0, "cap each rendered turn's text at this many runes (0 = uncapped, V7)")

	manifest.SetOutputSchema(cmd, json.RawMessage(outputSchema))
	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// curationJSON is show's curation --json field: state is always present
// (derived per MODEL §2 even when curation.json itself is absent — the
// `captured` case); the rest ride only when curation.json exists.
type curationJSON struct {
	State                string                   `json:"state"`
	At                   *time.Time               `json:"at,omitempty"`
	Machine              string                   `json:"machine,omitempty"`
	Reason               *string                  `json:"reason,omitempty"`
	TranscriptAtCuration *journal.TranscriptStamp `json:"transcript_at_curation,omitempty"`
}

func curationPayload(item journal.WalkItem) curationJSON {
	c := curationJSON{State: string(item.State())}
	if item.CurationPresent {
		at := item.Curation.At
		c.At = &at
		c.Machine = item.Curation.Machine
		c.Reason = item.Curation.Reason
		c.TranscriptAtCuration = item.Curation.TranscriptAtCuration
	}
	return c
}

// entryJSON is show's entry --json field: nil when the session carries no
// entry.md (MODEL §4: present iff curated).
type entryJSON struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Body  string   `json:"body"`
}

func entryPayload(e *entry.Entry) *entryJSON {
	if e == nil {
		return nil
	}
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	return &entryJSON{Title: e.Title, Tags: tags, Body: e.Body}
}

func writeJSON(streams *iostreams.Streams, result Result) error {
	payload := struct {
		Session  journal.Session `json:"session"`
		Curation curationJSON    `json:"curation"`
		Stale    bool            `json:"stale"`
		Entry    *entryJSON      `json:"entry"`
	}{
		Session:  result.Item.Session,
		Curation: curationPayload(result.Item),
		Stale:    result.Item.Stale(),
		Entry:    entryPayload(result.Entry),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeHuman emits show's default-mode view: one fact per line, then the
// curated entry's title/tags/body when one exists. No byte promise.
func writeHuman(streams *iostreams.Streams, result Result) error {
	item := result.Item
	lines := []string{
		fmt.Sprintf("session: %s", item.Key.DirName()),
		fmt.Sprintf("harness: %s", item.Session.Harness),
		fmt.Sprintf("machine: %s", item.Session.Machine),
		fmt.Sprintf("project: %s", projectLabel(item)),
		fmt.Sprintf("worktree: %s", emptyAs(item.Session.Worktree, "(main)")),
		fmt.Sprintf("branch: %s", emptyAs(item.Session.Branch, "(detached)")),
		fmt.Sprintf("started: %s", item.Session.StartedAt.Format(time.RFC3339)),
		fmt.Sprintf("last active: %s", item.Session.LastActiveAt.Format(time.RFC3339)),
		fmt.Sprintf("counts: user=%d assistant=%d", item.Session.Counts.User, item.Session.Counts.Assistant),
		fmt.Sprintf("substantive: %v", item.Session.Substantive),
		fmt.Sprintf("transcript: %s (%d lines)", item.Session.Transcript.Format, item.Session.Transcript.Lines),
		fmt.Sprintf("state: %s", stateColumn(item)),
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(streams.Out, l); err != nil {
			return err
		}
	}

	if result.Entry == nil {
		return nil
	}
	if _, err := fmt.Fprintln(streams.Out); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(streams.Out, "title: %s\n", result.Entry.Title); err != nil {
		return err
	}
	if len(result.Entry.Tags) > 0 {
		if _, err := fmt.Fprintf(streams.Out, "tags: %s\n", strings.Join(result.Entry.Tags, ", ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(streams.Out); err != nil {
		return err
	}
	_, err := fmt.Fprint(streams.Out, result.Entry.Body)
	return err
}

// turnJSON is one rendered turn's --json shape under --transcript.
type turnJSON struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func writeTranscriptJSON(streams *iostreams.Streams, turns []source.Turn) error {
	out := make([]turnJSON, len(turns))
	for i, t := range turns {
		out[i] = turnJSON{Role: t.Role, Text: t.Text}
	}
	payload := struct {
		Turns []turnJSON `json:"turns"`
	}{Turns: out}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, string(b))
	return err
}

// writeTranscriptHuman emits one "<role>: <text>" line per turn.
func writeTranscriptHuman(streams *iostreams.Streams, turns []source.Turn) error {
	if len(turns) == 0 {
		_, err := fmt.Fprintln(streams.Out, "no turns")
		return err
	}
	for _, t := range turns {
		if _, err := fmt.Fprintf(streams.Out, "%s: %s\n", t.Role, t.Text); err != nil {
			return err
		}
	}
	return nil
}

// projectLabel renders a session's project/label fact: "<slug>/<label>"
// when the session has a frozen project (MODEL §4), "-" for a
// projectless one.
func projectLabel(item journal.WalkItem) string {
	if item.Session.Project == nil {
		return "-"
	}
	return item.Session.Project.Slug + "/" + item.Session.Project.Label
}

// stateColumn renders a session's state fact, appending " (stale)" per M7
// when it applies.
func stateColumn(item journal.WalkItem) string {
	s := string(item.State())
	if item.Stale() {
		s += " (stale)"
	}
	return s
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
