package analyzeverb

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
