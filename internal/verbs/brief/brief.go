// Package brief implements `clast plumbing brief` (SURFACE V8/V20): the
// brief shape's gathered material — recent curated entries grouped by
// workspace, today's breadcrumbs, and today's sessions, for one project.
// It is a pure query — one journal.Walk plus one registry.Load, no
// writes, no LLM calls — composing over internal/query and
// internal/registry the same way every other query/registry verb does
// (M4: no side index).
package brief

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/query"
	"github.com/procrastivity/clast/internal/registry"
)

// perGroupCap and totalCap are V7/V8's grouping caps: 3 entries per
// workspace, 8 entries total across every workspace. They are owned here,
// not caller-tunable — V8 names no `--limit` for this verb.
const (
	perGroupCap = 3
	totalCap    = 8
)

// EntryRow is one curated entry gathered into the brief, complete with
// its entry.md content. Unlike wake/sessions' rows (title only), brief's
// flow makes no further plumbing call per entry (V10: "plumbing brief
// [<project>]... otherwise synthesize the working brief" — one call) —
// this document already carries the material the LLM synthesizes from.
type EntryRow struct {
	Item  journal.WalkItem
	Day   journal.Day
	Entry entry.Entry
}

// Group is one workspace's slice of the gathered entries — label falling
// back to branch (V8) — newest-first, capped by Run per perGroupCap/
// totalCap.
type Group struct {
	// Workspace is the group's key: the session's frozen project label,
	// or its branch when the label is empty (workspaceKey).
	Workspace string
	// Branch is the first entry's own branch, carried for display only —
	// a group is not guaranteed branch-homogeneous, this is a hint, not a
	// second key.
	Branch  string
	Entries []EntryRow
}

// SessionRow is one of today's sessions — the same three facts wake/
// sessions carry (a curated row's title, when the read succeeds).
type SessionRow struct {
	Item  journal.WalkItem
	Day   journal.Day
	Title string
}

// Result is plumbing brief's gathered material (SURFACE V8/V10).
type Result struct {
	// ProjectSlug is the resolved project this brief was gathered for.
	ProjectSlug string
	// CurrentWorkspace is the cwd's own clone's label, when the cwd
	// resolves (registry.ResolveCurrentClone) to a clone of this SAME
	// project — "" otherwise (unregistered cwd, not a git repo, or a
	// clone of a different project). It hoists that workspace's group
	// first when present among Groups (V8), regardless of whether the
	// project itself came from the positional or the cwd default.
	CurrentWorkspace string
	Groups           []Group
	// Breadcrumbs is today's breadcrumbs scoped to ProjectSlug (the same
	// project-only scoping `plumbing breadcrumbs --project` applies,
	// V19), chronological.
	Breadcrumbs []journal.BreadcrumbEntry
	// Sessions is today's sessions scoped to ProjectSlug, every state,
	// newest-first.
	Sessions []SessionRow
	// Empty is V7's brief empty-state short-circuit fact: true when
	// Groups, Breadcrumbs, and Sessions are all empty. The flow (V10)
	// reads this and stops before ever calling the LLM.
	Empty bool
}

// Run assembles brief's gathered material for projectLocator (a project
// id or slug, resolved via the registry — the same locator shape
// `clones [<project>]` accepts, V23) or, when projectLocator is "", for
// dir's own cwd-resolved project (the whereami path, V22; see
// resolveProject's doc for the exact posture and the finding it records).
//
// since restricts the recent-curated-entries window (nil = no
// restriction); today is the M8 day bucket breadcrumbs/sessions are
// scoped to — both already resolved by the caller (command.go), so this
// function needs no clock of its own and stays deterministic for tests.
func Run(ctx context.Context, root, dir, projectLocator string, cutoff journal.Cutoff, since *journal.Day, today journal.Day) (Result, error) {
	slug, currentWorkspace, err := resolveProject(ctx, root, dir, projectLocator)
	if err != nil {
		return Result{}, err
	}

	items, _, err := journal.Walk(root)
	if err != nil {
		return Result{}, err
	}

	entries := gatherEntries(root, items, slug, since, cutoff)
	groups := groupEntries(entries, currentWorkspace)

	breadcrumbs, err := gatherBreadcrumbs(root, today, cutoff, slug)
	if err != nil {
		return Result{}, err
	}

	sessions := gatherSessions(root, items, slug, today, cutoff)

	return Result{
		ProjectSlug:      slug,
		CurrentWorkspace: currentWorkspace,
		Groups:           groups,
		Breadcrumbs:      breadcrumbs,
		Sessions:         sessions,
		Empty:            len(groups) == 0 && len(breadcrumbs) == 0 && len(sessions) == 0,
	}, nil
}

