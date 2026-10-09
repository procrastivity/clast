// Package capture implements `clast plumbing capture` (SURFACE V13):
// everything new or grown, silently. The verb walks every implemented
// source (M12), captures new sessions, re-captures grown or rewritten
// ones per the source's storage model (M13), resolves project, clone,
// and worktree facts (M17), writes session.json, and applies M3's
// auto-dismissal itself. An unchanged session whose prior capture
// resolved no project retries resolution and backfills session.json in
// place once the clone is registered (M13/M15). Unreadable sessions are
// stderr diagnostics, never a failed run.
package capture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/registry"
	"github.com/procrastivity/clast/internal/source"
)

// autoNoopReason is M3's reserved dismissal reason. Reserved to capture:
// the dismiss verb refuses it (V15), and only the retraction below may
// remove a dismissal carrying it.
const autoNoopReason = "auto:no-op"

// Deps is Run's environment: everything the command layer resolves once.
type Deps struct {
	Root string
	// Sources is the walk set — the bare sweep's predicate-filtered
	// selection, or the one row --harness named (already validated and,
	// on the explicit path, presence-probed by the command layer).
	Sources []source.Source
	// AutoDismissNoop is config capture.auto_dismiss_noop (M3).
	AutoDismissNoop bool
	// Now stamps captured_at and dismissal times; a C2.6-style seam so
	// tests get deterministic documents.
	Now func() time.Time
}

// Captured is one session Run captured or recaptured — the unit of V13's
// one-line-per-session output.
type Captured struct {
	Key           journal.SessionKey
	Shard         string
	Recaptured    bool
	AutoDismissed bool
	// ProjectBackfilled marks a project backfill: session.json rewritten
	// in place with newly resolved project/worktree facts, nothing
	// re-copied (Recaptured stays false).
	ProjectBackfilled bool
}

// SourceFailure is one source's failure to enumerate at all — a
// Discover-level error the sweep isolated and continued past. Run
// collects them for the command layer to disclose: a sweep reports
// each on stderr and in --json's "unavailable" rows while still
// exiting 0 (V13: a source outage is never a failed run, but no
// reported success may hide incomplete work); the explicit --harness
// path instead maps its single source's failure to
// capture.source-unavailable. Cancellation and store-write failures
// never land here — they abort the run outright.
type SourceFailure struct {
	// Source is the failed source's registry name.
	Source string
	// Err is Discover's error, verbatim.
	Err error
}

// Run performs one capture sweep. Diagnostics are accumulated, not
// fatal (V13): a session that cannot be read is reported and skipped,
// and a source that cannot enumerate at all is a collected
// SourceFailure, not an abort. The returned error is reserved for what
// cannot be routed around — store writes, the journal walk itself, and
// context cancellation — and is always classified internal.* (exit 4):
// a fatal capture failure is an internal error, never the chassis's
// catch-all usage exit.
func Run(ctx context.Context, deps Deps) ([]Captured, []source.Diagnostic, []SourceFailure, error) {
	captured, diags, failed, err := run(ctx, deps)
	// An error already carrying a clasterr classification passes through
	// untouched; anything else (journal/os errors, context teardown) is
	// internal.capture.
	var ce *clasterr.Error
	if err != nil && !errors.As(err, &ce) {
		err = clasterr.New("internal.capture", err.Error())
	}
	return captured, diags, failed, err
}

// run is the sweep body behind Run's classification boundary — it
// returns raw errors; Run owns wrapping them as internal.capture.
func run(ctx context.Context, deps Deps) ([]Captured, []source.Diagnostic, []SourceFailure, error) {
	machine, err := journal.Hostname()
	if err != nil {
		return nil, nil, nil, err
	}

	items, walkDiags, err := journal.Walk(deps.Root)
	if err != nil {
		return nil, nil, nil, err
	}
	existing := make(map[journal.SessionKey]journal.WalkItem, len(items))
	for _, item := range items {
		existing[item.Key] = item
	}

	var captured []Captured
	var diags []source.Diagnostic
	var failed []SourceFailure
	for _, wd := range walkDiags {
		diags = append(diags, source.Diagnostic{Path: wd.Path, Err: wd.Err})
	}
	for _, src := range deps.Sources {
		// A canceled context aborts even for a source that ignores it —
		// cancellation is fatal, never a per-source failure.
		if err := ctx.Err(); err != nil {
			return captured, diags, failed, err
		}
		// A source keeping journal-external scan progress keys it to
		// this run's target before enumerating — one run, one journal
		// root, so another target's checkpoint can never answer here.
		if js, ok := src.(source.JournalScope); ok {
			js.ScopeJournal(deps.Root)
		}
		found, srcDiags, err := src.Discover(ctx)
		diags = append(diags, srcDiags...)
		if err != nil {
			// The same check on the error itself: a canceled run
			// aborts rather than disclosing the remaining sources as
			// "unavailable".
			if isCancellation(err) {
				return captured, diags, failed, err
			}
			// Could not enumerate at all — the uniform rule is
			// Discover error = source-level failure, Diagnostic =
			// items skipped. Record and continue: one broken source
			// must not deny another source's valid capture.
			failed = append(failed, SourceFailure{Source: src.Name(), Err: err})
			continue
		}

		for _, d := range found {
			if err := ctx.Err(); err != nil {
				return captured, diags, failed, err
			}
			one, oneDiags, err := captureOne(ctx, deps, src, d, existing, machine)
			diags = append(diags, oneDiags...)
			if err != nil {
				return captured, diags, failed, err
			}
			if one != nil {
				captured = append(captured, *one)
			}
		}
	}
	return captured, diags, failed, nil
}

