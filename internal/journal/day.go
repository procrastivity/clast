package journal

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/config"
)

// dayLayout is the YYYY-MM-DD layout shared by Day values and the
// sessions/ and breadcrumbs/ shard filenames (M8) — though a shard
// filename sharing this format is never load-bearing; every Day this
// package returns comes from timestamp-plus-cutoff math, never read back
// off a path.
const dayLayout = "2006-01-02"

// DefaultCutoffString is the shipped default day_cutoff,
// assets/config.default.yaml's "04:00" (MODEL M8).
const DefaultCutoffString = "04:00"

// dayCutoffConfigKey is the tool-config key naming the day cutoff
// (SURFACE V31, MODEL M8). Empty or absent means DefaultCutoffString.
const dayCutoffConfigKey = "day_cutoff"

// Cutoff is a parsed, validated day_cutoff value (MODEL M8): the clock
// time of day that divides one work day from the next. A timestamp whose
// own local clock time falls strictly before the cutoff belongs to the
// previous calendar day's bucket; at or after the cutoff, it belongs to
// its own calendar day. Parsed once (ParseCutoff/ConfiguredCutoff) so
// callers never re-validate the "HH:MM" string per call.
type Cutoff struct {
	hour, minute int
}

// ParseCutoff parses a "HH:MM" (24-hour, zero-padded) day_cutoff config
// value. A malformed value is an error here, at load time — never a
// silent fallback a verb has to guess around later.
func ParseCutoff(s string) (Cutoff, error) {
	hour, minute, ok := splitHHMM(s)
	if !ok {
		return Cutoff{}, fmt.Errorf(`journal: invalid day_cutoff %q: want "HH:MM"`, s)
	}
	return Cutoff{hour: hour, minute: minute}, nil
}

// ConfiguredCutoff resolves cfg's day_cutoff key — DefaultCutoffString
// when the key is empty or absent — and parses it once.
func ConfiguredCutoff(cfg config.Config) (Cutoff, error) {
	s, _ := cfg[dayCutoffConfigKey].(string)
	if s == "" {
		s = DefaultCutoffString
	}
	return ParseCutoff(s)
}

// splitHHMM parses a strict two-digit "HH:MM" string, hour 00-23, minute
// 00-59. It intentionally rejects single-digit forms ("4:00"): the config
// value is always written zero-padded (assets/config.default.yaml), and a
// stricter parse here refuses over guessing at looser forms.
func splitHHMM(s string) (hour, minute int, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// Day is a resolved work-day bucket, YYYY-MM-DD (M8).
type Day string

// String returns d unchanged.
func (d Day) String() string { return string(d) }

// DayOf computes the work-day bucket for timestamp t under c: t's own
// calendar date, in t's own location (never renormalized to the reading
// machine's zone — a session's started_at or a crumb's at already
// carries the offset that mattered when it was written), shifted back one
// day when t's clock time is strictly before the cutoff (MODEL M8).
// Buckets always derive from the timestamp plus the CURRENT cutoff, never
// from a shard or filename date.
func (c Cutoff) DayOf(t time.Time) Day {
	boundary := time.Date(t.Year(), t.Month(), t.Day(), c.hour, c.minute, 0, 0, t.Location())
	d := t
	if d.Before(boundary) {
		d = d.AddDate(0, 0, -1)
	}
	return Day(d.Format(dayLayout))
}

// addDays shifts d by n calendar days (n may be negative), parsing and
// re-formatting through UTC purely to do calendar arithmetic — d carries
// no time-of-day or zone of its own.
func (d Day) addDays(n int) (Day, error) {
	t, err := time.ParseInLocation(dayLayout, string(d), time.UTC)
	if err != nil {
		return "", fmt.Errorf("journal: %q is not a valid day bucket: %w", d, err)
	}
	return Day(t.AddDate(0, 0, n).Format(dayLayout)), nil
}

// ParseDay parses a V5 day-valued argument — "YYYY-MM-DD", "today",
// "yesterday", or "-Nd" (N calendar days before today) — into a concrete
// Day bucket, relative to the current instant (the shared now() seam)
// and cutoff. "today" and "yesterday" are resolved through cutoff
// exactly like any other timestamp: under a 04:00 cutoff, "today" at
// 02:30 is still yesterday's calendar date (MODEL M8) — there is no
// separate wall-clock "today". Malformed input is an error naming the
// input verbatim; refuse over guess, no partial parses (V4's
// locator posture, applied here to day arguments too).
func ParseDay(arg string, cutoff Cutoff) (Day, error) {
	switch {
	case arg == "today":
		return cutoff.DayOf(now()), nil
	case arg == "yesterday":
		return cutoff.DayOf(now()).addDays(-1)
	case strings.HasPrefix(arg, "-") && strings.HasSuffix(arg, "d"):
		n, err := parseNonNegativeInt(arg[1 : len(arg)-1])
		if err != nil {
			return "", fmt.Errorf("journal: invalid day argument %q: %w", arg, err)
		}
		return cutoff.DayOf(now()).addDays(-n)
	default:
		if _, err := time.Parse(dayLayout, arg); err != nil {
			return "", fmt.Errorf(`journal: invalid day argument %q: want "YYYY-MM-DD", "today", "yesterday", or "-Nd"`, arg)
		}
		return Day(arg), nil
	}
}

// ParseDuration parses a V5 duration-valued argument, "-Nd" or "-Nw",
// into a whole number of days (a week is exactly 7 days) — for
// --since-style range flags. It does not accept the literal "all"; a
// verb offering that value checks for it before calling ParseDuration.
// Malformed input is an error naming the input verbatim; refuse over
// guess.
func ParseDuration(arg string) (int, error) {
	if len(arg) < 2 || arg[0] != '-' {
		return 0, fmt.Errorf(`journal: invalid duration argument %q: want "-Nd" or "-Nw"`, arg)
	}
	unit := arg[len(arg)-1]
	n, err := parseNonNegativeInt(arg[1 : len(arg)-1])
	if err != nil {
		return 0, fmt.Errorf("journal: invalid duration argument %q: %w", arg, err)
	}
	switch unit {
	case 'd':
		return n, nil
	case 'w':
		return n * 7, nil
	default:
		return 0, fmt.Errorf(`journal: invalid duration argument %q: want "-Nd" or "-Nw"`, arg)
	}
}

// parseNonNegativeInt requires s to be one or more ASCII digits — no
// sign, no whitespace, not even a leading '+' — so "-Nd"/"-Nw" callers
// never silently accept "--3d" or "-3.5d" style garbage.
func parseNonNegativeInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("no digits")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a whole number of days")
		}
	}
	return strconv.Atoi(s)
}