// resolveProject resolves brief's project scope and, alongside it, the
// cwd's own workspace label to hoist (V8's "current workspace hoisted
// first").
//
// Two cases:
//
//   - projectLocator != "": resolved via registry.ResolveProject (id or
//     slug — the same locator shape `clones [<project>]` already accepts,
//     V23), rather than treated as a raw, unvalidated filter value the
//     way `sessions --project` is. An unknown locator is
//     validation.unknown-locator, mirroring clones' own unknownProject
//     (finding: brief's `<project>` reads as a registry LOCATOR, not a
//     bare filter string, since it is a positional naming an identity —
//     the same posture the id-or-slug registry verbs already take).
//     The cwd is then independently, best-effort resolved
//     (registry.ResolveCurrentClone): when it succeeds and names a clone
//     of the SAME project, that clone's label hoists; any failure
//     (unregistered cwd, not a git repo, a clone of a different project)
//     is silently ignored — an explicit project argument must never
//     refuse just because the cwd doesn't happen to resolve into it.
//
//   - projectLocator == "": the project defaults from the cwd, "the
//     whereami path" (V8/V22, V10's own phrasing) — resolveProject
//     mirrors whereami.Run's exact two-tier posture rather than
//     reinventing it or folding both failures into one code the way
//     breadcrumb originally did (query-verbs' own review fix corrected
//     that shortcut to split on ErrUnknownClone, the same split repeated
//     here): a cwd not inside a git repository at all is
//     validation.not-a-git-repo (whereami's own pre-flight check,
//     internal/verbs/whereami/whereami.go); one that is, but matches no
//     clone registered under this machine's name, is
//     refusal.unknown-clone naming `clast init` (V22's own stated case).
//     This is the "how V10 and V22 read together" finding V8 asks for:
//     V10 says the project defaults via cwd resolution "the whereami
//     path", and V22 states only the unregistered-clone half of that
//     path — the not-a-git-repo half is whereami's own implementation
//     choice, carried here for the same reason whereami made it (a
//     structured code beats a raw exec.ExitError bubbling out of
//     registry.CommonDir).
func resolveProject(ctx context.Context, root, dir, projectLocator string) (slug, currentWorkspace string, err error) {
	if projectLocator != "" {
		view, _, err := registry.Load(root)
		if err != nil {
			return "", "", err
		}
		pv, err := registry.ResolveProject(view, projectLocator)
		if err != nil {
			if errors.Is(err, registry.ErrUnknownProject) {
				return "", "", unknownProject(projectLocator)
			}
			return "", "", err
		}
		if current, cerr := registry.ResolveCurrentClone(ctx, root, dir); cerr == nil && current.Project.Slug == pv.Project.Slug {
			currentWorkspace = current.Clone.Label
		}
		return pv.Project.Slug, currentWorkspace, nil
	}

	if _, err := registry.CommonDir(ctx, dir); err != nil {
		return "", "", clasterr.New("validation.not-a-git-repo",
			fmt.Sprintf("%s is not inside a git repository clast can read: %v", dir, err))
	}
	current, err := registry.ResolveCurrentClone(ctx, root, dir)
	if err != nil {
		if errors.Is(err, registry.ErrUnknownClone) {
			return "", "", unknownClone(dir)
		}
		return "", "", err
	}
	return current.Project.Slug, current.Clone.Label, nil
}

// unknownProject is brief's not-found for an explicit `<project>` locator
// that resolves to nothing: validation.unknown-locator, mirroring
// clones' own unknownProject (internal/verbs/clones/clones.go) — the same
// wip-derived spelling for an unresolved locator.
func unknownProject(locator string) error {
	return clasterr.New("validation.unknown-locator",
		fmt.Sprintf("%q matches no registered project, by id or slug", locator))
}

// unknownClone is brief's refusal for a defaulted (no positional) cwd
// that resolves to no registered clone: refusal.unknown-clone (V8/V22),
// naming `clast init` as the fix — mirrors whereami's own unknownClone
// verbatim (no --repo/--project escape hatch to name here either: an
// explicit `<project>` argument is that escape hatch, already handled by
// the other branch of resolveProject).
func unknownClone(dir string) error {
	return clasterr.New("refusal.unknown-clone",
		fmt.Sprintf("refused — %s is not a registered clast clone; run `clast init` here to register it, or pass a project explicitly", dir))
}

// gatherEntries returns every curated entry for slug within the since
// window, newest-first by started_at — the same sort key sessions.Run
// uses. V8 leaves "recent" unpinned for curated entries; this resolves it
// the way the codebase already resolves windows (finding: the `since`
// config key posture, query.ConfiguredSince/ResolveSince — command.go
// wires the same flag/default sessions and wake already expose, rather
// than inventing a second window mechanism).
//
// A curated session whose entry.md fails to read (a torn document) is
// dropped from the result entirely — a deliberate divergence from
// sessions/wake's own tolerant posture (which keeps the row, only drops
// the title): those verbs list sessions, so a row survives without a
// title, but brief gathers entry CONTENT — a row with none is not
// gathered material, there is nothing for the flow to read (finding).
func gatherEntries(root string, items []journal.WalkItem, slug string, since *journal.Day, cutoff journal.Cutoff) []EntryRow {
	filter := query.Filter{
		States:  []journal.CurationState{journal.StateCurated},
		Project: slug,
		Since:   since,
	}
	matched := query.Apply(items, filter, cutoff)

	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].Session.StartedAt.After(matched[j].Session.StartedAt)
	})

	rows := make([]EntryRow, 0, len(matched))
	for _, it := range matched {
		e, ok, err := entry.Read(journal.EntryPath(root, it.Shard, it.Key))
		if err != nil || !ok {
			continue
		}
		rows = append(rows, EntryRow{Item: it, Day: cutoff.DayOf(it.Session.StartedAt), Entry: e})
	}
	return rows
}

