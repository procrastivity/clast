package amp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/source"
)

// searchPageRows is the -n value every search call uses. amp silently
// clamps anything over 100, and a full page is indistinguishable from a
// truncated one — no cursor or truncation marker exists — so a 100-row
// page is treated as *maybe truncated*: completeness is proven by
// subdivision, never inferred from a count.
const searchPageRows = "100"

// searchPageCap is the server's silent row cap — the trigger for
// window subdivision.
const searchPageCap = 100

// searchRow is one `threads search --json` row: {id, title, updatedAt}.
type searchRow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

// listRow is the half of a `threads list --json` row corroboration
// reads. list is never the enumerator — it drops virtual-executor
// threads and its `updated` is the lagging lastUserMessageAt semantic —
// but its messageCount feeds the 0-messages export retry hint, its id
// set cross-checks the windowed enumeration, and `updated` (a lower
// bound on the search surface's updatedAt) helps classify a miss as
// in-span or below-floor.
type listRow struct {
	ID           string `json:"id"`
	Updated      string `json:"updated"`
	MessageCount int    `json:"messageCount"`
}

// window is a complementary half-open UTC day range [after, before):
// `after:D` is inclusive and `before:D` exclusive on the server (step-01
// §6), so adjacent windows neither overlap nor gap. Dates only — the DSL
// accepts nothing finer, and clast generates the syntax itself because a
// malformed cutoff degrades to "unfiltered" on the server with no error.
type window struct {
	after  time.Time
	before time.Time
}

func (w window) days() int { return int(w.before.Sub(w.after).Hours() / 24) }

// predicates are the boolean facets a capped day window subdivides on.
// Both axes fully partition the universe (every thread is archived or
// not, pinned or not), so a leaf still at cap after both is the
// contract's named residual: >100 updates in one day is unenumerable
// below day granularity and lands as a Diagnostic. (The contract's third
// axis, repo:, needs a repo-url inventory no discovery surface carries —
// search/list rows have none — so the archived×pinned partition stands.)
type predicates struct {
	archived *bool
	pinned   *bool
}

// query renders the DSL for one window+predicate search. Only clast-
// computed values reach after:/before: — never a caller string.
func (w window) query(p predicates) string {
	parts := []string{
		"after:" + w.after.Format("2006-01-02"),
		"before:" + w.before.Format("2006-01-02"),
	}
	if p.archived != nil {
		parts = append(parts, "archived:"+strconv.FormatBool(*p.archived))
	}
	if p.pinned != nil {
		parts = append(parts, "pinned:"+strconv.FormatBool(*p.pinned))
	}
	return strings.Join(parts, " ")
}

// scanParams is the enumeration primitive's input — the surface the
// source-owned scan state drives for catch-up runs (see scanstate.go).
type scanParams struct {
	// From is the first UTC day to scan, inclusive. Catch-up passes
	// checkpoint-1d: the day overlap re-scans the boundary the DSL's
	// day granularity cannot express mid-day.
	From time.Time
	// Through is the scan's upper day bound, exclusive; zero means
	// tomorrow UTC — the windows always run ascending into the present
	// because updatedAt only moves forward, so a mid-scan update can
	// only move a thread into a not-yet-scanned window (where it is
	// then found, or deduped if seen before the jump).
	Through time.Time
	// MinTime drops rows at or before it — the ms-precision post-filter
	// a mid-day checkpoint needs, which the DSL cannot express.
	MinTime time.Time
	// IDs are by-id `id:` lookups unioned into the result — the retry
	// queue's lane. They bypass MinTime: the queue exists because
	// capture already failed for them.
	IDs []string
}

