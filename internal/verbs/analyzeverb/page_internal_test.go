package analyzeverb

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/procrastivity/clast/internal/journal"
)

func at(h, m int) time.Time { return time.Date(2026, 9, 21, h, m, 0, 0, time.UTC) }

func fixturePage() Page {
	return Page{
		WindowStart: "2026-09-21",
		Day:         "2026-09-22",
		Days: []Day{
			{
				Day: "2026-09-21",
				Projects: []Project{
					{Name: "clast", Sessions: []Session{
						{
							ID: "aaaaaaaa-1111", Day: "2026-09-21", Project: "clast", Branch: "main",
							State: journal.StateCurated, StartedAt: at(14, 3), LastActiveAt: at(14, 45),
							UserMsgs: 3, AssistantMsgs: 9, TranscriptLines: 120,
							Title: "Ship <the> thing", Tags: []string{"retro"},
							Entry: "## Goal\nfour words of entry", HasEntry: true,
							Summary: "- two words", HasSummary: true,
						},
						{
							ID: "bbbbbbbb-2222", Day: "2026-09-21", Project: "clast",
							State: journal.StateCurated, StartedAt: at(16, 0), LastActiveAt: at(16, 10),
							Title: "Unsummarized", Entry: "six words in this entry body", HasEntry: true,
						},
					}},
					{Name: "(no project)", Sessions: []Session{
						{
							ID: "cccccccc-3333", Day: "2026-09-21", Project: "(no project)",
							State: journal.StateDismissed, Reason: "auto:no-op", StartedAt: at(9, 0), LastActiveAt: at(9, 1),
						},
					}},
				},
				Breadcrumbs: []CrumbGroup{
					{Slug: "", Crumbs: []Crumb{{At: at(8, 30), Text: "global <note>", Machine: "BOX"}}},
					{Slug: "clast", Crumbs: []Crumb{{At: at(10, 5), Text: "picked up retro"}}},
				},
			},
			{
				Day:         "2026-09-22",
				Breadcrumbs: []CrumbGroup{{Slug: "wip", Crumbs: []Crumb{{At: at(11, 0), Text: "crumb only day"}}}},
			},
		},
	}
}

func TestPageStats(t *testing.T) {
	got := fixturePage().Stats()
	want := Stats{Curated: 2, Uncurated: 1, AvgEntryWords: 6, AvgSummaryWords: 3, Unsummarized: 1, Breadcrumbs: 3}
	if got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
}

func TestSessionLabels(t *testing.T) {
	p := fixturePage()
	ss := p.Sessions()
	if len(ss) != 3 {
		t.Fatalf("Sessions = %d, want 3", len(ss))
	}
	cases := []struct{ got, want string }{
		{ss[0].NavMeta(), "6w entry · 3w retro"},
		{ss[1].NavMeta(), "6w entry · not summarized"},
		{ss[2].NavMeta(), "auto:no-op"},
		{ss[2].Label(), "cccccccc · dismissed"},
		{ss[0].Started(), "14:03 UTC"},
		{ss[0].DayLabel(), "Mon, Sep 21"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if m := ss[0].Minutes(); m != 42 {
		t.Errorf("Minutes = %d, want 42", m)
	}
}

func renderFixture(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, fixturePage()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestRender_ShippedTemplate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out := renderFixture(t)
	for _, want := range []string{
		`<title>Session Explorer · 2026-09-21 → 2026-09-22</title>`,
		`<section class="view" id="overview">`,
		`<a href="#s-aaaaaaaa-1111">Ship &lt;the&gt; thing<span class="meta">6w entry · 3w retro</span></a>`,
		`<a href="#s-cccccccc-3333" class="dim">`,
		`<section class="view" id="s-aaaaaaaa-1111">`,
		`<h4>Goal</h4>`,
		`<ul><li>two words</li></ul>`,
		`not summarized — run <code>clast retro 2026-09-21</code>`,
		`Dismissed (auto:no-op).`,
		`<span class="chip">#retro</span>`,
		`<section class="view" id="crumbs-2026-09-21">`,
		`<header><h3>(global)</h3><span class="count">1</span></header>`,
		`global &lt;note&gt; <span class="machine">· BOX</span>`,
		`<section class="view" id="crumbs-2026-09-22">`,
		`<span class="chip"><b>3</b> breadcrumbs</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page lacks %q", want)
		}
	}
	if strings.Contains(out, "<the>") || strings.Contains(out, "<note>") {
		t.Error("rendered page carries unescaped journal text")
	}
}

func TestRender_Deterministic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if a, b := renderFixture(t), renderFixture(t); a != b {
		t.Error("two renders of one page differ, want byte-identical output")
	}
}

func TestRender_UserOverrideShadowsShipped(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	path := filepath.Join(cfg, "clast", "analyze", "index.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{{range .Sessions}}{{.Short}};{{end}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := renderFixture(t), "aaaaaaaa;bbbbbbbb;cccccccc;"; got != want {
		t.Errorf("override render = %q, want %q", got, want)
	}
}

func TestRender_BrokenOverride_IsPageUnavailable(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	path := filepath.Join(cfg, "clast", "analyze", "index.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{{.NoSuchField}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := Render(&buf, fixturePage())
	if err == nil || !strings.Contains(err.Error(), "analyze: rendering the page template") {
		t.Fatalf("Render = %v, want a page-unavailable error", err)
	}
	if buf.Len() != 0 {
		t.Errorf("Render wrote %d bytes on error, want none", buf.Len())
	}
}
