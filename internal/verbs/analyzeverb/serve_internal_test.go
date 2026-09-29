package analyzeverb

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
)

func TestHandler_ServesPage(t *testing.T) {
	h := NewHandler(func() (Page, error) { return fixturePage(), nil }, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "four words of entry") {
		t.Error("body lacks the fixture entry text")
	}
	want, err := renderPage(fixturePage())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != string(want) {
		t.Error("served body differs from renderPage output")
	}
}

func TestHandler_GatherErrorIs500AndLogged(t *testing.T) {
	var logged []string
	logf := func(format string, _ ...any) { logged = append(logged, format) }
	h := NewHandler(func() (Page, error) { return Page{}, errors.New("boom") }, logf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("body = %q, want the error message", rec.Body.String())
	}
	if len(logged) != 1 {
		t.Errorf("logged %d lines, want 1", len(logged))
	}
}

func TestHandler_PathAndMethod(t *testing.T) {
	h := NewHandler(func() (Page, error) { return fixturePage(), nil }, nil)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/nope", http.StatusNotFound},
		{http.MethodHead, "/", http.StatusOK},
		{http.MethodPost, "/", http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestHandler_HeadHasHeadersNoBody(t *testing.T) {
	h := NewHandler(func() (Page, error) { return fixturePage(), nil }, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestHandler_GathersPerRequest(t *testing.T) {
	calls := 0
	h := NewHandler(func() (Page, error) {
		calls++
		p := fixturePage()
		p.Day = journal.Day(fmt.Sprintf("2026-04-%02d", calls))
		return p, nil
	}, nil)

	var bodies []string
	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		bodies = append(bodies, rec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("gather called %d times, want 2 (once per request)", calls)
	}
	if bodies[0] == bodies[1] {
		t.Error("two requests returned the same page, want each to see a fresh gather")
	}
}

func TestHandler_MethodNotAllowedSetsAllow(t *testing.T) {
	h := NewHandler(func() (Page, error) { return fixturePage(), nil }, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

// transcriptFixture writes one captured session with a subagent under a
// temp journal dir and returns a handler whose window holds it.
func transcriptFixture(t *testing.T, jsonl string) (h http.Handler, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir = t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("session.json", `{"transcript":{"format":"claude-jsonl"}}`)
	write("transcript.jsonl", jsonl)
	write("subagents/agent-a1.jsonl", `{"type":"user","uuid":"u2","message":{"role":"user","content":"SUBPROMPT"}}`+"\n")
	write("subagents/agent-a1.meta.json", `{"agentType":"Explore","description":"look around","toolUseId":"toolu_x"}`)

	page := fixturePage()
	page.Days[0].Projects[0].Sessions[0].dir = dir
	return NewHandler(func() (Page, error) { return page, nil }, nil), dir
}

const okJSONL = `{"type":"user","uuid":"u1","timestamp":"2026-09-21T14:00:00Z","message":{"role":"user","content":"TRANSCRIPTHELLO"}}` + "\n"

func fixtureSessionID() string { return fixturePage().Sessions()[0].ID }

func TestHandler_Transcript(t *testing.T) {
	h, _ := transcriptFixture(t, okJSONL)
	id := fixtureSessionID()

	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	rec := get(http.MethodGet, TranscriptURL(id))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "TRANSCRIPTHELLO") {
		t.Fatalf("session transcript = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if !strings.Contains(rec.Body.String(), "/agents/") {
		t.Error("session transcript does not link its subagent")
	}

	// The agent id is whatever the reader lists; take it from the page link.
	agentPath := AgentURL(id, "a1")
	if body := get(http.MethodGet, TranscriptURL(id)).Body.String(); !strings.Contains(body, agentPath) {
		t.Fatalf("subagent link %s not in page", agentPath)
	}
	rec = get(http.MethodGet, agentPath)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SUBPROMPT") {
		t.Errorf("subagent transcript = %d", rec.Code)
	}
	if rec := get(http.MethodHead, TranscriptURL(id)); rec.Code != http.StatusOK {
		t.Errorf("HEAD = %d", rec.Code)
	}

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/t/unknown", http.StatusNotFound},
		{http.MethodGet, TranscriptURL(id) + "/agents/nope", http.StatusNotFound},
		{http.MethodGet, "/t/..%2f..", http.StatusNotFound},
		{http.MethodGet, "/t/x/agents/../../etc", http.StatusNotFound},
		{http.MethodGet, TranscriptURL(id) + "/agents/..%2f..%2ftranscript", http.StatusNotFound},
		{http.MethodGet, TranscriptURL(id) + "/agents/", http.StatusNotFound},
		{http.MethodGet, "/t/", http.StatusNotFound},
		{http.MethodGet, TranscriptURL(id) + "/extra", http.StatusNotFound},
		{http.MethodPost, TranscriptURL(id), http.StatusMethodNotAllowed},
	} {
		if rec := get(tc.method, tc.path); rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestHandler_TranscriptPartialAndFailed(t *testing.T) {
	// A line past the reader's size limit stops the read midway: what was
	// read is shown, under a warning.
	h, dir := transcriptFixture(t, okJSONL+strings.Repeat("x", 17<<20)+"\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, TranscriptURL(fixtureSessionID()), nil))
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "TRANSCRIPTHELLO") ||
		!strings.Contains(rec.Body.String(), "could not be read to the end") {
		t.Errorf("partial transcript = %d, want 200 with events and warning", rec.Code)
	}

	// No transcript at all: nothing to show, so 500.
	if err := os.Remove(filepath.Join(dir, "transcript.jsonl")); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, TranscriptURL(fixtureSessionID()), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("missing transcript = %d, want 500", rec.Code)
	}
}

func TestOverview_LinksTranscriptWithoutContent(t *testing.T) {
	h, _ := transcriptFixture(t, okJSONL)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `href="`+TranscriptURL(fixtureSessionID())+`"`) {
		t.Error("overview lacks the transcript link")
	}
	if !strings.Contains(body, "needs-server") {
		t.Error("overview lacks the static-file hint")
	}
	if strings.Contains(body, "TRANSCRIPTHELLO") || strings.Contains(body, "SUBPROMPT") || strings.Contains(body, os.TempDir()) {
		t.Error("overview leaks transcript content or a filesystem path")
	}
}