// Discover implements source.Source: the checkpoint-driven catch-up
// enumeration — windowed search over [floor-1d, tomorrow) plus the
// pending set's by-id lane. The floor and pending set live in the
// source-owned scan-state cache (scanstate.go); a missing or corrupt
// file is the contract's initial full scan from historyFloor. The state
// is only written when the transport actually answered — an absent
// binary's quiet nil,nil,nil must never move the floor, or installing
// amp later would silently skip the backlog it predates.
func (s *Source) Discover(ctx context.Context) ([]source.Discovered, []source.Diagnostic, error) {
	st := s.loadScanState()
	from := historyFloor
	var minTime time.Time
	if !st.CleanThrough.IsZero() {
		from = utcDay(st.CleanThrough).AddDate(0, 0, -1)
		minTime = st.CleanThrough
	}
	scanStart := s.clock()
	found, diags, out, err := s.enumerate(ctx, scanParams{
		From:    from,
		MinTime: minTime,
		IDs:     st.PendingIDs,
	})
	if err != nil {
		return nil, diags, err
	}
	if !s.binarySeen() {
		return nil, nil, nil // absent transport — quiet, and no state write
	}
	s.recordScan(scanStart, out, st, found)
	return found, diags, nil
}

// scanOutcome is enumerate's side-channel to the checkpoint writer —
// which coverage a later catch-up must re-scan, which pending ids the
// id: lane proved gone, which ids corroboration found unenumerated, and
// where the scan's coverage actually ended.
type scanOutcome struct {
	// dirty is the earliest window bound a Diagnostic broke clean
	// coverage over — the floor only ever advances past clean windows,
	// so this bounds the floor's move at that window's start.
	dirty time.Time
	// end is the scan's resolved upper day bound — the floor never
	// advances past coverage the scan never made (a bounded scan is
	// only the test/fault-injection shape today, but the clamp keeps
	// the seam honest for step-08/09).
	end time.Time
	// gone holds pending ids whose id: lookup came back empty and
	// clean — the thread is provably absent, so it leaves the pending
	// set rather than being probed forever.
	gone map[string]bool
	// retry holds ids corroboration saw listed but enumeration never
	// emitted — a real enumeration gap. The floor may still cross
	// their windows because each joins the pending set: recoverably
	// queued, re-probed by the id: lane next sweep, per the contract's
	// "committed or recoverably queued" checkpoint rule.
	retry map[string]bool
}

// markDirty records d as a broken-coverage bound: a diag'd window's
// span is never clean, so the floor must re-cover from it.
func (o *scanOutcome) markDirty(d time.Time) {
	if o.dirty.IsZero() || d.Before(o.dirty) {
		o.dirty = d
	}
}

// markGone records a pending id the id: lane proved absent — clean
// empty page, no row diagnostics.
func (o *scanOutcome) markGone(id string) {
	if o.gone == nil {
		o.gone = map[string]bool{}
	}
	o.gone[id] = true
}

// markRetry queues an id into the pending set — discovered by
// corroboration but never enumerated, so its capture stays owed. The
// id: lane re-probes it next sweep; it needs no floor hold because it
// is recoverably queued, exactly the checkpoint-advance rule.
func (o *scanOutcome) markRetry(id string) {
	if o.retry == nil {
		o.retry = map[string]bool{}
	}
	o.retry[id] = true
}

