package claude

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/procrastivity/clast/internal/source"
)

const (
	simpleID    = "aaaaaaaa-1111-4111-8111-111111111111"
	noopID      = "bbbbbbbb-2222-4222-8222-222222222222"
	truncatedID = "cccccccc-3333-4333-8333-333333333333"
	forkID      = "dddddddd-4444-4444-8444-444444444444"
)

func fixtureSource() *Source {
	return NewAt(filepath.Join("testdata", "claudehome"))
}

func discoveredByID(t *testing.T, id string) source.Discovered {
	t.Helper()
	found, diags, err := fixtureSource().Discover(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Discover: err=%v diags=%v", err, diags)
	}
	for _, d := range found {
		if d.NativeID == id {
			return d
		}
	}
	t.Fatalf("session %s not discovered", id)
	return source.Discovered{}
}

func TestDiscoverEnumeratesFixtureSessions(t *testing.T) {
	found, diags, err := fixtureSource().Discover(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Discover: err=%v diags=%v", err, diags)
	}
	var ids []string
	for _, d := range found {
		ids = append(ids, d.NativeID)
		if d.Path == "" || d.ModTime.IsZero() {
			t.Errorf("discovered %s missing path or mtime: %+v", d.NativeID, d)
		}
	}
	sort.Strings(ids)
	want := []string{simpleID, noopID, truncatedID, forkID}
	sort.Strings(want)
	if len(ids) != len(want) {
		t.Fatalf("discovered %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("discovered %v, want %v", ids, want)
		}
	}
}

func TestDiscoverMissingRootIsEmpty(t *testing.T) {
	found, diags, err := NewAt(filepath.Join(t.TempDir(), "absent")).Discover(context.Background())
	if err != nil || diags != nil || found != nil {
		t.Fatalf("want nil, nil, nil for an absent config dir; got %v, %v, %v", found, diags, err)
	}
}

func TestCorrelateReadsCWDFromTranscript(t *testing.T) {
	dir, diags, err := fixtureSource().Correlate(context.Background(), discoveredByID(t, simpleID))
	if err != nil || len(diags) != 0 {
		t.Fatalf("Correlate: err=%v diags=%v", err, diags)
	}
	if dir != "/home/user/Code/demo" {
		t.Errorf("dir = %q, want /home/user/Code/demo", dir)
	}
}

func TestScanTranscriptFacts(t *testing.T) {
	cases := []struct {
		id          string
		user        int
		assistant   int
		substantive bool
		branch      string
		badLines    int
	}{
		// The simple session: tool_result carrier, isMeta prompt, and
		// origin:task notification all stay out of UserCount; gitBranch's
		// last value wins.
		{simpleID, 2, 2, true, "feature/hello", 0},
		{noopID, 1, 0, false, "main", 0},
		// The half-written trailing line (a second assistant entry, cut
		// mid-write) is counted as a bad line, not as a message.
		{truncatedID, 1, 1, true, "main", 1},
		// The fork's duplicated copied entry counts once (M14).
		{forkID, 2, 2, true, "main", 0},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			d := discoveredByID(t, tc.id)
			f, diags, err := scanTranscript(d.Path)
			if err != nil || len(diags) != 0 {
				t.Fatalf("scanTranscript: err=%v diags=%v", err, diags)
			}
			if f.UserCount != tc.user || f.AssistantCount != tc.assistant {
				t.Errorf("counts = %d/%d, want %d/%d", f.UserCount, f.AssistantCount, tc.user, tc.assistant)
			}
			if got := f.AssistantCount >= 1; got != tc.substantive {
				t.Errorf("substantive = %v, want %v", got, tc.substantive)
			}
			if f.Branch != tc.branch {
				t.Errorf("branch = %q, want %q", f.Branch, tc.branch)
			}
			if f.BadLines != tc.badLines {
				t.Errorf("badLines = %d, want %d", f.BadLines, tc.badLines)
			}
			if f.StartedAt.IsZero() || f.LastActiveAt.Before(f.StartedAt) {
				t.Errorf("timestamps: started=%v last=%v", f.StartedAt, f.LastActiveAt)
			}
		})
	}
}
