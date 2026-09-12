// Package query is the shared session filter layer every query verb
// composes over one journal.Walk (SURFACE V17 and friends; MODEL M4: the
// tree is the log, so a query is a walk, never a side index). One Filter
// value names every axis `sessions` (and, in part, `stats`) can narrow on;
// Match/Apply derive the M7/M8 facts (staleness, day bucket) a raw
// WalkItem doesn't carry on its own.
package query

import (
	"fmt"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/config"
	"github.com/procrastivity/clast/internal/journal"
	sourceregistry "github.com/procrastivity/clast/internal/source/registry"
)

// SinceConfigKey is the tool-config key naming the default recent window
// for a bare `sessions` (SURFACE V31: "-14d").
const SinceConfigKey = "since"

// DefaultSinceString is config.default.yaml's own since value, used when
// the key is absent from a loaded config entirely (config.Load always
// merges the shipped default in, so this is a defensive fallback, not the
// normal path).
const DefaultSinceString = "-14d"

// Filter is the shared session filter set (SURFACE V17). A zero Filter
// matches every session Walk returns — no field's zero value narrows
// anything, so composing filters is just setting the fields a verb's
// flags actually populated.
type Filter struct {
	// States restricts to sessions in one of these curation states
	// (repeatable --state). Empty means no restriction.
	States []journal.CurationState
	// StaleOnly restricts to stale sessions (M7) — --stale. It composes
	// with States: --state curated --stale is a further narrowing, not a
	// replacement (V17).
	StaleOnly bool
	// Project restricts to session.json's frozen project.slug (--project).
	// "" means no restriction.
	Project string
	// Harness restricts to session.json's harness (--harness). "" means
	// no restriction. ValidateHarness is the caller's job before this
	// field is set — Match trusts an already-validated Filter and never
	// itself raises validation.unknown-harness.
	Harness string
	// Machine restricts to session.json's machine (--machine). "" means
	// no restriction.
	Machine string
	// Day restricts to sessions whose M8 day bucket
	// (Cutoff.DayOf(started_at)) equals *Day. nil means no restriction.
	Day *journal.Day
	// Since restricts to sessions whose M8 day bucket is on or after
	// *Since (inclusive) — the "recent window" (V17/V31). nil means no
	// restriction (the "all" case). A day-bucket comparison, not a raw
	// timestamp one: journal.Day is a plain YYYY-MM-DD string, so two
	// buckets compare correctly with Go's ordinary string "<", and this
	// sidesteps the location ambiguity a single absolute cutoff instant
	// would otherwise have to resolve across sessions recorded in
	// different zones (M8: a session's started_at keeps its own offset).
	Since *journal.Day
}

// ValidateHarness checks harness against the source registry table (V30):
// "" always validates (no restriction); anything else must name an
// implemented source, or this returns validation.unknown-harness naming
// the implemented set. Every verb that accepts --harness calls this
// before building a Filter, so Match itself never needs to know about the
// source registry.
func ValidateHarness(harness string) error {
	if harness == "" {
		return nil
	}
	if _, ok := sourceregistry.Lookup(harness); !ok {
		return clasterr.New("validation.unknown-harness",
			fmt.Sprintf("unknown harness %q; implemented: %s", harness, strings.Join(sourceregistry.Names, ", ")))
	}
	return nil
}

// validStates lists --state's closed vocabulary, in the order ParseStates
// reports them in an error (MODEL §2's own state names).
var validStates = []journal.CurationState{journal.StateCaptured, journal.StateCurated, journal.StateDismissed}

// ParseStates validates and converts --state's repeated raw values into
// Filter.States. An empty values slice returns (nil, nil) — no
// restriction, Filter's own zero-value posture. Any value outside MODEL
// §2's three states is validation.unknown-state, naming the value and the
// closed set.
func ParseStates(values []string) ([]journal.CurationState, error) {
	if len(values) == 0 {
		return nil, nil
	}
	states := make([]journal.CurationState, 0, len(values))
	for _, v := range values {
		state := journal.CurationState(v)
		if !isValidState(state) {
			return nil, clasterr.New("validation.unknown-state",
				fmt.Sprintf("unknown state %q; want one of %s", v, joinStates(validStates)))
		}
		states = append(states, state)
	}
	return states, nil
}

func isValidState(s journal.CurationState) bool {
	for _, v := range validStates {
		if v == s {
			return true
		}
	}
	return false
}

func joinStates(states []journal.CurationState) string {
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// ResolveSince parses a --since flag value ("all" or a V5 duration,
// "-Nd"/"-Nw" per journal.ParseDuration) into a Filter.Since lower bound,
// relative to now's own current M8 day bucket under cutoff. defaultArg is
// what an unset flag (flagValue == "") resolves to instead — callers pass
// the configured `since` key (V31, sessions' own default) or the literal
// "all" (stats' default, V21: deliberately not the since key), since the
// two query verbs that use this default differently.
func ResolveSince(flagValue, defaultArg string, cutoff journal.Cutoff, now time.Time) (*journal.Day, error) {
	arg := flagValue
	if arg == "" {
		arg = defaultArg
	}
	if arg == "all" {
		return nil, nil
	}
	days, err := journal.ParseDuration(arg)
	if err != nil {
		return nil, err
	}
	bound, err := cutoff.DayOf(now).AddDays(-days)
	if err != nil {
		return nil, err
	}
	return &bound, nil
}

// ConfiguredSince reads cfg's `since` config key (V31), defaulting to
// DefaultSinceString when the key is empty or absent. Mirrors
// journal.ConfiguredCutoff's own posture: since must be a string when
// present, never a silent fallback on a config typo.
func ConfiguredSince(cfg config.Config) (string, error) {
	s := DefaultSinceString
	if raw, present := cfg[SinceConfigKey]; present && raw != nil {
		str, isString := raw.(string)
		if !isString {
			return "", fmt.Errorf("query: config key %q must be a string, got %T", SinceConfigKey, raw)
		}
		if str != "" {
			s = str
		}
	}
	return s, nil
}

// Match reports whether item satisfies f, deriving day-bucket and
// staleness facts through cutoff (M7/M8) — item itself never carries
// them precomputed.
func Match(item journal.WalkItem, f Filter, cutoff journal.Cutoff) bool {
	if len(f.States) > 0 && !stateIn(f.States, item.State()) {
		return false
	}
	if f.StaleOnly && !item.Stale() {
		return false
	}
	if f.Project != "" && (item.Session.Project == nil || item.Session.Project.Slug != f.Project) {
		return false
	}
	if f.Harness != "" && item.Session.Harness != f.Harness {
		return false
	}
	if f.Machine != "" && item.Session.Machine != f.Machine {
		return false
	}
	if f.Day != nil && cutoff.DayOf(item.Session.StartedAt) != *f.Day {
		return false
	}
	if f.Since != nil && cutoff.DayOf(item.Session.StartedAt) < *f.Since {
		return false
	}
	return true
}

func stateIn(states []journal.CurationState, s journal.CurationState) bool {
	for _, st := range states {
		if st == s {
			return true
		}
	}
	return false
}

// Apply filters items down to the ones Match approves, preserving Walk's
// own order (shard, then directory name) — sort order for display (V17:
// newest-first by started_at) is a verb-level concern layered on top, not
// this shared step's.
func Apply(items []journal.WalkItem, f Filter, cutoff journal.Cutoff) []journal.WalkItem {
	var out []journal.WalkItem
	for _, it := range items {
		if Match(it, f, cutoff) {
			out = append(out, it)
		}
	}
	return out
}