// enumerate runs the windowed-search enumeration: complementary UTC
// windows scanned ascending, subdivided on the 100-row silent cap, rows
// deduped by id keeping the freshest updatedAt. A spawn failure (no amp
// binary) is the absent-transport case — nil, nil, nil per the shared
// contract's quiet-absence rule; every other failure to enumerate is a
// real error, never a downgraded diagnostic and never nil,nil,nil.
func (s *Source) enumerate(ctx context.Context, p scanParams) (found []source.Discovered, diags []source.Diagnostic, out scanOutcome, err error) {
	through := p.Through
	if through.IsZero() {
		through = utcDay(s.clock()).AddDate(0, 0, 1)
	}
	out.end = through // the floor may not advance past the coverage made
	from := p.From
	if from.IsZero() {
		from = historyFloor
	}
	from = utcDay(from)

	sink := map[string]source.Discovered{}
	for start := from; start.Before(through); {
		end := start.AddDate(0, 0, 7)
		if end.After(through) {
			end = through
		}
		wdiags, err := s.scanWindow(ctx, window{after: start, before: end}, predicates{}, sink, p.MinTime, &out)
		diags = append(diags, wdiags...)
		if err != nil {
			// Spawn failure is "absent transport" only when the binary
			// never resolved this run; a binary that stops resolving
			// mid-scan is a real error, not quiet absence.
			if isErrKind(err, errSpawn) && !s.binarySeen() {
				return nil, nil, scanOutcome{}, nil // no amp binary = absent transport, quiet
			}
			return nil, diags, out, err
		}
		start = end
	}

	// The retry-queue lane: by-id lookups union into the same sink. An
	// id a window already re-emitted needs no call — the sink check is
	// the lane's whole dedup. A clean empty page proves the thread left
	// the universe and drains the pending entry; a lookup whose own
	// rows diagnosed keeps it pending rather than lose the retry.
	for _, id := range p.IDs {
		if err := ctx.Err(); err != nil {
			return nil, diags, out, err
		}
		if _, ok := sink[id]; ok {
			continue
		}
		before := len(sink)
		idDiags, err := s.lookupID(ctx, id, sink)
		diags = append(diags, idDiags...)
		if err != nil {
			if isErrKind(err, errSpawn) && !s.binarySeen() {
				return nil, nil, scanOutcome{}, nil
			}
			return nil, diags, out, err
		}
		if len(sink) == before && len(idDiags) == 0 {
			out.markGone(id)
		}
	}

	corDiags, err := s.corroborate(ctx, sink, &out, p.MinTime)
	diags = append(diags, corDiags...)
	if err != nil {
		return nil, diags, out, err
	}

	found = make([]source.Discovered, 0, len(sink))
	for _, d := range sink {
		found = append(found, d)
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].ModTime.Equal(found[j].ModTime) {
			return found[i].NativeID < found[j].NativeID
		}
		return found[i].ModTime.Before(found[j].ModTime)
	})
	return found, diags, out, nil
}

// scanWindow runs one window's search and, on the silent 100-row cap,
// subdivides: a multi-day window splits into its days first; a capped
// day splits on archived:, then pinned:. A leaf still at cap after both
// facets is the named residual — its rows are still absorbed (they are
// real threads) and the window reports a Diagnostic. Any Diagnostic
// marks its window dirty for the checkpoint — coverage under it was
// not clean, so the floor may not cross it.
func (s *Source) scanWindow(ctx context.Context, w window, p predicates, sink map[string]source.Discovered, minTime time.Time, out *scanOutcome) ([]source.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query := w.query(p)
	res, err := s.cli(ctx, []string{"threads", "search", query, "-n", searchPageRows, "--json"}, s.searchBudget)
	if err != nil {
		return nil, err
	}
	var rows []searchRow
	if err := json.Unmarshal(res.stdout, &rows); err != nil {
		return nil, fmt.Errorf("amp threads search %q: malformed JSON page: %w", query, err)
	}
	if len(rows) < searchPageCap {
		diags := s.absorbRows(sink, rows, query, minTime)
		if len(diags) > 0 {
			out.markDirty(w.after)
		}
		return diags, nil
	}
	if w.days() > 1 {
		var diags []source.Diagnostic
		for d := w.after; d.Before(w.before); {
			next := d.AddDate(0, 0, 1)
			if next.After(w.before) {
				next = w.before
			}
			dDiags, err := s.scanWindow(ctx, window{after: d, before: next}, p, sink, minTime, out)
			diags = append(diags, dDiags...)
			if err != nil {
				return diags, err
			}
			d = next
		}
		return diags, nil
	}
	if p.archived == nil {
		return s.subdivide(ctx, w, p, sink, minTime, out, "archived")
	}
	if p.pinned == nil {
		return s.subdivide(ctx, w, p, sink, minTime, out, "pinned")
	}
	diags := s.absorbRows(sink, rows, query, minTime)
	out.markDirty(w.after) // the capped-leaf residual is never clean coverage
	return append(diags, source.Diagnostic{
		Path: "amp threads search '" + query + "'",
		Err: fmt.Errorf("window returned %d rows at the %d-row cap after archived/pinned subdivision; threads beyond the cap are unenumerable",
			len(rows), searchPageCap),
	}), nil
}

