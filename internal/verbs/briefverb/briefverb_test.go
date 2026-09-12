package briefverb_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
	"github.com/procrastivity/clast/internal/verbs/brief"
	"github.com/procrastivity/clast/internal/verbs/briefverb"
)

func cfgWith(baseURL, model string) config.Config {
	return config.Config{
		"llm": map[string]any{
			"base_url": baseURL,
			"model":    model,
		},
	}
}

// sampleResult builds a small, non-empty brief.Result: one workspace, one
// curated entry (title/tags/body), one breadcrumb, one session — enough
// to exercise every placeholder Synthesize fills.
func sampleResult() brief.Result {
	started := time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC)
	item := journal.WalkItem{
		Key: journal.SessionKey{Harness: "claude", NativeID: "sess-01"},
		Session: journal.Session{
			Harness: "claude", SessionID: "sess-01", Branch: "go",
			StartedAt: started, Counts: journal.SessionCounts{User: 3, Assistant: 4},
		},
	}
	slug := "widget"
	return brief.Result{
		ProjectSlug:      "widget",
		CurrentWorkspace: "dev",
		Groups: []brief.Group{
			{
				Workspace: "dev",
				Branch:    "go",
				Entries: []brief.EntryRow{
					{
						Item: item,
						Day:  journal.Day("2026-09-11"),
						Entry: entry.Entry{
							Frontmatter: entry.Frontmatter{Title: "fixed the wake ordering bug", Tags: []string{"bugfix", "wake"}},
							Body:        "Tracked down groupByProject sorting by first appearance instead of recency.",
						},
					},
				},
			},
		},
		Breadcrumbs: []journal.BreadcrumbEntry{
			{
				Breadcrumb: journal.Breadcrumb{At: started.Add(2 * time.Hour), Slug: &slug, Text: "remember to check the stale-curated path"},
				Machine:    "framework",
			},
		},
		Sessions: []brief.SessionRow{
			{Item: item, Day: journal.Day("2026-09-11"), Title: ""},
		},
		Empty: false,
	}
}

func TestSynthesize_HappyPath_RequestCarriesRenderedPair(t *testing.T) {
	stub := llmtest.New(t, "here is your briefing")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	text, err := briefverb.Synthesize(context.Background(), sampleResult(), client)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if text != "here is your briefing" {
		t.Errorf("Synthesize returned %q, want the stub's canned completion", text)
	}

	reqs := stub.Requests()
	if len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want exactly 1", len(reqs))
	}
	req := reqs[0]
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Fatalf("request messages = %+v, want [system, user]", req.Messages)
	}
	user := req.Messages[1].Content
	for _, want := range []string{
		"widget",                      // {{project}}
		"dev",                         // {{current_label}}
		"fixed the wake ordering bug", // entry title
		"bugfix, wake",                // entry tags
		"groupByProject sorting",      // entry body (substring)
		"remember to check the stale", // breadcrumb text
	} {
		if !strings.Contains(user, want) {
			t.Errorf("rendered user prompt does not contain %q:\n%s", want, user)
		}
	}
}

func TestSynthesize_LLMRequestFailure_WrapsAsBriefCode(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.Fail(500, `{"error": "boom"}`)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	_, err = briefverb.Synthesize(context.Background(), sampleResult(), client)
	if err == nil {
		t.Fatal("Synthesize: want error, got nil")
	}
	var terr *clasterr.Error
	if !errors.As(err, &terr) {
		t.Fatalf("Synthesize error is not a *clasterr.Error: %v (%T)", err, err)
	}
	if terr.Code != "brief.llm-request-failed" {
		t.Errorf("Code = %q, want %q", terr.Code, "brief.llm-request-failed")
	}
}

// The empty-before-client ordering itself (command.go's RunE never
// constructs an llm.Client, let alone calls Synthesize, when
// brief.Result.Empty is true) is not this package's own Cobra wiring to
// unit-test in isolation — this codebase's convention (curate/dismiss/
// wake/brief's own _test.go files) is that Cobra-level command behavior is
// exercised only through internal/cli/e2e_test.go, against the built
// binary. See TestBrief_EmptyState_NoLLMConfigNeeded_NoRequests there for
// the end-to-end proof: a journal with nothing to gather, a config.yaml
// pointing llm.base_url at a stub that fails every request, and no
// CLAST_LLM_API_KEY set — the command still exits 0 and reports the empty
// case, proving neither the client nor the endpoint is ever reached.
