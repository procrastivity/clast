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
	"time"
)

// dayShardLayout is the local-calendar-date layout used for both the
// sessions/ directory shard (M8) and the breadcrumb filename date below.
const dayShardLayout = "2006-01-02"

// hostname is os.Hostname, indirected (C2.6) so tests can fake the local
// machine name without depending on the real host's.
var hostname = os.Hostname

// AppendBreadcrumb appends one breadcrumb line to this machine's file,
// breadcrumbs/YYYY-MM-DD.<machine>.jsonl. The file date is b.At's own
// local calendar date, not an independent clock read: for the only real
// writer At IS write time, so MODEL §4's "local calendar date at write
// time" still holds, and deriving the file date from At — rather than
// reading the clock again — is what makes ReadBreadcrumbsForDay's
// day/day+1 file window unbreakable: the file a crumb lands in and the
// At it filters on can never disagree by construction. It is a sharding
// decision made once per append and never recomputed later, the same
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

	fileDate := b.At.Local().Format(dayShardLayout)
	path := BreadcrumbsPath(root, fileDate, machine)
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

// Diagnostic names one malformed or unreadable thing this package chose
// to skip and count rather than fail on: one JSONL breadcrumb line, or
// (walk.go) one session directory. One shared type rather than a bespoke
// one per source, so every tolerant read in this package (store brief,
// MODEL §7's "tolerant of what it doesn't own" spirit) reports the same
// shape. Line is 0 when the diagnostic isn't line-scoped.
type Diagnostic struct {
	Path string
	Line int
	Err  error
}

// ReadBreadcrumbs returns every breadcrumb filed under fileDate's on-disk
// breadcrumb files, merged across every machine that has written one:
// readers glob YYYY-MM-DD.*.jsonl rather than picking one machine's file
// (M5). fileDate names a FILE, not a work day — the parameter is named
// that way on purpose (rather than "shard" or "day") to keep the trap
// visible: a crumb filed under fileDate can still belong to a different
// day's bucket once day_cutoff is applied (a 02:30 write is still
// "yesterday" under a 04:00 cutoff, M8), so this function alone is never
// the right way to answer "what happened on day X" — that is
// ReadBreadcrumbsForDay below. fileDate is validated as a bare
// YYYY-MM-DD before it ever reaches filepath.Glob, so a caller can't
// smuggle glob metacharacters (*, ?, [...]) into the pattern through an
// unvalidated day string.
//
// Entries come back in file-then-line order (files sorted by name, i.e.
// by machine). No file for fileDate is not an error: it returns no
// entries. An unparseable line is skipped and reported in diags rather
// than failing the read.
func ReadBreadcrumbs(root, fileDate string) (entries []BreadcrumbEntry, diags []Diagnostic, err error) {
	if _, err := time.Parse(dayShardLayout, fileDate); err != nil {
		return nil, nil, fmt.Errorf("journal: %q is not a valid YYYY-MM-DD file date: %w", fileDate, err)
	}

	pattern := filepath.Join(root, "breadcrumbs", fileDate+".*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("journal: globbing %s: %w", pattern, err)
	}
	sort.Strings(matches) // deterministic order across machines.

	for _, path := range matches {
		machine := machineFromBreadcrumbFilename(fileDate, path)
		fileEntries, fileDiags, err := readBreadcrumbFile(path, machine)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntries...)
		diags = append(diags, fileDiags...)
	}
	return entries, diags, nil
}

// ReadBreadcrumbsForDay returns every breadcrumb whose work-day bucket —
// c.DayOf(entry.At), M8 — is day. This is the day-bucketed read most
// callers actually want, as opposed to ReadBreadcrumbs' raw,
// filename-only read.
//
// It reads day's own file date AND the next calendar day's (day+1):
// AppendBreadcrumb shards a crumb's file by the calendar date at write
// time, so a crumb written at (say) 02:30 lands in day+1's file — but
// under a cutoff later than 02:30 (the default 04:00), that crumb still
// belongs to day's work-day bucket. Reading only day's own file would
// silently miss it. Every candidate entry from both files is then
// filtered on c.DayOf(entry.At) == day, which also correctly drops the
// day+1 entries that genuinely belong to day+1 (e.g. one written at
// 09:00 stays on day+1 under a 04:00 cutoff).
func ReadBreadcrumbsForDay(root string, day Day, c Cutoff) (entries []BreadcrumbEntry, diags []Diagnostic, err error) {
	next, err := day.addDays(1)
	if err != nil {
		return nil, nil, err
	}

	for _, fileDate := range [2]Day{day, next} {
		fileEntries, fileDiags, err := ReadBreadcrumbs(root, string(fileDate))
		if err != nil {
			return nil, nil, err
		}
		diags = append(diags, fileDiags...)
		for _, e := range fileEntries {
			if c.DayOf(e.At) == day {
				entries = append(entries, e)
			}
		}
	}
	return entries, diags, nil
}

// machineFromBreadcrumbFilename recovers <machine> from
// YYYY-MM-DD.<machine>.jsonl given the file date it was globbed under.
func machineFromBreadcrumbFilename(fileDate, path string) string {
	base := filepath.Base(path)
	trimmed := strings.TrimSuffix(base, ".jsonl")
	return strings.TrimPrefix(trimmed, fileDate+".")
}

// readBreadcrumbFile parses one machine's breadcrumb file line by line,
// counting an unparseable line as a diagnostic instead of failing.
func readBreadcrumbFile(path, machine string) ([]BreadcrumbEntry, []Diagnostic, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("journal: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var entries []BreadcrumbEntry
	var diags []Diagnostic

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
			diags = append(diags, Diagnostic{Path: path, Line: lineNo, Err: err})
			continue
		}
		entries = append(entries, BreadcrumbEntry{Breadcrumb: b, Machine: machine})
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("journal: reading %s: %w", path, err)
	}
	return entries, diags, nil
}