// subdivide splits a capped day window on one boolean facet.
func (s *Source) subdivide(ctx context.Context, w window, p predicates, sink map[string]source.Discovered, minTime time.Time, out *scanOutcome, facet string) ([]source.Diagnostic, error) {
	var diags []source.Diagnostic
	for _, b := range []bool{false, true} {
		sub := p
		v := b // a fresh variable per branch — p carries a pointer
		switch facet {
		case "archived":
			sub.archived = &v
		case "pinned":
			sub.pinned = &v
		}
		dDiags, err := s.scanWindow(ctx, w, sub, sink, minTime, out)
		diags = append(diags, dDiags...)
		if err != nil {
			return diags, err
		}
	}
	return diags, nil
}

// absorbRows folds one search page into the sink, deduping by id and
// keeping the freshest updatedAt on collision (a thread seen twice — a
// mid-scan forward jump or a capped leaf's re-scan — keeps its latest
// known revision). Rows with no id or an unparseable updatedAt are
// per-item diagnostics, never fatal.
func (s *Source) absorbRows(sink map[string]source.Discovered, rows []searchRow, query string, minTime time.Time) []source.Diagnostic {
	var diags []source.Diagnostic
	for _, row := range rows {
		if row.ID == "" {
			diags = append(diags, source.Diagnostic{
				Path: "amp threads search '" + query + "'",
				Err:  errors.New("search row carries no id"),
			})
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
		if err != nil {
			diags = append(diags, source.Diagnostic{
				Path: threadURL(row.ID),
				Err:  fmt.Errorf("unparseable updatedAt %q", row.UpdatedAt),
			})
			continue
		}
		if !minTime.IsZero() && !updatedAt.After(minTime) {
			continue
		}
		if prev, ok := sink[row.ID]; ok && !updatedAt.After(prev.ModTime) {
			continue
		}
		sink[row.ID] = source.Discovered{
			NativeID: row.ID,
			Path:     threadURL(row.ID),
			ModTime:  updatedAt,
		}
	}
	return diags
}

// lookupID emits one thread by `id:` query — the retry-queue lane. A
// deleted thread returns an empty page: nothing to emit, no diagnostic
// (the prior capture's record stands).
func (s *Source) lookupID(ctx context.Context, id string, sink map[string]source.Discovered) ([]source.Diagnostic, error) {
	query := "id:" + id
	res, err := s.cli(ctx, []string{"threads", "search", query, "-n", searchPageRows, "--json"}, s.searchBudget)
	if err != nil {
		return nil, err
	}
	var rows []searchRow
	if err := json.Unmarshal(res.stdout, &rows); err != nil {
		return nil, fmt.Errorf("amp threads search %q: malformed JSON page: %w", query, err)
	}
	return s.absorbRows(sink, rows, query, time.Time{}), nil
}

// corroborate runs the one `threads list` pass the contract allows —
// corroboration/enrichment only, never the enumerator (list drops
// virtual-executor threads and its offset walk is lossy). Its
// messageCounts feed the 0-messages export retry hint; an id it lists
// that enumeration missed is a Diagnostic — a real enumeration gap —
// AND joins the pending set via out.retry: the floor may only advance
// over discoveries committed or recoverably queued, and a missed id is
// neither until it is queued. A list failure or a capped (possibly
// truncated) page downgrades to one diagnostic: enumeration stands
// alone.
func (s *Source) corroborate(ctx context.Context, sink map[string]source.Discovered, out *scanOutcome, minTime time.Time) ([]source.Diagnostic, error) {
	const op = "amp threads list --include-archived --limit 500 --json"
	res, err := s.cli(ctx, []string{"threads", "list", "--include-archived", "--limit", "500", "--json"}, s.listBudget)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // cancellation stays fatal — never a diag
		}
		return []source.Diagnostic{{Path: op, Err: fmt.Errorf("corroboration unavailable: %w", err)}}, nil
	}
	var rows []listRow
	if err := json.Unmarshal(res.stdout, &rows); err != nil {
		return []source.Diagnostic{{Path: op, Err: fmt.Errorf("corroboration page malformed: %w", err)}}, nil
	}
	counts := make(map[string]int, len(rows))
	for _, r := range rows {
		if r.ID != "" {
			counts[r.ID] = r.MessageCount
		}
	}
	s.mu.Lock()
	s.listCounts = counts
	s.mu.Unlock()

	if len(rows) >= 500 {
		return []source.Diagnostic{{Path: op, Err: errors.New(
			"threads list returned its 500-row cap — a possibly-truncated universe; corroboration skipped")}}, nil
	}
	var diags []source.Diagnostic
	var committed map[string]time.Time
	committedLoaded := false
	for _, r := range rows {
		if r.ID == "" {
			continue
		}
		if _, ok := sink[r.ID]; ok {
			continue
		}
		// A listed id the enumeration never emitted is a real gap only
		// when the thread plausibly sits inside the scanned span — the
		// windows emit every row whose updatedAt clears MinTime, so a
		// thread wholly below the floor is out of scope, not missed.
		// (Queueing every such row was the pending treadmill: the list
		// endpoint sees the whole account history each sweep while the
		// windows legitimately cover only the tail.) The evidence,
		// cheapest first: the row's own `updated` stamp — the lagging
		// lastUserMessageAt, a lower bound on the search surface's
		// updatedAt — clearing the floor proves the span on its own.
		// Then the journal's committed revision: none at all is a
		// never-captured thread (a seeded or foreign floor may not hide
		// it), and one above the floor is a revision this scan should
		// have re-emitted. A miss failing every arm is committed
		// at-or-below the floor — out of span, quiet, unqueued. The
		// residue that pairing misses — moved past the floor, silently
		// dropped, both stamps stale — is the accepted trade: the
		// alternative re-queues the entire below-floor universe every
		// sweep.
		inSpan := minTime.IsZero()
		if !inSpan {
			if t, err := time.Parse(time.RFC3339Nano, r.Updated); err == nil && t.After(minTime) {
				inSpan = true
			}
		}
		if !inSpan {
			if !committedLoaded {
				committed, committedLoaded = s.committedRevisions(), true
			}
			rev, ok := committed[r.ID]
			inSpan = !ok || rev.IsZero() || rev.After(minTime)
		}
		if !inSpan {
			continue
		}
		diags = append(diags, source.Diagnostic{
			Path: threadURL(r.ID),
			Err:  errors.New("listed by threads list but not enumerated by windowed search"),
		})
		out.markRetry(r.ID) // queued pending — the gap stays recoverable, never a silent miss under the floor
	}
	return diags, nil
}

// committedRevisions maps each committed amp session's native id to its
// recorded last_active_at — the journal's own answer to "was this id's
// span already covered". It exists for corroborate's in-span check and
// is consulted only when a floor exists and a miss needs classifying.
// An unbound journal or a failed walk yields an empty map, under which
// every miss queues — the conservative never-lose-data posture,
// identical to treating every miss as in-span.
func (s *Source) committedRevisions() map[string]time.Time {
	revs := map[string]time.Time{}
	s.mu.Lock()
	root := s.journalRoot
	s.mu.Unlock()
	if root == "" {
		return revs
	}
	items, _, err := journal.Walk(root)
	if err != nil {
		return revs
	}
	for _, it := range items {
		if it.Key.Harness == Name {
			revs[it.Key.NativeID] = it.Session.LastActiveAt
		}
	}
	return revs
}

// reportedCount is the corroboration map's messageCount for id — the
// "nonempty" half of the 0-messages export rule. 0 means unknown or
// truly prompt-free.
func (s *Source) reportedCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCounts[id]
}

// utcDay is t's UTC calendar day at 00:00 — window bounds are always
// whole UTC days because the DSL's finest cutoff is one.
func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
