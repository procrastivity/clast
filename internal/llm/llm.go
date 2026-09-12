// Package llm is the shared OpenAI-compatible chat-completions client the
// wake/brief/retro verb forms speak to the configured LLM endpoint
// (SURFACE V12): one call shape — a system/user prompt pair in, the
// assistant's reply text out. No provider abstraction (V12 rejected a
// second provider until one actually exists), no streaming, no retries,
// no options struct beyond what the three verbs need.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/config"
)

// APIKeyEnvVar is the one secret env var V31/D8 names — the client reads
// no other credential source.
const APIKeyEnvVar = "CLAST_LLM_API_KEY"

// configKey is the tool-config section V31 carries the endpoint under.
const configKey = "llm"

// DefaultTimeout bounds one Complete call end-to-end (connect through
// response body fully read). 30s: generous enough for a real completion
// (retro's summaries and wake's drafts are short generations, not
// long-form), short enough that a verb never hangs a terminal
// indefinitely on a stalled endpoint. Recorded as a wip finding per the
// step brief — no SURFACE clause fixes this number.
const DefaultTimeout = 30 * time.Second

// chatCompletionsPath is the one endpoint path V12 names, joined onto
// llm.base_url.
const chatCompletionsPath = "/chat/completions"

// Client speaks one endpoint: POST {llm.base_url}/chat/completions in the
// OpenAI chat-completions request/response shape, authenticated by
// CLAST_LLM_API_KEY as a bearer token.
type Client struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// NewClient resolves llm.base_url and llm.model from cfg (SURFACE V31)
// and CLAST_LLM_API_KEY from the environment, and constructs a Client
// ready to call Complete. It returns a clasterr.Error when a
// prerequisite is missing, so callers get a clear, code-bearing failure
// rather than a runtime surprise on the first Complete call:
//   - "validation.llm-not-configured" — llm.base_url and/or llm.model
//     are empty or absent in the resolved config.
//   - "validation.llm-api-key-missing" — CLAST_LLM_API_KEY is unset or
//     empty in the environment.
//
// A malformed llm config section (wrong YAML type) is a plain error, not
// a clasterr.Error — the same posture journal.ConfiguredCutoff and
// query.ConfiguredSince use for a config-shape defect; callers that want
// it surfaced as clasterr wrap it themselves (the "validation.config"
// pattern config.Load callers already use).
func NewClient(cfg config.Config) (*Client, error) {
	baseURL, model, err := llmConfigValues(cfg)
	if err != nil {
		return nil, err
	}

	var missing []string
	if baseURL == "" {
		missing = append(missing, "llm.base_url")
	}
	if model == "" {
		missing = append(missing, "llm.model")
	}
	if len(missing) > 0 {
		return nil, clasterr.New("validation.llm-not-configured",
			fmt.Sprintf("llm endpoint not configured: set %s in config.yaml", strings.Join(missing, " and ")))
	}

	apiKey := os.Getenv(APIKeyEnvVar)
	if apiKey == "" {
		return nil, clasterr.New("validation.llm-api-key-missing",
			fmt.Sprintf("%s is not set", APIKeyEnvVar))
	}

	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: DefaultTimeout},
	}, nil
}

// llmConfigValues reads cfg's "llm" section and returns its base_url and
// model strings, "" when the section, or a given key within it, is
// absent — NewClient's presence check turns that into
// validation.llm-not-configured. A present-but-wrong-shaped section or
// key (not a mapping, not a string) is a plain error naming the key and
// the offending Go type, mirroring capture.autoDismissNoop's own
// yaml.v3-decode-shape handling (nested mappings decode as either
// map[string]any or config.Config depending on caller, both accepted).
func llmConfigValues(cfg config.Config) (baseURL, model string, err error) {
	raw, present := cfg[configKey]
	if !present || raw == nil {
		return "", "", nil
	}

	var section map[string]any
	switch m := raw.(type) {
	case map[string]any:
		section = m
	case config.Config:
		section = m
	default:
		return "", "", fmt.Errorf("llm: config key %q must be a mapping, got %T", configKey, raw)
	}

	baseURL, err = stringField(section, "base_url")
	if err != nil {
		return "", "", err
	}
	model, err = stringField(section, "model")
	if err != nil {
		return "", "", err
	}
	return baseURL, model, nil
}

func stringField(section map[string]any, key string) (string, error) {
	v, present := section[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("llm: config key %q must be a string, got %T", configKey+"."+key, v)
	}
	return s, nil
}

// message is one OpenAI chat-format message.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the OpenAI-compatible /chat/completions request body
// V12 fixes: model plus a messages array carrying the system/user prompt
// pair every flow uses.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

// chatResponse is the slice of the OpenAI-compatible response this
// client reads: the first choice's assistant message content. Every
// other field a real endpoint sends (usage, finish_reason, id, ...) is
// ignored — the surface this client needs is exactly this much.
type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

// Complete sends one system/user prompt pair — the posture every
// wake/brief/retro flow uses — to the configured endpoint and returns the
// first choice's assistant message content verbatim.
//
// A non-2xx response or a malformed response body is a plain error
// naming the endpoint and, for non-2xx, the status and response body —
// enough for a verb to report a clear failure without this package
// guessing at a clasterr code a later verb Matter hasn't chosen yet.
func (c *Client) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	endpoint := c.baseURL + chatCompletionsPath

	reqBody, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("llm: encoding request to %s: %w", endpoint, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("llm: building request to %s: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: calling %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: reading response from %s: %w", endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm: %s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(respBody)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("llm: %s returned malformed JSON: %w", endpoint, err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm: %s returned no choices", endpoint)
	}

	return parsed.Choices[0].Message.Content, nil
}
