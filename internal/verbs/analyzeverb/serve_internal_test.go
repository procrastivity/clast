package analyzeverb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
