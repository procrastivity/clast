package pi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// Capture implements source.Source: the file-tail posture (M13). The
// Pi file streams through write verbatim as transcript.jsonl, and the
// same pass (an io.TeeReader into a scan observer) computes the
// fingerprint and every fact from exactly the bytes the copy wrote. The
// file is opened once and never re-read, so a live file growing
// mid-capture cannot split the fingerprint from the facts.
//
// The header is re-validated from the streamed first line (shared
// readHeader: id rule, version gate); a failure aborts the copy and is
// an item error, as is a header id that differs from d.NativeID (file
// replaced since Discover).
func (s *Source) Capture(ctx context.Context, d source.Discovered, write source.WriteArtifact) (source.Facts, []source.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return source.Facts{}, nil, err
	}
	f, err := os.Open(d.Path)
	if err != nil {
		return source.Facts{}, nil, fmt.Errorf("pi: opening %s: %w", d.Path, err)
	}
	// Deliberate discard: read-only file.
	defer func() { _ = f.Close() }()

	sc := newScan(d.NativeID)
	werr := write("transcript.jsonl", io.TeeReader(&ctxReader{ctx: ctx, r: f}, sc))
	if err := ctx.Err(); err != nil {
		return source.Facts{}, nil, err
	}
	if sc.fatal != nil {
		return source.Facts{}, nil, fmt.Errorf("pi: %s: %w", d.Path, sc.fatal)
	}
	if werr != nil {
		return source.Facts{}, nil, werr
	}
	sc.finish()
	if sc.fatal != nil {
		return source.Facts{}, nil, fmt.Errorf("pi: %s: %w", d.Path, sc.fatal)
	}

	r := sc.result()
	var diags []source.Diagnostic
	if r.badLines > 0 {
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: fmt.Errorf(
			"pi: %s: %d unparseable line(s) skipped (first at line %d)", d.Path, r.badLines, r.firstBad,
		)})
	}
	if r.brokenAt != "" {
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: fmt.Errorf(
			"pi: %s: leaf path broken at %s; counts are whole-file", d.Path, r.brokenAt,
		)})
	}
	return source.Facts{
		StartedAt:    sc.hdr.Timestamp,
		LastActiveAt: r.lastActive,
		Counts:       journal.SessionCounts{User: r.user, Assistant: r.assistant},
		Substantive:  r.substantive,
		Transcript: journal.TranscriptFingerprint{
			Format: Format,
			Lines:  sc.lines,
			SHA256: hex.EncodeToString(sc.h.Sum(nil)),
		},
	}, diags, nil
}

// ctxReader fails reads once ctx is done, so a cancelled capture stops
// mid-copy with ctx's own error.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

var errNoHeader = errors.New("no complete session header")
