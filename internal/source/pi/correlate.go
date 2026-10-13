package pi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/procrastivity/clast/internal/source"
)

// Correlate binds a session to the header cwd, read from line 1 only: a
// non-empty absolute path, passed through lexically (no symlink
// resolution, no slug decoding). Missing or relative cwd is "" —
// projectless, still capturable. An unreadable file is an error (the
// verb skips that item); a header that no longer passes the gate (file
// changed since Discover) yields "" with a diagnostic.
func (s *Source) Correlate(ctx context.Context, d source.Discovered) (string, []source.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	h, err := readHeaderFile(d.Path)
	if err != nil {
		if errors.Is(err, errSilentSkip) {
			return "", nil, nil
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return "", nil, fmt.Errorf("pi: reading %s: %w", d.Path, err)
		}
		return "", []source.Diagnostic{{Path: d.Path, Err: fmt.Errorf("pi: %s: %w", d.Path, err)}}, nil
	}
	if h.CWD == "" || !filepath.IsAbs(h.CWD) {
		return "", nil, nil
	}
	return h.CWD, nil, nil
}
