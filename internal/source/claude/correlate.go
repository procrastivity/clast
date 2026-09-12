package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/procrastivity/clast/internal/source"
)

// Correlate binds a session to the working directory it started in: the
// first cwd field in the transcript. In-transcript evidence beats
// decoding the project directory's slug (the seed card's settled call —
// the slug flattens / and . to - irreversibly), and Claude Code stamps
// cwd on every conversation entry, so the first one found is the
// session's own report. "" means the transcript carries no cwd at all
// (nothing but bookkeeping lines so far): the session is capturable,
// just projectless.
//
// The read stops at the first cwd rather than reusing scanTranscript's
// full pass — correlation is its own duty (M12) and needs one field, so
// it never pays for a full-file scan. Unparseable lines are skipped
// silently here; scanTranscript is the pass that counts them.
func (s *Source) Correlate(_ context.Context, d source.Discovered) (string, []source.Diagnostic, error) {
	f, err := os.Open(d.Path)
	if err != nil {
		return "", nil, fmt.Errorf("claude: opening %s: %w", d.Path, err)
	}
	// Deliberate discard: read-only file.
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		var e struct {
			IsSidechain bool   `json:"isSidechain"`
			CWD         string `json:"cwd"`
		}
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if !e.IsSidechain && e.CWD != "" {
			return e.CWD, nil, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", []source.Diagnostic{{Path: d.Path, Err: err}}, nil
	}
	return "", nil, nil
}
