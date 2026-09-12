package journal

import (
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/config"
)

func mustCutoff(t *testing.T, s string) Cutoff {
	t.Helper()
	c, err := ParseCutoff(s)
	if err != nil {
		t.Fatalf("ParseCutoff(%q): %v", s, err)
	}
	return c
}

func TestParseCutoff_Valid(t *testing.T) {
	for _, s := range []string{"04:00", "00:00", "23:59", "09:05"} {
		if _, err := ParseCutoff(s); err != nil {
			t.Errorf("ParseCutoff(%q): unexpected error: %v", s, err)
		}
	}
}

func TestParseCutoff_Malformed(t *testing.T) {
	for _, s := range []string{
		"4:00",   // not zero-padded
		"04:0",   // minute not zero-padded
		"24:00",  // hour out of range
		"04:60",  // minute out of range
		"0400",   // no colon
		"",       // empty
		"aa:bb",  // not digits
		"04:00 ", // trailing garbage
		"-1:00",  // negative
	} {
		if _, err := ParseCutoff(s); err == nil {
			t.Errorf("ParseCutoff(%q): want error, got nil", s)
		}
	}
}

func TestConfiguredCutoff(t *testing.T) {
	c, err := ConfiguredCutoff(config.Config{})
	if err != nil {
		t.Fatalf("ConfiguredCutoff(empty): %v", err)
	}
	if c != mustCutoff(t, DefaultCutoffString) {
		t.Errorf("ConfiguredCutoff(empty) = %+v, want default %+v", c, mustCutoff(t, DefaultCutoffString))
	}

	c, err = ConfiguredCutoff(config.Config{"day_cutoff": "06:30"})
	if err != nil {
		t.Fatalf("ConfiguredCutoff(06:30): %v", err)
	}
	if c != mustCutoff(t, "06:30") {
		t.Errorf("ConfiguredCutoff(06:30) = %+v, want %+v", c, mustCutoff(t, "06:30"))
	}

	if _, err := ConfiguredCutoff(config.Config{"day_cutoff": "garbage"}); err == nil {
		t.Errorf("ConfiguredCutoff(garbage): want error, got nil")
	}
}

func TestCutoff_DayOf_BoundaryCases(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	loc := time.FixedZone("test", -5*3600)

	cases := []struct {
		name string
		at   time.Time
		want Day
	}{
		{"exactly cutoff belongs to its own day", time.Date(2026, 9, 11, 4, 0, 0, 0, loc), "2026-09-11"},
		{"one minute before cutoff belongs to previous day", time.Date(2026, 9, 11, 3, 59, 0, 0, loc), "2026-09-10"},
		{"one minute after cutoff belongs to its own day", time.Date(2026, 9, 11, 4, 1, 0, 0, loc), "2026-09-11"},
		{"midnight belongs to previous day under a nonzero cutoff", time.Date(2026, 9, 11, 0, 0, 0, 0, loc), "2026-09-10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cutoff.DayOf(c.at); got != c.want {
				t.Errorf("DayOf(%v) = %q, want %q", c.at, got, c.want)
			}
		})
	}
}

func TestCutoff_DayOf_MidnightCutoffNeverShifts(t *testing.T) {
	cutoff := mustCutoff(t, "00:00")
	loc := time.FixedZone("test", -5*3600)

	for _, at := range []time.Time{
		time.Date(2026, 9, 11, 0, 0, 0, 0, loc),
		time.Date(2026, 9, 11, 12, 0, 0, 0, loc),
		time.Date(2026, 9, 11, 23, 59, 0, 0, loc),
	} {
		if got, want := cutoff.DayOf(at), Day("2026-09-11"); got != want {
			t.Errorf("DayOf(%v) with 00:00 cutoff = %q, want %q", at, got, want)
		}
	}
}

func TestCutoff_DayOf_UsesTimestampOwnOffset(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")

	// Two timestamps at the same instant in different fixed-offset zones
	// must bucket by their OWN local calendar date, not a shared/reader
	// timezone: 2026-09-11T02:30-05:00 is before the cutoff on the 11th
	// (bucket 09-10); the same instant re-expressed at +09:00 reads
	// 2026-09-11T16:30+09:00, well after the cutoff on the 11th (bucket
	// 09-11). DayOf must respect each Time's own Location, never convert.
	west := time.Date(2026, 9, 11, 2, 30, 0, 0, time.FixedZone("west", -5*3600))
	east := west.In(time.FixedZone("east", 9*3600))

	if got, want := cutoff.DayOf(west), Day("2026-09-10"); got != want {
		t.Errorf("DayOf(west) = %q, want %q", got, want)
	}
	if got, want := cutoff.DayOf(east), Day("2026-09-11"); got != want {
		t.Errorf("DayOf(east) = %q, want %q", got, want)
	}
}

