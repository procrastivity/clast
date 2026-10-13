package pi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// candidate is one header-valid file before duplicate resolution.
type candidate struct {
	id      string
	path    string
	modTime time.Time
}

// Discover enumerates Pi sessions: for each candidate root, *.jsonl
// files directly in the root (flat layout) and one level down in each
// subdirectory (per-cwd layout). Symlinked files and directories are
// followed. Only line 1 of each file is read. See the contract, section
// 2, for the outcome table.
func (s *Source) Discover(ctx context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	r := s.resolveRoots()
	if r.err != nil {
		return nil, r.diags, r.err
	}
	diags := r.diags
	var cands []candidate
	enumerated := 0
	var rootErrs []error
	var rootDiags []source.Diagnostic

	w := &walker{ctx: ctx, diags: &diags, cands: &cands}
	for _, root := range r.roots {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			rootErrs = append(rootErrs, err)
			rootDiags = append(rootDiags, source.Diagnostic{Path: root, Err: err})
			continue
		}
		enumerated++
		if err := w.walkDir(root, entries, true); err != nil {
			return nil, nil, err
		}
	}
	if enumerated == 0 && len(rootErrs) > 0 {
		return nil, diags, rootErrs[0]
	}
	diags = append(diags, rootDiags...)
	if len(cands) == 0 {
		return nil, diags, nil
	}

	found, dupDiags := resolveDuplicates(cands)
	return found, append(diags, dupDiags...), nil
}

type walker struct {
	ctx   context.Context
	diags *[]source.Diagnostic
	cands *[]candidate
}

func (w *walker) diag(path string, err error) {
	*w.diags = append(*w.diags, source.Diagnostic{Path: path, Err: err})
}

// walkDir handles one directory's entries. At the root level
// subdirectories are descended once; below that they are ignored.
func (w *walker) walkDir(dir string, entries []os.DirEntry, isRoot bool) error {
	for _, e := range entries {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), ".jsonl") {
			w.file(path)
			continue
		}
		if !isRoot {
			continue
		}
		// Follow directory symlinks; a broken or unreadable non-.jsonl
		// neighbour is not ours to comment on.
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		sub, err := os.ReadDir(path)
		if err != nil {
			w.diag(path, err)
			continue
		}
		if err := w.walkDir(path, sub, false); err != nil {
			return err
		}
	}
	return nil
}

// file applies the outcome table to one *.jsonl path.
func (w *walker) file(path string) {
	info, err := os.Stat(path) // follows symlinks
	if err != nil {
		w.diag(path, err)
		return
	}
	if !info.Mode().IsRegular() {
		return // directory named *.jsonl, fifo, socket...
	}
	h, err := readHeaderFile(path)
	if err != nil {
		if errors.Is(err, errSilentSkip) {
			return
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			w.diag(path, err)
			return
		}
		w.diag(path, fmt.Errorf("pi: %s: %w", path, err))
		return
	}
	if fid, ok := filenameID(path); !ok || fid != h.ID {
		w.diag(path, fmt.Errorf("pi: %s: filename id %q does not match header id %q", path, fid, h.ID))
	}
	*w.cands = append(*w.cands, candidate{id: h.ID, path: path, modTime: info.ModTime()})
}

// filenameID splits <ts>_<id>.jsonl at the first underscore. When there
// is none, it returns the base name sans suffix and false.
func filenameID(path string) (string, bool) {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	_, id, ok := strings.Cut(base, "_")
	if !ok {
		return base, false
	}
	return id, true
}

// resolveDuplicates keeps one file per header id: newest mtime, ties by
// smallest path. Losers get a diagnostic. Output keeps walk order.
func resolveDuplicates(cands []candidate) ([]source.Discovered, []source.Diagnostic) {
	winner := map[string]candidate{}
	for _, c := range cands {
		w, ok := winner[c.id]
		if !ok || c.modTime.After(w.modTime) || (c.modTime.Equal(w.modTime) && c.path < w.path) {
			winner[c.id] = c
		}
	}
	var found []source.Discovered
	var diags []source.Diagnostic
	for _, c := range cands {
		w := winner[c.id]
		if c.path != w.path {
			diags = append(diags, source.Diagnostic{
				Path: c.path,
				Err:  fmt.Errorf("pi: %s: duplicate session id %s, using %s", c.path, c.id, w.path),
			})
			continue
		}
		found = append(found, source.Discovered{NativeID: c.id, Path: c.path, ModTime: c.modTime})
	}
	return found, diags
}
