package devin

import (
	"io"

	"github.com/procrastivity/clast/internal/source"
	"github.com/tidwall/gjson"
)

// RenderTranscript implements source.TranscriptRenderer over
// devin-export-v1: the sanctioned transcript read for `show
// --transcript` — capped prompt/assistant prose in chain order. Only
// human prompts (is_user_input truthy) and assistant content render; a
// turn with no text (a bare tool call, an auto-"continue") is skipped
// rather than rendered empty, the same posture the claude renderer
// takes. maxChars, when > 0, caps each turn's text at that many runes —
// never bytes, so a cap never ends mid-codepoint (V7).
func (s *Source) RenderTranscript(r io.Reader, maxChars int) ([]source.Turn, error) {
	doc, err := parseExport(r)
	if err != nil {
		return nil, err
	}
	var turns []source.Turn
	for _, idx := range doc.activeChain() {
		n := doc.MessageNodes[idx]
		if !gjson.Valid(n.Chat) {
			continue
		}
		var role string
		switch gjson.Get(n.Chat, "role").String() {
		case "user":
			if !gjson.Get(n.Chat, "metadata.is_user_input").Bool() {
				continue
			}
			role = "user"
		case "assistant":
			role = "assistant"
		default:
			continue
		}
		text := gjson.Get(n.Chat, "content").String()
		if text == "" {
			continue
		}
		if maxChars > 0 {
			text = truncateRunes(text, maxChars)
		}
		turns = append(turns, source.Turn{Role: role, Text: text})
	}
	return turns, nil
}

// truncateRunes caps s at n runes — never n bytes, so a capped turn
// never ends mid-codepoint (V7). Same helper the claude renderer owns.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
