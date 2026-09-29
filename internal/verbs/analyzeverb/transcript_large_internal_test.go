package analyzeverb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// largeTranscript builds a deterministic ~4 MB Claude transcript: pairs of
// assistant tool_use and user tool_result records with multi-KB results,
// every tenth result past both caps (over 200 lines and over 20 KB), and a
// meta record after each pair. It returns the JSONL and the pair count.
func largeTranscript(t *testing.T) ([]byte, int) {
	t.Helper()
	const pairs = 2000
	var b bytes.Buffer
	enc := func(v any) {
		line, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	enc(map[string]any{
		"type": "user", "uuid": "u0", "timestamp": "2026-09-21T14:00:00Z",
		"message": map[string]any{"role": "user", "content": "big session"},
	})
	for i := 0; i < pairs; i++ {
		id := fmt.Sprintf("toolu_%04d", i)
		enc(map[string]any{
			"type": "assistant", "uuid": fmt.Sprintf("a%d", i), "timestamp": "2026-09-21T14:00:01Z",
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{
					"type": "tool_use", "id": id, "name": "Bash",
					"input": map[string]any{"command": fmt.Sprintf("cat file-%04d.txt", i)},
				},
			}},
		})
		var res strings.Builder
		lines, fill := 20, "y" // multi-KB, under both caps
		if i%10 == 0 {
			lines, fill = 400, "x" // past both caps: 400 lines of 80 bytes is 32 KB
		}
		for l := 0; l < lines; l++ {
			fmt.Fprintf(&res, "%04d line %03d %s\n", i, l, strings.Repeat(fill, 60))
		}
		enc(map[string]any{
			"type": "user", "uuid": fmt.Sprintf("r%d", i), "timestamp": "2026-09-21T14:00:02Z",
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": id, "content": res.String()},
			}},
		})
		enc(map[string]any{
			"type": "system", "subtype": "turn_duration", "uuid": fmt.Sprintf("m%d", i),
			"timestamp": "2026-09-21T14:00:03Z", "durationMs": 1500,
		})
	}
	return b.Bytes(), pairs
}

func TestGatherAndRenderTranscript_Large(t *testing.T) {
	data, pairs := largeTranscript(t)
	if len(data) < 3<<20 {
		t.Fatalf("generated transcript is %d bytes, want a realistic 3 MB or more", len(data))
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := json.Marshal(journal.Session{Transcript: journal.TranscriptFingerprint{Format: "claude-jsonl"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), rec, 0o644); err != nil {
		t.Fatal(err)
	}

	page, err := GatherTranscript(dir, Session{ID: "big", Title: "big session"}, "")
	if err != nil {
		t.Fatalf("GatherTranscript: %v", err)
	}
	calls := 0
	for _, e := range page.Events {
		if e.Kind == source.KindToolCall && e.Tool.Result != nil {
			calls++
		}
	}
	if calls != pairs {
		t.Fatalf("paired tool calls = %d, want %d", calls, pairs)
	}

	var out bytes.Buffer
	if err := RenderTranscript(&out, page); err != nil {
		t.Fatalf("RenderTranscript: %v", err)
	}
	html := out.String()

	// Real transcripts rendered at about a quarter of their input size;
	// this synthetic one is mostly tool output, so it must at least shrink.
	if out.Len() >= len(data) {
		t.Errorf("rendered %d bytes from %d bytes of input, want smaller", out.Len(), len(data))
	}
	if got, want := strings.Count(html, "more lines"), pairs/10; got != want {
		t.Errorf("truncation notes = %d, want %d (one per over-cap result)", got, want)
	}
	if got := strings.Count(html, `<details class="tool"`); got != pairs {
		t.Errorf("tool details = %d, want %d", got, pairs)
	}
	if strings.Contains(html, `<details class="tool" open`) {
		t.Error("a tool call renders open, want collapsed")
	}
	longest := 0
	for _, m := range regexp.MustCompile(`(?s)<pre[^>]*>(.*?)</pre>`).FindAllStringSubmatch(html, -1) {
		if n := len(m[1]); n > longest {
			longest = n
		}
		if n := strings.Count(m[1], "\n"); n > maxResultLines {
			t.Fatalf("a <pre> holds %d lines, cap is %d", n, maxResultLines)
		}
	}
	// Escaping can only grow a capped text; 2x is ample for this ASCII data.
	if longest > 2*maxResultBytes {
		t.Errorf("longest <pre> is %d bytes, want at most %d", longest, 2*maxResultBytes)
	}
}