// isCancellation reports whether err is context teardown — checked
// before any source or session error is treated as continuable, so a
// canceled run aborts rather than being swallowed into a per-source
// failure or a per-item diagnostic.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// unchangedVerdict answers the recapture check — "is d still the
// revision prior's session.json recorded". A source implementing the
// OPTIONAL source.Unchanged seam answers from its own evidence (amp
// compares the enumerated updatedAt with the recorded last_active_at —
// fetch-free, which is the point); every other source re-reads the
// discovered file's fingerprint, M13's file-tail default.
func unchangedVerdict(ctx context.Context, src source.Source, d source.Discovered, prior journal.Session) (bool, error) {
	if u, ok := src.(source.Unchanged); ok {
		return u.Unchanged(ctx, d, prior)
	}
	lines, sum, err := source.FingerprintFile(d.Path)
	if err != nil {
		return false, err
	}
	return lines == prior.Transcript.Lines && sum == prior.Transcript.SHA256, nil
}

// captureOne handles one discovered session end to end. A nil Captured
// with nil error means "nothing to do" — unchanged since last capture,
// or unreadable (then with diagnostics).
func captureOne(ctx context.Context, deps Deps, src source.Source, d source.Discovered, existing map[journal.SessionKey]journal.WalkItem, machine string) (*Captured, []source.Diagnostic, error) {
	key := journal.SessionKey{Harness: src.Name(), NativeID: d.NativeID}

	prior, recapture := existing[key]
	if recapture {
		unchanged, err := unchangedVerdict(ctx, src, d, prior.Session)
		if err != nil {
			if isCancellation(err) {
				return nil, nil, err
			}
			return nil, []source.Diagnostic{{Path: d.Path, Err: err}}, nil
		}
		if unchanged {
			if prior.Session.Project != nil {
				return nil, nil, nil // unchanged — the silent common case (V13).
			}
			// Unchanged transcript, but the prior capture resolved no
			// project (captured before `clast init`, M15): retry
			// resolution and backfill session.json in place if the clone
			// is registered now.
			return backfillProject(ctx, deps, src, d, prior, key)
		}
	}

	var diags []source.Diagnostic
	dir, corrDiags, err := src.Correlate(ctx, d)
	diags = append(diags, corrDiags...)
	if err != nil {
		if isCancellation(err) {
			return nil, diags, err
		}
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: err})
		return nil, diags, nil
	}

	stage, err := journal.StageSession(deps.Root)
	if err != nil {
		return nil, diags, err
	}
	facts, capDiags, err := src.Capture(ctx, d, stage.Write)
	diags = append(diags, capDiags...)
	if err != nil {
		// Deliberate discard: the capture already failed; the discard
		// error would only shadow the diagnostic that matters.
		_ = stage.Discard()
		if isCancellation(err) {
			return nil, diags, err
		}
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: err})
		return nil, diags, nil
	}

	// A recapture must never replace committed truth with a worse read:
	// a source implementing the OPTIONAL Supersede seam vets the fresh
	// capture against the committed session — a partial or stale read
	// cannot overwrite a more complete committed capture merely because
	// the source's metadata advanced. A refusal discards the staged
	// artifacts (committed session.json and artifacts stand untouched),
	// discloses the refusal as the per-item diagnostic, and leaves the
	// id on the source's retry bookkeeping — amp's pending lane is
	// pending-is-emitted, so the vetoed id is already owed again.
	if recapture {
		if sp, ok := src.(source.Supersede); ok {
			if supersedes, why := sp.Supersedes(ctx, d, prior.Session, facts); !supersedes {
				_ = stage.Discard()
				diags = append(diags, source.Diagnostic{Path: d.Path, Err: errors.New(why)})
				return nil, diags, nil
			}
		}
	}

	now := deps.Now()
	shard := prior.Shard
	if !recapture {
		startedAt := facts.StartedAt
		if startedAt.IsZero() {
			startedAt = now
		}
		shard = startedAt.Local().Format("2006-01-02")
	}
	if err := stage.Commit(shard, key); err != nil {
		return nil, diags, err
	}

	session := journal.Session{
		Machine:      machine,
		Worktree:     "",
		Branch:       facts.Branch,
		StartedAt:    facts.StartedAt,
		LastActiveAt: facts.LastActiveAt,
		CapturedAt:   now,
		SourcePath:   d.Path,
		Counts:       facts.Counts,
		Substantive:  facts.Substantive,
		Incomplete:   facts.Incomplete,
		Transcript:   facts.Transcript,
	}
	if dir != "" {
		// Correlation gave a directory; the registry may still not know
		// it (unregistered project, vanished path, not a git repo) — all
		// of those leave the session projectless rather than uncaptured
		// (M17's empty-means-no-fact convention).
		if cc, err := registry.ResolveCurrentClone(ctx, deps.Root, dir); err == nil {
			session.Project = &journal.SessionProject{
				ID:    cc.Project.ID,
				Slug:  cc.Project.Slug,
				Clone: cc.Clone.ID,
				Label: cc.Clone.Label,
				Path:  dir,
			}
			session.Worktree = cc.Worktree
		}
	}
	if err := journal.WriteSession(deps.Root, shard, key, session); err != nil {
		return nil, diags, err
	}

	autoDismissed, err := applyAutoDismissal(deps, shard, key, facts.Substantive, machine, now)
	if err != nil {
		return nil, diags, err
	}

	return &Captured{Key: key, Shard: shard, Recaptured: recapture, AutoDismissed: autoDismissed}, diags, nil
}

