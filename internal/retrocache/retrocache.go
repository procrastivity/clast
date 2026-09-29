// Package retrocache is the retro-summary cache under
// $XDG_CACHE_HOME/clast/retro/ (SURFACE V7's private-state carve-out):
// where it lives, how one summary is keyed, and how an entry is read and
// written. It was extracted from retroverb so a second reader — analyze,
// which serves cached summaries but never calls the LLM — keys entries
// exactly as retro writes them. No verb package imports another verb's
// package, so the recipe lives here, below both.
package retrocache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/prompt"
)

// appName/subdir name the cache's own subtree under $XDG_CACHE_HOME:
// $XDG_CACHE_HOME/clast/retro/ (SURFACE V7/V11, MODEL §6). "clast"
// mirrors internal/asset's appName; "retro" scopes this shape's cache
// away from any future cache another shape might grow.
const (
	appName = "clast"
	subdir  = "retro"
)

// Dir resolves $XDG_CACHE_HOME/clast/retro, falling back to
// ~/.cache/clast/retro when $XDG_CACHE_HOME is unset — the same
// env-then-home-fallback shape internal/asset.OverrideDir uses for
// $XDG_CONFIG_HOME, just against the cache root instead of the config
// root. The directory need not exist yet; Put creates it on first write,
// and a cold/missing directory is simply every lookup missing (MODEL §6:
// the cache is disposable, deleting it — or never having it — is always
// safe).
func Dir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("retro: resolving XDG_CACHE_HOME fallback: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, appName, subdir), nil
}

// Fingerprint returns the cache key for one rendered retro-summary prompt
// pair against model: the hex-encoded sha256 of rendered.System,
// rendered.User, and model, each joined by a single NUL byte so no two
// components can ever collide by plain concatenation (a "foo"+"bar" vs
// "fo"+"obar" style seam).
//
// Recipe (wip finding, llm-verbs/step-05, amended llm-verbs seal sweep
// F5): fingerprinting the fully *rendered* pair — not the raw entry body
// alone, and not the raw prompt templates alone — captures every fact
// the templates are filled from in one hash: the entry body itself, the
// session's day/started_at/session_id (all baked into the filled user
// prompt), AND the prompt templates' own content. model joins as a third
// component (not folded into the rendered pair itself) because it is a
// fact about the *call*, not the *prompt* — a config change to llm.model
// produces a materially different completion for the identical rendered
// pair, and without this a cache built under one model would silently
// keep serving that model's summaries after the config moved to another.
// So a fingerprint changes — and the cache correctly misses — on any of:
// a re-curated entry body, a changed prompt-pair asset (an override
// edit, or a shipped-template update), a changed llm.model, or
// (vacuously) a different session/day, while staying stable run to run
// for the one thing V11 asks it to be stable for: an unchanged curated
// session under an unchanged model, re-run later, is not re-summarized.
//
// Callers key entries through EntryKey, which renders the pair with
// {{project}} blanked; Fingerprint itself is the hash alone.
func Fingerprint(rendered prompt.Rendered, model string) string {
	h := sha256.New()
	h.Write([]byte(rendered.System))
	h.Write([]byte{0})
	h.Write([]byte(rendered.User))
	h.Write([]byte{0})
	h.Write([]byte(model))
	return hex.EncodeToString(h.Sum(nil))
}

// Entry is the facts one retro summary is rendered from — the
// retro-summary user template's placeholders (assets/prompts/
// retro-summary-user.md), before they are stringified.
type Entry struct {
	// Project is the project as the model reads it — the caller maps
	// retro's no-project bucket to its readable label before it gets
	// here. EntryKey leaves it out of the key.
	Project   string
	Day       journal.Day
	StartedAt time.Time
	SessionID string
	Body      string
}

// PromptData maps e onto prompt.RetroSummary's placeholders:
// {{project}}, {{started_at}}, {{day}}, {{session_id}}, {{body}}.
func (e Entry) PromptData() map[string]string {
	return map[string]string{
		"project":    e.Project,
		"started_at": e.StartedAt.UTC().Format(time.RFC3339),
		"day":        string(e.Day),
		"session_id": e.SessionID,
		"body":       e.Body,
	}
}

// EntryKey returns the cache key for e's summary under model: e's prompt
// pair rendered with {{project}} blanked, then fingerprinted. The project
// is the one fact deliberately left out, so a capture-time project
// backfill (a "(no project)" session later resolved to its clone) keeps
// its cached summary instead of costing a fresh request per backfilled
// session. The model still sees the real project in the pair retro
// actually sends; --refresh rewrites a summary that should reflect it.
func EntryKey(e Entry, model string) (string, error) {
	e.Project = ""
	rendered, err := prompt.Render(prompt.RetroSummary, e.PromptData())
	if err != nil {
		return "", err
	}
	return Fingerprint(rendered, model), nil
}

// entry is one cached summary's on-disk JSON shape — deliberately just
// the one field retro needs; the cache holds no logic the skill form
// must reproduce (V7), only this shortcut.
type entry struct {
	Summary string `json:"summary"`
}

// Get reads a cached summary for fingerprint from dir. A missing file,
// an unreadable one, or one whose content doesn't parse as the cache's
// JSON shape is treated as a miss, never an error: MODEL §6's cache is
// disposable, and a torn or hand-edited cache file must never fail a
// run.
func Get(dir, fingerprint string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, fingerprint+".json"))
	if err != nil {
		return "", false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return "", false
	}
	return e.Summary, true
}

// Put writes summary under fingerprint, creating dir as needed, via a
// temp-file-then-rename swap in the same directory (journal's own
// writeBytesAtomic shape, repeated here since that helper is unexported
// inside internal/journal/store.go and this cache is not journal state
// anyway — MODEL §6 draws the cache as a wholly separate subtree). A
// write failure here is the caller's to treat as best-effort: losing a
// cache write costs nothing but a repeat LLM call on a later run, never
// a wrong answer now.
func Put(dir, fingerprint, summary string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(entry{Summary: summary})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fingerprint+".json")

	tmp, err := os.CreateTemp(dir, ".tmp-"+fingerprint+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