// workspaceKey is one entry's grouping key (V8): its frozen project's
// label, or its branch when the label is empty. query.Filter.Project
// already guarantees Session.Project != nil for every row gatherEntries
// returns (Match rejects a projectless session outright once Project is
// set), so the label side of this is not itself defensive — the branch
// fallback exists for the (M16-rare, never produced by a registered
// clone) case of an empty label.
func workspaceKey(it journal.WalkItem) string {
	if it.Session.Project != nil && it.Session.Project.Label != "" {
		return it.Session.Project.Label
	}
	return it.Session.Branch
}

// groupEntries buckets rows by workspace (workspaceKey), hoists
// currentWorkspace's group first when present, then applies the 3-per-
// group / 8-total caps (V7's disposition row, owned here). Group order is
// otherwise first appearance in rows' own newest-first order — the same
// "unique keys in order of appearance, current hoisted" shape the old
// bash porcelain used (main:lib/clast/clast-porcelain-subcommands/
// brief.bash, kept as feel input only, never ported verbatim).
func groupEntries(rows []EntryRow, currentWorkspace string) []Group {
	var order []string
	byKey := map[string]*Group{}
	for _, r := range rows {
		key := workspaceKey(r.Item)
		g, ok := byKey[key]
		if !ok {
			g = &Group{Workspace: key, Branch: r.Item.Session.Branch}
			byKey[key] = g
			order = append(order, key)
		}
		g.Entries = append(g.Entries, r)
	}

	order = hoist(order, currentWorkspace)

	groups := make([]Group, 0, len(order))
	emitted := 0
	for _, key := range order {
		if emitted >= totalCap {
			break
		}
		g := *byKey[key]
		groupCap := perGroupCap
		if remaining := totalCap - emitted; remaining < groupCap {
			groupCap = remaining
		}
		if len(g.Entries) > groupCap {
			g.Entries = g.Entries[:groupCap]
		}
		emitted += len(g.Entries)
		groups = append(groups, g)
	}
	return groups
}

// hoist moves current to the front of order when present, preserving the
// relative order of everything else. current == "" or absent from order
// is a no-op.
func hoist(order []string, current string) []string {
	if current == "" {
		return order
	}
	idx := -1
	for i, k := range order {
		if k == current {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return order
	}
	hoisted := make([]string, 0, len(order))
	hoisted = append(hoisted, order[idx])
	hoisted = append(hoisted, order[:idx]...)
	hoisted = append(hoisted, order[idx+1:]...)
	return hoisted
}

// gatherBreadcrumbs returns today's breadcrumbs scoped to slug — the same
// project-only scoping `plumbing breadcrumbs --project` applies (V19:
// "project != ”: that project's crumbs only"), inlined here rather than
// imported (no verb package imports another verb's package in this
// codebase; small duplicated logic like this already recurs across wake/
// sessions, e.g. rowJSON/stateColumn).
func gatherBreadcrumbs(root string, day journal.Day, cutoff journal.Cutoff, slug string) ([]journal.BreadcrumbEntry, error) {
	entries, _, err := journal.ReadBreadcrumbsForDay(root, day, cutoff)
	if err != nil {
		return nil, err
	}
	var out []journal.BreadcrumbEntry
	for _, e := range entries {
		if e.Slug != nil && *e.Slug == slug {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}

// gatherSessions returns today's sessions scoped to slug, every curation
// state, newest-first — the same tolerant title read sessions.Run/
// wake.Run use (drop the title, keep the row, MODEL §7): this is a
// listing of activity, not gathered content, so there is no reason to
// diverge from their posture the way gatherEntries deliberately does.
func gatherSessions(root string, items []journal.WalkItem, slug string, day journal.Day, cutoff journal.Cutoff) []SessionRow {
	filter := query.Filter{Project: slug, Day: &day}
	matched := query.Apply(items, filter, cutoff)

	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].Session.StartedAt.After(matched[j].Session.StartedAt)
	})

	rows := make([]SessionRow, len(matched))
	for i, it := range matched {
		row := SessionRow{Item: it, Day: cutoff.DayOf(it.Session.StartedAt)}
		if it.State() == journal.StateCurated {
			if e, ok, err := entry.Read(journal.EntryPath(root, it.Shard, it.Key)); err == nil && ok {
				row.Title = e.Title
			}
		}
		rows[i] = row
	}
	return rows
}
