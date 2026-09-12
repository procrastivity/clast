package wakeverb_test

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
	"github.com/procrastivity/clast/internal/journal/journaltest"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
	"github.com/procrastivity/clast/internal/verbs/wakeverb"
)

func cfgWith(baseURL, model string) config.Config {
	return config.Config{
		"llm": map[string]any{
			"base_url": baseURL,
			"model":    model,
		},
	}
}

// findItem re-walks root and returns the WalkItem for key — the tests
// below build fixtures through journaltest (which authors documents, not
// WalkItems) and then need the real, Walk-derived item Draft/RunAuto/
// RunInteractive all operate on.
func findItem(t *testing.T, root string, key journal.SessionKey) journal.WalkItem {
	t.Helper()
	items, _, err := journal.Walk(root)
	if err != nil {
		t.Fatalf("journal.Walk: %v", err)
	}
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no walk item for %s in %s", key.DirName(), root)
	return journal.WalkItem{}
}

const sampleDraft = "# Session: fixed the wake ordering bug\n\n" +
	"## Goal\nSort the working set by recency.\n\n" +
	"## What shipped\n- Reordered groupByProject.\n\n" +
	"Suggested tags: bugfix, wake\n"

func TestDraft_HappyPath_RequestCarriesRenderedPlaceholders(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "sess-01"}
	started := time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, started).
		WithTranscript("2026-09-11", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hello from the transcript"}}`+"\n")).
		WithProject("2026-09-11", key, journal.SessionProject{ID: "p1", Slug: "widget", Clone: "c1", Label: "dev", Path: "/dev"})
	item := findItem(t, fx.Root(), key)

	stub := llmtest.New(t, sampleDraft)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}
	yesterday := journal.Day("2026-09-10")

	text, err := wakeverb.Draft(context.Background(), fx.Root(), cutoff, yesterday, item, client)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if text != sampleDraft {
		t.Errorf("Draft = %q, want the stub's canned completion", text)
	}

	reqs := stub.Requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 2 {
		t.Fatalf("stub captured %+v, want exactly one [system,user] request", reqs)
	}
	user := reqs[0].Messages[1].Content
	for _, want := range []string{"widget", "hello from the transcript", "2026-09-11T09:30:00Z"} {
		if !strings.Contains(user, want) {
			t.Errorf("rendered user prompt does not contain %q:\n%s", want, user)
		}
	}
}

