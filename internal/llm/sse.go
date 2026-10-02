package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// maxSSELine caps one SSE line. A single content delta from a real
// endpoint is far smaller; the cap only guards against a runaway line.
const maxSSELine = 1 << 20

// streamEvent is the slice of one OpenAI-compatible chat.completion.chunk
// event this client reads. Events a proxy sends with an empty choices
// array (usage chunks), a role-only delta, or a finish_reason and no
// content decode to the zero value and are ignored.
type streamEvent struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readStream reads an SSE reply from body and returns the joined content
// deltas. Each content delta is reported to emit as PhaseDelta and each
// reasoning delta as PhaseThinking (emit may be nil). The stream ends at a
// "data: [DONE]" event or at a clean EOF: some proxies close the
// connection without [DONE], and that is success, not an error.
func readStream(body io.Reader, endpoint string, emit Observer) (string, error) {
	var (
		result strings.Builder
		data   []string
		done   bool
	)

	// flush handles the collected data lines as one event.
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		if payload == "[DONE]" {
			done = true
			return nil
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return fmt.Errorf("llm: %s returned malformed stream event: %w", endpoint, err)
		}
		if ev.Error != nil {
			return fmt.Errorf("llm: %s stream error: %s", endpoint, ev.Error.Message)
		}
		if len(ev.Choices) == 0 {
			return nil
		}
		d := ev.Choices[0].Delta
		if d.Content != "" {
			result.WriteString(d.Content)
			if emit != nil {
				emit(Event{Phase: PhaseDelta, Text: d.Content})
			}
		}
		thinking := d.ReasoningContent
		if thinking == "" {
			thinking = d.Reasoning
		}
		if thinking != "" && emit != nil {
			emit(Event{Phase: PhaseThinking, Text: thinking})
		}
		return nil
	}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	for !done && sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				return "", err
			}
		case strings.HasPrefix(line, ":"):
			// keepalive comment
		case strings.HasPrefix(line, "data:"):
			v := strings.TrimPrefix(line, "data:")
			data = append(data, strings.TrimPrefix(v, " "))
		}
		// event:, id:, retry: and unknown fields are ignored.
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("llm: reading stream from %s: %w", endpoint, err)
	}
	if !done {
		// EOF with an event still open: treat it as complete.
		if err := flush(); err != nil {
			return "", err
		}
	}
	return result.String(), nil
}
