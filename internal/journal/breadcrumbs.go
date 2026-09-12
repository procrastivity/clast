package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// dayShardLayout is the local-calendar-date layout used for both the
// sessions/ directory shard (M8) and the breadcrumb filename date below.
const dayShardLayout = "2006-01-02"

// hostname is os.Hostname, indirected (C2.6) so tests can fake the local
// machine name without depending on the real host's.
var hostname = os.Hostname

// AppendBreadcrumb appends one breadcrumb line to this machine's file,
// breadcrumbs/YYYY-MM-DD.<machine>.jsonl. The date is the local calendar
// date at write time (MODEL §4) — a sharding decision made once per
// append, independent of b.At; it is never recomputed later, the same
// posture M8 fixes for the sessions/ shard. <machine> comes from
// os.Hostname behind the seam above. The file is opened O_APPEND so this
// machine's appends never collide with another machine's file (M5: one
// appender per file; readers glob across machines instead). The first
// append also triggers EnsureRoot's init-on-first-write (MODEL M6).
func AppendBreadcrumb(root string, b Breadcrumb) error {
	machine, err := hostname()
	if err != nil {
		return fmt.Errorf("journal: resolving machine name: %w", err)
	}
	if err := EnsureRoot(root); err != nil {
		return err
	}

	shard := now().Local().Format(dayShardLayout)
	path := BreadcrumbsPath(root, shard, machine)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("journal: creating %s: %w", filepath.Dir(path), err)
	}

	line, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("journal: encoding breadcrumb: %w", err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("journal: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("journal: appending to %s: %w", path, err)
	}
	return nil
}

// BreadcrumbEntry is one breadcrumb as read back, together with the
// machine its filename attributes it to. Machine is parsed from the
// filename (YYYY-MM-DD.<machine>.jsonl), never stored in the line itself
// — a judgment call for this step: the line shape stays exactly the MODEL
// §4 example, and provenance rides along on the wrapper instead.
type BreadcrumbEntry struct {
	Breadcrumb
	Machine string
}

// BreadcrumbDiagnostic names one unparseable breadcrumb line, skipped
// during a read rather than failing it — the walk's malformed-document
// posture (store brief, MODEL §7's "tolerant of what it doesn't own"
// spirit), applied here to our own JSONL: a garbage line is a counted
// diagnostic, never a fatal error.
type BreadcrumbDiagnostic struct {
	Path string
	Line int
	Err  error
}

// ReadBreadcrumbs returns every breadcrumb filed under shard, merged
// across every machine that has written one: readers glob
// YYYY-MM-DD.*.jsonl rather than picking one machine's file (M5). Entries
// come back in file-then-line order (files sorted by name, i.e. by
// machine); day-bucket attribution from each entry's At plus the current
// day_cutoff is a later step's job (M8), not this read's. No file for
// shard is not an error: it returns no entries. An unparseable line is
// skipped and reported in diags rather than failing the read.
func ReadBreadcrumbs(root, shard string) (entries []BreadcrumbEntry, diags []BreadcrumbDiagnostic, err error) {
	pattern := filepath.Join(root, "breadcrumbs", shard+".*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("journal: globbing %s: %w", pattern, err)
	}
	sort.Strings(matches) // deterministic order across machines.

	for _, path := range matches {
		machine := machineFromBreadcrumbFilename(shard, path)
		fileEntries, fileDiags, err := readBreadcrumbFile(path, machine)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntries...)
		diags = append(diags, fileDiags...)
	}
	return entries, diags, nil
}

// machineFromBreadcrumbFilename recovers <machine> from
// YYYY-MM-DD.<machine>.jsonl given the shard date it was globbed under.
func machineFromBreadcrumbFilename(shard, path string) string {
	base := filepath.Base(path)
	trimmed := strings.TrimSuffix(base, ".jsonl")
	return strings.TrimPrefix(trimmed, shard+".")
}

// readBreadcrumbFile parses one machine's breadcrumb file line by line,
// counting an unparseable line as a diagnostic instead of failing.
func readBreadcrumbFile(path, machine string) ([]BreadcrumbEntry, []BreadcrumbDiagnostic, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("journal: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var entries []BreadcrumbEntry
	var diags []BreadcrumbDiagnostic

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue // a blank line (e.g. trailing newline) is not garbage.
		}
		var b Breadcrumb
		if err := json.Unmarshal(line, &b); err != nil {
			diags = append(diags, BreadcrumbDiagnostic{Path: path, Line: lineNo, Err: err})
			continue
		}
		entries = append(entries, BreadcrumbEntry{Breadcrumb: b, Machine: machine})
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("journal: reading %s: %w", path, err)
	}
	return entries, diags, nil
}