func TestDraft_LLMFailure_WrapsAsWakeCode(t *testing.T) {
	fx := journaltest.New(t)
	key := journal.SessionKey{Harness: "claude", NativeID: "sess-02"}
	started := time.Date(2026, 9, 11, 9, 30, 0, 0, time.UTC)
	fx.Captured("2026-09-11", key, journal.TranscriptFingerprint{Format: "claude-jsonl", Lines: 1, SHA256: "a"}, started).
		WithTranscript("2026-09-11", key, []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}`+"\n"))
	item := findItem(t, fx.Root(), key)

	stub := llmtest.New(t, "unused")
	stub.Fail(500, `{"error":"boom"}`)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")
	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("llm.NewClient: %v", err)
	}

	cutoff, err := journal.ParseCutoff(journal.DefaultCutoffString)
	if err != nil {
		t.Fatalf("ParseCutoff: %v", err)
	}

	_, err = wakeverb.Draft(context.Background(), fx.Root(), cutoff, journal.Day("2026-09-10"), item, client)
	assertCode(t, err, "wake.llm-request-failed")
}

func TestParseDraft_ExtractsTitleTagsAndStripsBoth(t *testing.T) {
	parsed := wakeverb.ParseDraft(sampleDraft, "fallback title")
	if parsed.Title != "fixed the wake ordering bug" {
		t.Errorf("Title = %q, want %q", parsed.Title, "fixed the wake ordering bug")
	}
	if got, want := parsed.Tags, []string{"bugfix", "wake"}; !equalSlices(got, want) {
		t.Errorf("Tags = %v, want %v", got, want)
	}
	if strings.Contains(parsed.Body, "# Session:") {
		t.Errorf("Body still carries the title heading: %q", parsed.Body)
	}
	if strings.Contains(strings.ToLower(parsed.Body), "suggested tags") {
		t.Errorf("Body still carries the tags trailer: %q", parsed.Body)
	}
	if !strings.Contains(parsed.Body, "## Goal") {
		t.Errorf("Body dropped real content: %q", parsed.Body)
	}
	// LengthCheck keeps the title heading (only the tags trailer is
	// stripped) — the Auto mode length guard's own measure.
	if !strings.Contains(parsed.LengthCheck, "# Session:") {
		t.Errorf("LengthCheck unexpectedly dropped the title heading: %q", parsed.LengthCheck)
	}
	if strings.Contains(strings.ToLower(parsed.LengthCheck), "suggested tags") {
		t.Errorf("LengthCheck still carries the tags trailer: %q", parsed.LengthCheck)
	}
}

func TestParseDraft_NoTitleHeading_UsesFallback(t *testing.T) {
	raw := "## Goal\nJust some prose, no title line.\n\nSuggested tags: none\n"
	parsed := wakeverb.ParseDraft(raw, "Session claude-abc123")
	if parsed.Title != "Session claude-abc123" {
		t.Errorf("Title = %q, want the fallback", parsed.Title)
	}
}

func TestBelowThreshold(t *testing.T) {
	if !wakeverb.BelowThreshold("short", 60) {
		t.Error("BelowThreshold(\"short\", 60) = false, want true")
	}
	if wakeverb.BelowThreshold(strings.Repeat("x", 60), 60) {
		t.Error("BelowThreshold(60 chars, 60) = true, want false (at threshold, not below)")
	}
	// minChars 0 never triggers — a rune count is never negative.
	if wakeverb.BelowThreshold("", 0) {
		t.Error("BelowThreshold(\"\", 0) = true, want false (0 disables the guard)")
	}
	// Multi-byte runes count as one character each, not one per byte.
	multiByte := strings.Repeat("é", 10) // 20 bytes, 10 runes
	if !wakeverb.BelowThreshold(multiByte, 11) {
		t.Error("BelowThreshold should count runes, not bytes")
	}
}

// TestBuildEntryDocument_RoundTripsThroughEntryParse pins the posture
// recorded in wakeverb.go's own doc comment: BuildEntryDocument's
// yaml.Marshal-based frontmatter construction can never hand curate's
// validate() malformed YAML, even when the LLM's own title text is full
// of YAML-special characters — the "draft-invalid-frontmatter handling"
// the step brief asks this step to decide and record.
func TestBuildEntryDocument_RoundTripsThroughEntryParse(t *testing.T) {
	cases := []struct {
		name  string
		title string
		tags  []string
		body  string
	}{
		{"plain", "fixing the flaky test", []string{"testing", "ci"}, "The test flaked."},
		{"colon and quotes", `fix: the "flaky" test: colon galore`, []string{"testing"}, "body"},
		{"yaml-special chars", "- [not a list]: {nope}", nil, "body\nwith\nnewlines"},
		{"no tags", "a title", nil, "body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := wakeverb.BuildEntryDocument(entry.Frontmatter{Title: c.title, Tags: c.tags}, c.body)
			if err != nil {
				t.Fatalf("BuildEntryDocument: %v", err)
			}
			e, err := entry.Parse(doc)
			if err != nil {
				t.Fatalf("entry.Parse(BuildEntryDocument(...)) failed: %v\ndocument:\n%s", err, doc)
			}
			if e.Title != c.title {
				t.Errorf("round-tripped title = %q, want %q", e.Title, c.title)
			}
			if strings.TrimSpace(e.Body) != strings.TrimSpace(c.body) {
				t.Errorf("round-tripped body = %q, want %q", e.Body, c.body)
			}
		})
	}
}

func TestPromoteSection_FoldsAppendedSections(t *testing.T) {
	body := "## Goal\nDo the thing."
	body = wakeverb.PromoteSection(body, "Decision", "use postgres", "chose postgres over sqlite")
	body = wakeverb.PromoteSection(body, "Workflow", "release steps", "tag, build, publish")

	for _, want := range []string{
		"## Goal\nDo the thing.",
		"## Decision\n\n### use postgres\n\nchose postgres over sqlite",
		"## Workflow\n\n### release steps\n\ntag, build, publish",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("folded body does not contain %q:\nfull body:\n%s", want, body)
		}
	}
	// Order: each promotion lands after the previous one.
	if strings.Index(body, "## Decision") > strings.Index(body, "## Workflow") {
		t.Errorf("promotions out of order:\n%s", body)
	}
}

// assertCode fails t unless err is a *clasterr.Error carrying exactly
// code — curate's own test helper (internal/verbs/curate/curate_test.go),
// repeated here for the same reason every other verb package's tests
// repeat it rather than importing clasterr's test-only assertion helper
// (there isn't one).
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with code %q, got nil", code)
	}
	var cerr *clasterr.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error = %v (%T), want a *clasterr.Error", err, err)
	}
	if cerr.Code != code {
		t.Errorf("code = %q, want %q", cerr.Code, code)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
