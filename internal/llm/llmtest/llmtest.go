// Package llmtest is an httptest-backed OpenAI-compatible /chat/completions
// stub for internal/llm's Client and, later, the wake/brief/retro flows
// built on it. It is the llm-verbs Matter's own oracle-free fixture (SEED
// CARDS "llm-verbs", H4): there is no real endpoint to record against, so
// tests drive this stub instead — canned completion responses, captured
// requests for asserting what the client sent (model, messages), and a
// raw-response override for the non-2xx and malformed-JSON failure modes.
//
// Naming mirrors internal/journal/journaltest: a small, testing.TB-scoped
// fixture builder living in a "<package>test" sibling of the package it
// fixtures.
package llmtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Message is one OpenAI chat-format message, as sent or received over the
// wire. A deliberate duplicate of internal/llm's own unexported message
// type: this package fixtures the wire format, not internal/llm's Go
// API, so it decodes the request body independently rather than
// depending on internal/llm's internals.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CapturedRequest is one /chat/completions call the stub received, decoded
// enough for assertions on request shape: the model and messages the
// client sent, and the raw Authorization header for asserting the bearer
// token.
type CapturedRequest struct {
	Model         string
	Messages      []Message
	Authorization string
}

// Server is a running OpenAI-compatible /chat/completions stub. Point a
// Client at it by setting the resolved config's llm.base_url to Server.URL.
type Server struct {
	t   testing.TB
	srv *httptest.Server

	mu       sync.Mutex
	requests []CapturedRequest

	// canned is the assistant message content every request receives
	// until Fail or RespondRaw overrides it.
	canned string

	// raw, when rawSet, overrides the response entirely: status and body
	// verbatim, bypassing the canned-completion JSON shape. Used for the
	// non-2xx and malformed-JSON failure modes.
	rawSet    bool
	rawStatus int
	rawBody   string
}

// New starts a stub that answers every /chat/completions request with a
// single canned choice carrying content as the assistant message. The
// server is closed automatically via t.Cleanup.
func New(t testing.TB, content string) *Server {
	t.Helper()
	s := &Server{t: t, canned: content}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the stub's base URL — set llm.base_url to exactly this.
func (s *Server) URL() string {
	return s.srv.URL
}

// SetResponse changes the canned assistant content subsequent requests
// receive, clearing any prior Fail/RespondRaw override.
func (s *Server) SetResponse(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canned = content
	s.rawSet = false
}

// Fail switches the stub to answer every subsequent request with
// statusCode and body — the non-2xx failure mode.
func (s *Server) Fail(statusCode int, body string) {
	s.RespondRaw(statusCode, body)
}

// RespondRaw switches the stub to answer every subsequent request with
// statusCode and body written verbatim, bypassing the canned-completion
// JSON entirely. Pass a 2xx statusCode with a non-JSON (or truncated
// JSON) body to exercise the malformed-response failure mode instead of
// the non-2xx one.
func (s *Server) RespondRaw(statusCode int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rawSet = true
	s.rawStatus = statusCode
	s.rawBody = body
}

// Requests returns every request captured so far, in arrival order.
func (s *Server) Requests() []CapturedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CapturedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// wireRequest is the subset of the OpenAI chat-completions request body
// the stub decodes.
type wireRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// wireChoice/wireResponse mirror the response shape internal/llm.Client
// parses: one choice's message.
type wireChoice struct {
	Message Message `json:"message"`
}

type wireResponse struct {
	Choices []wireChoice `json:"choices"`
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.t.Helper()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Errorf("llmtest: reading request body: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req wireRequest
	// A malformed request body is a captured-request bug in the client
	// under test, not something the stub should hide — decode errors are
	// ignored here (req stays zero-valued) so Requests() still records
	// the attempt for the test to notice via an empty Model/Messages.
	_ = json.Unmarshal(body, &req)

	s.mu.Lock()
	s.requests = append(s.requests, CapturedRequest{
		Model:         req.Model,
		Messages:      req.Messages,
		Authorization: r.Header.Get("Authorization"),
	})
	rawSet, rawStatus, rawBody := s.rawSet, s.rawStatus, s.rawBody
	canned := s.canned
	s.mu.Unlock()

	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}

	if rawSet {
		w.WriteHeader(rawStatus)
		_, _ = w.Write([]byte(rawBody))
		return
	}

	resp := wireResponse{Choices: []wireChoice{{Message: Message{Role: "assistant", Content: canned}}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
