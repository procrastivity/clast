package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// TranscriptFormat is what session.json's transcript.format records for
// this source's copies (M13): the understanding that produced the
// artifact.
const TranscriptFormat = "claude-jsonl"

// Capture implements source.Source: the file-tail posture (M13). The
// transcript streams through write verbatim, and the same pass computes
// the fingerprint and extracts the facts (M10) — hash, line count, and
// entry scan all see exactly the bytes the copy wrote, so a transcript
// growing mid-capture can never split the fingerprint from the facts.
// Recapture is the same call: the copy replaces the old one atomically
// (the write seam's discipline).
//
// Sidecars ride along: every file under <path minus .jsonl>/subagents/
// (agent-<id>.jsonl and its .meta.json) is copied verbatim to the same
// relative name. A sidecar that cannot be read is a diagnostic, not a
// failed capture — the main transcript is the artifact that matters.
func (s *Source) Capture(_ context.Context, d source.Discovered, write source.WriteArtifact) (source.Facts, []source.Diagnostic, error) {
	f, err := os.Open(d.Path)
	if err != nil {
		return source.Facts{}, nil, fmt.Errorf("claude: opening %s: %w", d.Path, err)
	}
	// Deliberate discard: read-only file.
	defer func() { _ = f.Close() }()

	cs := newCaptureScan()
	if err := write("transcript.jsonl", io.TeeReader(f, cs)); err != nil {
		return source.Facts{}, nil, err
	}
	cs.finish()

	facts := source.Facts{
		StartedAt:    cs.facts.StartedAt,
		LastActiveAt: cs.facts.LastActiveAt,
		Branch:       cs.facts.Branch,
		Counts: journal.SessionCounts{
			User:      cs.facts.UserCount,
			Assistant: cs.facts.AssistantCount,
		},
		// The open call's resolution: substantive = Claude replied at
		// least once in the main transcript (sidecars excluded).
		Substantive: cs.facts.AssistantCount >= 1,
		Transcript: journal.TranscriptFingerprint{
			Format: TranscriptFormat,
			Lines:  cs.lines,
			SHA256: hex.EncodeToString(cs.h.Sum(nil)),
		},
	}
	return facts, s.captureSidecars(d, write), nil
}

// captureSidecars copies the session's subagent sidecar files, if any.
// A session with no <uuid>/subagents/ directory is the common case and
// contributes nothing.
func (s *Source) captureSidecars(d source.Discovered, write source.WriteArtifact) []source.Diagnostic {
	subagentsDir := filepath.Join(strings.TrimSuffix(d.Path, ".jsonl"), "subagents")
	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []source.Diagnostic{{Path: subagentsDir, Err: err}}
	}

	var diags []source.Diagnostic
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(subagentsDir, e.Name())
		sf, err := os.Open(src)
		if err != nil {
			diags = append(diags, source.Diagnostic{Path: src, Err: err})
			continue
		}
		err = write("subagents/"+e.Name(), sf)
		// Deliberate discard: read-only file.
		_ = sf.Close()
		if err != nil {
			diags = append(diags, source.Diagnostic{Path: src, Err: err})
		}
	}
	return diags
}

// captureScan is the copy pass's write-side observer: it hashes every
// byte, counts lines the way source.FingerprintFile does (newline bytes,
// plus a non-empty unterminated final line), and folds each complete
// line through scanLine. A line past maxLineSize is counted as a bad
// line rather than buffered — same tolerance, bounded memory.
type captureScan struct {
	h        hash.Hash
	facts    facts
	seen     map[string]struct{}
	lines    int
	buf      []byte
	over     bool
	empty    bool
	lastByte byte
}

func newCaptureScan() *captureScan {
	return &captureScan{h: sha256.New(), seen: map[string]struct{}{}, empty: true}
}

// Write implements io.Writer for the tee; it never returns an error, so
// a copy can only fail on the real read or write side.
func (c *captureScan) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	c.empty = false
	c.lastByte = p[n-1]
	c.h.Write(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			c.appendPartial(p)
			break
		}
		c.appendPartial(p[:i])
		c.endLine()
		p = p[i+1:]
	}
	return n, nil
}

func (c *captureScan) appendPartial(b []byte) {
	if c.over {
		return
	}
	if len(c.buf)+len(b) > maxLineSize {
		c.over = true
		c.buf = c.buf[:0]
		return
	}
	c.buf = append(c.buf, b...)
}

func (c *captureScan) endLine() {
	c.lines++
	if c.over {
		c.facts.BadLines++
	} else {
		scanLine(c.buf, &c.facts, c.seen)
	}
	c.buf = c.buf[:0]
	c.over = false
}

// finish closes out an unterminated final line (M13's live-file case:
// the harness may be mid-append). Call exactly once, after the copy.
func (c *captureScan) finish() {
	if !c.empty && c.lastByte != '\n' {
		c.endLine()
	}
}
