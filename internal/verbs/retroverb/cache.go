package retroverb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/procrastivity/clast/internal/prompt"
)

// cacheAppName/cacheSubdir name the cache's own subtree under
// $XDG_CACHE_HOME: $XDG_CACHE_HOME/clast/retro/ (SURFACE V7/V11, MODEL
// §6 — the cache is deferred/derived elsewhere in this codebase but
// retro's own summary cache is this Matter's first cache to actually
// ship). "clast" mirrors internal/asset's appName; "retro" scopes this
// shape's own cache away from any future cache another shape might grow.
const (
	cacheAppName = "clast"
	cacheSubdir  = "retro"
)

// CacheDir resolves $XDG_CACHE_HOME/clast/retro, falling back to
// ~/.cache/clast/retro when $XDG_CACHE_HOME is unset — the same
// env-then-home-fallback shape internal/asset.OverrideDir uses for
// $XDG_CONFIG_HOME (that package's own doc comment), just against the
// cache root instead of the config root. The directory need not exist
// yet; cachePut creates it on first write, and a cold/missing directory
// is simply every lookup missing (MODEL §6: the cache is disposable,
// deleting it — or never having it — is always safe).
func CacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("retro: resolving XDG_CACHE_HOME fallback: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, cacheAppName, cacheSubdir), nil
}

// Fingerprint returns the cache key for one rendered retro-summary prompt
// pair: the hex-encoded sha256 of rendered.System and rendered.User,
// joined by a single NUL byte so the two halves can never collide by
// plain concatenation (a "foo"+"bar" vs "fo"+"obar" style seam).
//
// Recipe (wip finding, llm-verbs/step-05): fingerprinting the fully
// *rendered* pair — not the raw entry body alone, and not the raw prompt
// templates alone — captures every fact §2 fills the templates from in
// one hash: the entry body itself, the session's project/day/started_at/
// session_id (all baked into the filled user prompt), AND the prompt
// templates' own content (both halves, unfilled, are also baked in via
// the surrounding template text). So a fingerprint changes — and the
// cache correctly misses — on any of: a re-curated entry body, a changed
// prompt-pair asset (an override edit, or a shipped-template update), or
// (vacuously) a different session/day, while staying stable run to run
// for the one thing V11 asks it to be stable for: an unchanged curated
// session, re-run later, is not re-summarized.
func Fingerprint(rendered prompt.Rendered) string {
	h := sha256.New()
	h.Write([]byte(rendered.System))
	h.Write([]byte{0})
	h.Write([]byte(rendered.User))
	return hex.EncodeToString(h.Sum(nil))
}

// cacheEntry is one cached summary's on-disk JSON shape — deliberately
// just the one field retro needs; the cache holds no logic the skill
// form must reproduce (V7), only this shortcut.
type cacheEntry struct {
	Summary string `json:"summary"`
}

// cacheGet reads a cached summary for fingerprint from dir. A missing
// file, an unreadable one, or one whose content doesn't parse as
// cacheEntry's JSON shape is treated as a miss, never an error (step-05's
// own posture: "corrupt/unreadable cache entries are treated as misses,
// never errors" — MODEL §6's cache is disposable, and a torn or
// hand-edited cache file must never fail a retro run).
func cacheGet(dir, fingerprint string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, fingerprint+".json"))
	if err != nil {
		return "", false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return "", false
	}
	return e.Summary, true
}

// cachePut writes summary under fingerprint, creating dir as needed, via
// a temp-file-then-rename swap in the same directory (journal's own
// writeBytesAtomic shape, repeated here since that helper is unexported
// inside internal/journal/store.go and this cache is not journal state
// anyway — MODEL §6 draws the cache as a wholly separate subtree). A
// write failure here is the caller's to treat as best-effort (see
// summarizeEntry): losing a cache write costs nothing but a repeat LLM
// call on a later run, never a wrong answer now.
func cachePut(dir, fingerprint, summary string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheEntry{Summary: summary})
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