// backfillProject retries project resolution for an unchanged session
// whose prior capture resolved none, and rewrites session.json in place
// when a project resolves now. Everything else in the prior document —
// captured_at, the transcript fingerprint, counts — is preserved
// verbatim, so a curated session cannot become stale (M7) and the next
// sweep hits the silent early exit. Only session.json is written: the
// transcript copy and curation documents are never touched, so a
// standing dismissal or entry is unaffected. A still-unresolvable
// session stays silent and is retried on every sweep.
func backfillProject(ctx context.Context, deps Deps, src source.Source, d source.Discovered, prior journal.WalkItem, key journal.SessionKey) (*Captured, []source.Diagnostic, error) {
	dir, diags, err := src.Correlate(ctx, d)
	if err != nil {
		if isCancellation(err) {
			return nil, diags, err
		}
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: err})
		return nil, diags, nil
	}
	if dir == "" {
		return nil, diags, nil // still no cwd evidence — stays projectless.
	}
	cc, err := registry.ResolveCurrentClone(ctx, deps.Root, dir)
	if err != nil {
		// Still unregistered, vanished, or not a git repo — the same
		// projectless-not-uncaptured posture as the fresh-capture path.
		return nil, diags, nil
	}

	session := prior.Session
	session.Project = &journal.SessionProject{
		ID:    cc.Project.ID,
		Slug:  cc.Project.Slug,
		Clone: cc.Clone.ID,
		Label: cc.Clone.Label,
		Path:  dir,
	}
	session.Worktree = cc.Worktree
	if err := journal.WriteSession(deps.Root, prior.Shard, key, session); err != nil {
		return nil, diags, err
	}
	return &Captured{Key: key, Shard: prior.Shard, ProjectBackfilled: true}, diags, nil
}

// applyAutoDismissal is M3 at capture time, both directions:
//
//   - A non-substantive session with no curation state yet is dismissed
//     with the reserved reason, when config says so. An existing curation
//     document — any state — is never touched: a user's dismissal or
//     entry outranks the automatic call.
//   - A session that grew substantive while carrying capture's own
//     auto:no-op dismissal has that dismissal retracted (back to
//     captured). The reason is reserved to capture (V15), so the only
//     decision being reversed here is capture's own from an earlier
//     sweep; a user's dismissal, whatever its text, never matches.
func applyAutoDismissal(deps Deps, shard string, key journal.SessionKey, substantive bool, machine string, now time.Time) (bool, error) {
	cur, present, err := journal.ReadCuration(deps.Root, shard, key)
	if err != nil {
		return false, err
	}

	if !substantive && deps.AutoDismissNoop && !present {
		reason := autoNoopReason
		return true, journal.WriteCuration(deps.Root, shard, key, journal.Curation{
			State:   journal.StateDismissed,
			At:      now,
			Machine: machine,
			Reason:  &reason,
		})
	}

	if substantive && present && cur.State == journal.StateDismissed && cur.Reason != nil && *cur.Reason == autoNoopReason {
		if err := journal.RemoveCuration(deps.Root, shard, key); err != nil {
			return false, fmt.Errorf("retracting auto-dismissal: %w", err)
		}
	}
	return false, nil
}
