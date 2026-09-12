// Package capture implements `clast plumbing capture` (SURFACE V13):
// everything new or grown, silently. The verb walks every implemented
// source (M12), captures new sessions, re-captures grown or rewritten
// ones per the source's storage model (M13), resolves project, clone,
// and worktree facts (M17), writes session.json, and applies M3's
// auto-dismissal itself. Unreadable sessions are stderr diagnostics,
// never a failed run.
package capture

import (
	"context"
	"fmt"
	"time"

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
	// Sources is the walk set — the full registry table, or one row when
	// --harness filtered it (already validated by the command layer).
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
}

// Run performs one capture sweep. Diagnostics are accumulated, not
// fatal (V13): a session that cannot be read is reported and skipped;
// only store writes and the walk itself can fail the run.
func Run(ctx context.Context, deps Deps) ([]Captured, []source.Diagnostic, error) {
	machine, err := journal.Hostname()
	if err != nil {
		return nil, nil, err
	}

	items, walkDiags, err := journal.Walk(deps.Root)
	if err != nil {
		return nil, nil, err
	}
	existing := make(map[journal.SessionKey]journal.WalkItem, len(items))
	for _, item := range items {
		existing[item.Key] = item
	}

	var captured []Captured
	var diags []source.Diagnostic
	for _, wd := range walkDiags {
		diags = append(diags, source.Diagnostic{Path: wd.Path, Err: wd.Err})
	}
	for _, src := range deps.Sources {
		found, srcDiags, err := src.Discover(ctx)
		if err != nil {
			return captured, diags, err
		}
		diags = append(diags, srcDiags...)

		for _, d := range found {
			one, oneDiags, err := captureOne(ctx, deps, src, d, existing, machine)
			diags = append(diags, oneDiags...)
			if err != nil {
				return captured, diags, err
			}
			if one != nil {
				captured = append(captured, *one)
			}
		}
	}
	return captured, diags, nil
}

// captureOne handles one discovered session end to end. A nil Captured
// with nil error means "nothing to do" — unchanged since last capture,
// or unreadable (then with diagnostics).
func captureOne(ctx context.Context, deps Deps, src source.Source, d source.Discovered, existing map[journal.SessionKey]journal.WalkItem, machine string) (*Captured, []source.Diagnostic, error) {
	key := journal.SessionKey{Harness: src.Name(), NativeID: d.NativeID}

	prior, recapture := existing[key]
	if recapture {
		lines, sum, err := source.FingerprintFile(d.Path)
		if err != nil {
			return nil, []source.Diagnostic{{Path: d.Path, Err: err}}, nil
		}
		if lines == prior.Session.Transcript.Lines && sum == prior.Session.Transcript.SHA256 {
			return nil, nil, nil // unchanged — the silent common case (V13).
		}
	}

	var diags []source.Diagnostic
	dir, corrDiags, err := src.Correlate(ctx, d)
	diags = append(diags, corrDiags...)
	if err != nil {
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
		diags = append(diags, source.Diagnostic{Path: d.Path, Err: err})
		return nil, diags, nil
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