func TestParseDay_LiteralDate(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	got, err := ParseDay("2026-09-11", cutoff)
	if err != nil {
		t.Fatalf("ParseDay: %v", err)
	}
	if got != "2026-09-11" {
		t.Errorf("ParseDay = %q, want %q", got, "2026-09-11")
	}
}

func TestParseDay_TodayAndYesterday_ShiftedClock(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	loc := time.FixedZone("test", -5*3600)

	// 02:30 local is before the 04:00 cutoff, so "today" is still the
	// previous calendar date.
	fakeNow(t, time.Date(2026, 9, 11, 2, 30, 0, 0, loc))

	today, err := ParseDay("today", cutoff)
	if err != nil {
		t.Fatalf("ParseDay(today): %v", err)
	}
	if today != "2026-09-10" {
		t.Errorf(`ParseDay("today") = %q, want %q (cutoff not yet reached)`, today, "2026-09-10")
	}

	yesterday, err := ParseDay("yesterday", cutoff)
	if err != nil {
		t.Fatalf("ParseDay(yesterday): %v", err)
	}
	if yesterday != "2026-09-09" {
		t.Errorf(`ParseDay("yesterday") = %q, want %q`, yesterday, "2026-09-09")
	}
}

func TestParseDay_TodayAfterCutoff(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	loc := time.FixedZone("test", -5*3600)
	fakeNow(t, time.Date(2026, 9, 11, 9, 0, 0, 0, loc))

	today, err := ParseDay("today", cutoff)
	if err != nil {
		t.Fatalf("ParseDay(today): %v", err)
	}
	if today != "2026-09-11" {
		t.Errorf(`ParseDay("today") = %q, want %q`, today, "2026-09-11")
	}
}

func TestParseDay_DaysBack(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	loc := time.FixedZone("test", -5*3600)
	fakeNow(t, time.Date(2026, 9, 11, 9, 0, 0, 0, loc)) // today = 2026-09-11

	cases := map[string]Day{
		"-0d": "2026-09-11",
		"-1d": "2026-09-10",
		"-7d": "2026-09-04",
	}
	for arg, want := range cases {
		got, err := ParseDay(arg, cutoff)
		if err != nil {
			t.Fatalf("ParseDay(%q): %v", arg, err)
		}
		if got != want {
			t.Errorf("ParseDay(%q) = %q, want %q", arg, got, want)
		}
	}
}

func TestParseDay_Malformed(t *testing.T) {
	cutoff := mustCutoff(t, "04:00")
	for _, arg := range []string{
		"2026-13-40", // invalid month/day
		"2026/09/11", // wrong separators
		"tomorrow",   // not a recognized keyword
		"-d",         // no digits
		"-3xd",       // junk before unit
		"--3d",       // double sign
		"-3w",        // day grammar does not accept weeks
		"",           // empty
	} {
		if _, err := ParseDay(arg, cutoff); err == nil {
			t.Errorf("ParseDay(%q): want error, got nil", arg)
		}
	}
}

func TestParseDuration_Valid(t *testing.T) {
	cases := map[string]int{
		"-14d": 14,
		"-2w":  14,
		"-0d":  0,
		"-1w":  7,
	}
	for arg, want := range cases {
		got, err := ParseDuration(arg)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", arg, err)
		}
		if got != want {
			t.Errorf("ParseDuration(%q) = %d, want %d", arg, got, want)
		}
	}
}

func TestParseDuration_Malformed(t *testing.T) {
	for _, arg := range []string{
		"14d",  // missing leading dash
		"-14",  // missing unit
		"-14x", // unrecognized unit
		"-w",   // no digits
		"all",  // literal handled by the caller, not this parser
		"",     // empty
		"-3.5d",
	} {
		if _, err := ParseDuration(arg); err == nil {
			t.Errorf("ParseDuration(%q): want error, got nil", arg)
		}
	}
}
