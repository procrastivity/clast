package llm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/llm/llmtest"
)

func cfgWith(baseURL, model string) config.Config {
	return config.Config{
		"llm": map[string]any{
			"base_url": baseURL,
			"model":    model,
		},
	}
}

func TestComplete_HappyPath(t *testing.T) {
	stub := llmtest.New(t, "hello from the assistant")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := client.Complete(context.Background(), "you are a helpful drafting assistant", "draft me a thing")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "hello from the assistant" {
		t.Fatalf("Complete content = %q, want %q", got, "hello from the assistant")
	}

	reqs := stub.Requests()
	if len(reqs) != 1 {
		t.Fatalf("stub captured %d requests, want 1", len(reqs))
	}
	req := reqs[0]

	if req.Model != "gpt-test" {
		t.Errorf("request model = %q, want %q", req.Model, "gpt-test")
	}
	if req.Authorization != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", req.Authorization, "Bearer sk-test-key")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("request carried %d messages, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "you are a helpful drafting assistant" {
		t.Errorf("system message = %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "draft me a thing" {
		t.Errorf("user message = %+v", req.Messages[1])
	}
}

func TestComplete_RequestURL(t *testing.T) {
	stub := llmtest.New(t, "ok")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	// base_url carries a trailing slash — NewClient must not double it
	// up into "//chat/completions".
	client, err := llm.NewClient(cfgWith(stub.URL()+"/", "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Complete(context.Background(), "sys", "usr"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(stub.Requests()) != 1 {
		t.Fatalf("expected the stub to receive exactly one request at /chat/completions")
	}
}

func TestNewClient_MissingConfig(t *testing.T) {
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	cases := []struct {
		name string
		cfg  config.Config
	}{
		{"no llm section at all", config.Config{}},
		{"llm section present but empty", config.Config{"llm": map[string]any{}}},
		{"base_url empty, model set", cfgWith("", "gpt-test")},
		{"model empty, base_url set", cfgWith("http://example.invalid", "")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := llm.NewClient(tc.cfg)
			if err == nil {
				t.Fatal("NewClient: want error, got nil")
			}
			var terr *clasterr.Error
			if !errors.As(err, &terr) {
				t.Fatalf("NewClient error is not a *clasterr.Error: %v (%T)", err, err)
			}
			if terr.Code != "validation.llm-not-configured" {
				t.Errorf("Code = %q, want %q", terr.Code, "validation.llm-not-configured")
			}
		})
	}
}

func TestNewClient_MissingAPIKey(t *testing.T) {
	t.Setenv("CLAST_LLM_API_KEY", "")

	_, err := llm.NewClient(cfgWith("http://example.invalid", "gpt-test"))
	if err == nil {
		t.Fatal("NewClient: want error, got nil")
	}
	var terr *clasterr.Error
	if !errors.As(err, &terr) {
		t.Fatalf("NewClient error is not a *clasterr.Error: %v (%T)", err, err)
	}
	if terr.Code != "validation.llm-api-key-missing" {
		t.Errorf("Code = %q, want %q", terr.Code, "validation.llm-api-key-missing")
	}
}

func TestComplete_NonTwoXX(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.Fail(500, `{"error": "boom"}`)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Complete(context.Background(), "sys", "usr")
	if err == nil {
		t.Fatal("Complete: want error, got nil")
	}
	if !strings.Contains(err.Error(), "/chat/completions") {
		t.Errorf("error %q does not name the endpoint", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not surface the response body", err.Error())
	}
}

func TestComplete_MalformedJSON(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.RespondRaw(200, "not json at all {{{")
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Complete(context.Background(), "sys", "usr")
	if err == nil {
		t.Fatal("Complete: want error, got nil")
	}
	if !strings.Contains(err.Error(), "/chat/completions") {
		t.Errorf("error %q does not name the endpoint", err.Error())
	}
}

func TestComplete_NoChoices(t *testing.T) {
	stub := llmtest.New(t, "unused")
	stub.RespondRaw(200, `{"choices": []}`)
	t.Setenv("CLAST_LLM_API_KEY", "sk-test-key")

	client, err := llm.NewClient(cfgWith(stub.URL(), "gpt-test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Complete(context.Background(), "sys", "usr")
	if err == nil {
		t.Fatal("Complete: want error, got nil")
	}
}
