package amp

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/procrastivity/clast/internal/source"
)

// TranscriptFormats implements source.TranscriptRenderer (and the same
// method serves source.TranscriptReader): this source reads exactly the
// one format its own Capture stamps (TranscriptFormat, amp.go).
func (s *Source) TranscriptFormats() []string {
	return []string{TranscriptFormat}
}

// RenderTranscript implements source.TranscriptRenderer over the
// amp-export document: `show --transcript`'s deliberately thin prose
// view (SURFACE V18, MODEL M9's one sanctioned transcript read). The
// transcript is ONE JSON document, not a line stream — the whole doc
// parses or the read is an error; there is no per-line tolerance to
// fall back on.
//
// Turn mapping (claude parity): a user message's visible text blocks
// are a prompt turn, an assistant message's a reply turn — thinking,
// tool_use/tool_result, images, and hidden text blocks (injected
// context, not spoken prose) are dropped; info/compaction messages and
// unknown roles produce no turn; a message with no visible text
// contributes nothing. A mid-turn doc renders its complete prefix.
// maxChars, when > 0, caps each turn's text at that many runes.
func (s *Source) RenderTranscript(r io.Reader, maxChars int) ([]source.Turn, error) {
	doc, err := readDocFrom(r)
	if err != nil {
		return nil, err
	}
	var turns []source.Turn
	for _, m := range doc.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		text := renderableText(m.Content)
		if text == "" {
			continue
		}
		if maxChars > 0 {
			text = truncateRunes(text, maxChars)
		}
		turns = append(turns, source.Turn{Role: m.Role, Text: text})
	}
	return turns, nil
}

// renderableText is one message's prose for the transcript view: its
// non-hidden text blocks, newline-joined. A bare-string content (seen
// in neither epoch, tolerated anyway) renders as itself.
func renderableText(raw json.RawMessage) string {
	blocks, ok := contentBlocks(raw)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" || blk.Hidden || blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// truncateRunes caps s at n runes — never n bytes, so a capped turn
// never ends mid-codepoint (V7). Same helper as claude's.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
