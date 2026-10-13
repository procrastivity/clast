// Package pi is the pi source: Pi coding-agent sessions as JSONL files
// under the session store (default ~/.pi/agent/sessions). File-tail
// posture (M13). The contract this package implements is
// docs/pi-source/decisions.md ("Contract (step-02)").
//
// The files hold the three duties: root resolution, the walk, the shared
// header reader and Correlate (discover*.go, roots.go, header.go,
// correlate.go), and Capture (capture.go, scan.go). Pi is a local
// source in the production registry: bare sweeps include it unless
// capture.exclude names it, and it takes no capture.pi.auto key.
package pi

import (
	"os"

	"github.com/procrastivity/clast/internal/source"
)

// Name is the registry key and M11 identity prefix.
const Name = "pi"

// Format is the transcript.format string Capture stamps (step-05). The
// number is the Pi header version the parser was qualified against.
const Format = "pi-jsonl-v3"

// supportedVersion is the only header version this package reads.
const supportedVersion = 3

// Env is the process environment Source reads to find Pi's store. Both
// fields are injectable so tests never touch the real environment or
// home directory; a nil field means the os default.
type Env struct {
	// Getenv reads an environment variable (default os.Getenv).
	Getenv func(string) string
	// UserHomeDir resolves the home directory (default os.UserHomeDir).
	UserHomeDir func() (string, error)
}

func (e Env) getenv(k string) string {
	if e.Getenv != nil {
		return e.Getenv(k)
	}
	return os.Getenv(k)
}

func (e Env) home() (string, error) {
	if e.UserHomeDir != nil {
		return e.UserHomeDir()
	}
	return os.UserHomeDir()
}

// Source implements source.Source (and source.Presence) over Pi's
// session store.
type Source struct {
	// fixedRoot, when non-empty, replaces the whole candidate root list
	// (the fixture seam, like claude.NewAt).
	fixedRoot string
	env       Env
}

// New returns the pi source over the real environment: roots resolve
// lazily at call time from PI_CODING_AGENT_SESSION_DIR, the global
// settings.json, PI_CODING_AGENT_DIR and the home directory.
func New() *Source { return &Source{} }

// NewWithEnv returns the pi source with an injected environment.
func NewWithEnv(env Env) *Source { return &Source{env: env} }

// NewAt returns the pi source over exactly one root directory — the
// fixture seam. No environment, settings file or home directory is read.
func NewAt(root string) *Source { return &Source{fixedRoot: root} }

// Name implements source.Source.
func (s *Source) Name() string { return Name }

// Model implements source.Source: pi is file-tail (M13).
func (s *Source) Model() source.StorageModel { return source.FileTail }

var (
	_ source.Source   = (*Source)(nil)
	_ source.Presence = (*Source)(nil)
)
