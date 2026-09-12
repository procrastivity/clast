package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/procrastivity/clast/internal/source"
)

// TranscriptFormats implements source.TranscriptRenderer: this source
// renders exactly the one format its own Capture stamps (TranscriptFormat,
// capture.go).
func (s *Source) TranscriptFormats() []string {
	return []string{TranscriptFormat}
}

// contentBlock is one element of a block-array message content — the
// shape isHumanPrompt (scan.go) already discriminates against a plain
// string; this file also reads its "text" field for rendering.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// RenderTranscript implements source.TranscriptRenderer over the claude
// envelope: the transcript renderer seam (SURFACE V18, MODEL M9's one
// sanctioned transcript read). It reuses scan.go's own envelope decoding
// (entry, message) but, unlike scan.go's fact-only extraction (M10 —
// capture never materializes message text), this is the one place that
// does: `show --transcript` is the sanctioned read, never capture's own
// scan.
//
// A tolerant line-by-line scan: an unparseable line, a sidechain entry,
// or a meta bookkeeping entry contributes no turn, never an error — the
// same posture scan.go's own tolerant pass uses (M12). Only "user" and
// "assistant" entries render; a turn with no extractable text (a bare
// tool_result carrier, say) is skipped rather than rendered empty.
// maxChars, when > 0, caps each turn's text at that many runes.
func (s *Source) RenderTranscript(r io.Reader, maxChars int) ([]source.Turn, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var turns []source.Turn
	for scanner.Scan() {
		var e entry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.IsSidechain || e.IsMeta {
			continue
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}

		text := renderMessageText(e.Message)
		if text == "" {
			continue
		}
		if maxChars > 0 {
			text = truncateRunes(text, maxChars)
		}
		turns = append(turns, source.Turn{Role: e.Type, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claude: reading transcript: %w", err)
	}
	return turns, nil
}

// renderMessageText extracts displayable text from a message's content
// field: a plain JSON string IS the text (a human prompt, or a simple
// assistant reply with no blocks); a block array concatenates every
// "text"-typed block's own text, newline-joined, skipping tool_use/
// tool_result and any other block kind. Content that is neither — or a
// block array with no text blocks at all — renders as "".
func renderMessageText(raw json.RawMessage) string {
	var m message
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}

	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return text
	}

	var blocks []contentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" || blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// truncateRunes caps s at n runes — never n bytes, so a capped turn never
// ends mid-codepoint (V7).
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
