// Package show implements `clast plumbing show <session>` (SURFACE V18):
// the single-session view. Default mode is facts, curation state
// (+staleness), and the curated entry's body when one exists;
// --transcript renders the turns instead, through the session's
// transcript.format renderer — the one sanctioned transcript read (M9).
package show

import (
	"fmt"
	"os"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
)

// Result is what Run resolved for one locator.
type Result struct {
	Item journal.WalkItem
	// Entry is the parsed entry.md, or nil when the session has none
	// (captured or dismissed, or a curated session whose entry.md
	// somehow went missing — Walk only stats it, MODEL §4/M9).
	Entry *entry.Entry
}

// Run resolves locator against root's journal (journal.Resolve, V4:
// not-found.session, validation.ambiguous-locator naming candidates) and
// loads its entry.md when one exists.
func Run(root, locator string) (Result, error) {
	items, _, err := journal.Walk(root)
	if err != nil {
		return Result{}, err
	}
	item, err := journal.Resolve(items, locator)
	if err != nil {
		return Result{}, err
	}

	result := Result{Item: item}
	if item.EntryExists {
		e, ok, err := entry.Read(journal.EntryPath(root, item.Shard, item.Key))
		if err != nil {
			return Result{}, err
		}
		if ok {
			result.Entry = &e
		}
	}
	return result, nil
}

// Transcript renders item's transcript copy through its transcript.
// format's renderer (M10/M13) — the one sanctioned transcript read (M9):
// journal only ever names the path (TranscriptPath), never opens or
// parses it; this is the one place, beside the registry's format lookup,
// that does. A format with no registered renderer on this build refuses
// with validation.unknown-transcript-format (V18/V34). maxChars, when >
// 0, caps each turn's text at that many runes (V7's prompt-budget
// truncation, --max-turn-chars).
func Transcript(root string, item journal.WalkItem, maxChars int) ([]source.Turn, error) {
	renderer, err := sourceregistry.ValidateTranscriptFormat(item.Session.Transcript.Format)
	if err != nil {
		return nil, err
	}

	path := journal.TranscriptPath(root, item.Shard, item.Key)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("show: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	return renderer.RenderTranscript(f, maxChars)
}
