package amp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// stderrCapBytes bounds captured stderr — an error excerpt, never the
// payload. 64 KiB keeps the longest real error text with room to spare.
const stderrCapBytes = 64 << 10

// errOverCap is the capWriter's Write-side sentinel; it stops the io.Copy
// feeding the buffer and never surfaces verbatim — realRun converts it
// to an errCap cliError.
var errOverCap = errors.New("amp: output byte cap exceeded")

// callBudget bounds one CLI invocation in both resources a subprocess
// can cost an unattended sweep: wall time and output bytes.
type callBudget struct {
	timeout  time.Duration
	maxBytes int64
}

// cliResult is one invocation's raw outcome: exit code plus the
// cap-bounded captured streams. Classification — exit codes, stderr
// text, timeouts — happens in cli, so an injected runFunc exercises the
// same post-exit behavior a real process does.
type cliResult struct {
	stdout []byte
	stderr []byte
	code   int
}

// runFunc is the subprocess seam: one CLI invocation under a budget.
// ctx already carries the call's timeout when the runner sees it.
type runFunc func(ctx context.Context, args []string, budget callBudget) (cliResult, error)

// errKind classifies a CLI failure for the retry/diagnostic rules that
// consume it (contract step-02 §5): a missing binary means the transport
// is absent, not failed; not-exist is a per-item diagnostic and never
// retried; auth fails closed fast; transport-ish failures retry inside
// the export bound.
type errKind int

const (
	errSpawn     errKind = iota // the binary could not be resolved — for Discover this is "absent"
	errStart                    // resolved but the process could not be started
	errTimeout                  // the per-call time budget expired
	errCap                      // stdout exceeded the byte cap
	errExit                     // non-zero exit with unrecognized stderr
	errOffline                  // "Cannot reach Amp servers"
	errNotExist                 // "Thread … does not exist."
	errInvalidID                // "Invalid thread URL or ID"
	errAuth                     // auth/login material missing or rejected
	// errQuarantined is the synthesized refusal for an export already
	// recorded over the byte cap — an error no transport call was spent
	// on, so quarantine reads in the ledger cost zero.
	errQuarantined
)

// cliError is one bounded CLI failure: the probed operation, a stderr
// excerpt, and the kind the retry rules switch on.
type cliError struct {
	kind   errKind
	op     string
	detail string
	err    error
}

func (e *cliError) Error() string {
	if e.detail != "" {
		return e.op + ": " + e.detail
	}
	if e.err != nil {
		return fmt.Sprintf("%s: %s", e.op, e.err)
	}
	return e.op + ": failed"
}

func (e *cliError) Unwrap() error { return e.err }

// cli runs one amp invocation under budget. The timeout is a child
// context of the caller's, checked in order: caller cancellation wins
// and returns the context error verbatim (cancellation is fatal per the
// shared contract, never reclassified), then the per-call deadline lands
// as an ordinary bounded error that deliberately does NOT unwrap
// context.DeadlineExceeded — capture treats that match as a fatal run
// abort, and an item's own time budget is not the run's.
func (s *Source) cli(ctx context.Context, args []string, budget callBudget) (cliResult, error) {
	op := "amp " + strings.Join(args, " ")
	call := ctx
	cancel := context.CancelFunc(func() {})
	if budget.timeout > 0 {
		call, cancel = context.WithTimeout(ctx, budget.timeout)
	}
	defer cancel()

	run := s.run
	if run == nil {
		run = s.realRun
	}
	res, err := run(call, args, budget)
	// Any outcome that is not a spawn failure means the transport
	// exists — including an injected runner, which is why the marker
	// lives here and not only inside realRun: the scan-state writer
	// gates on binarySeen, and a fake transport must count as real.
	if !isErrKind(err, errSpawn) {
		s.binaryResolved()
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if err != nil {
		if errors.Is(call.Err(), context.DeadlineExceeded) {
			return res, &cliError{
				kind: errTimeout, op: op,
				detail: fmt.Sprintf("timed out after %s", budget.timeout),
			}
		}
		var ce *cliError
		if errors.As(err, &ce) {
			return res, ce
		}
		return res, &cliError{kind: errStart, op: op, err: err}
	}
	if res.code != 0 {
		// A process killed by the per-call deadline reports the budget,
		// not the bare exit status (its stderr is empty/mid-flight).
		if errors.Is(call.Err(), context.DeadlineExceeded) {
			return res, &cliError{
				kind: errTimeout, op: op,
				detail: fmt.Sprintf("timed out after %s", budget.timeout),
			}
		}
		return res, classifyExit(op, res.stderr)
	}
	if int64(len(res.stdout)) > budget.maxBytes {
		// The real runner caps mid-stream; an injected runner could
		// still over-return, so the budget is enforced at the seam
		// boundary too.
		return res, &cliError{
			kind: errCap, op: op,
			detail: fmt.Sprintf("stdout exceeded the %d-byte cap", budget.maxBytes),
		}
	}
	return res, nil
}

// realRun is the production runner: one exec.CommandContext call. Stdin
// stays nil (the null device) so nothing can prompt, and the environment
// is inherited untouched — AMP_API_KEY or the CLI's stored credentials
// are the harness's own convention, used ambiently; clast injects
// nothing.
func (s *Source) realRun(ctx context.Context, args []string, budget callBudget) (cliResult, error) {
	op := "amp " + strings.Join(args, " ")
	bin := s.bin
	if bin == "" {
		bin = binName
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return cliResult{}, &cliError{kind: errSpawn, op: op, err: err}
	}
	s.binaryResolved() // the transport exists — later failures are never "absent"
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr capWriter
	stdout.limit = budget.maxBytes
	stderr.limit = stderrCapBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	res := cliResult{stdout: stdout.buf.Bytes(), stderr: stderr.buf.Bytes()}
	if stdout.over {
		return res, &cliError{
			kind: errCap, op: op,
			detail: fmt.Sprintf("stdout exceeded the %d-byte cap", budget.maxBytes),
		}
	}
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		res.code = ee.ExitCode()
	default:
		return res, &cliError{kind: errStart, op: op, err: runErr}
	}
	return res, nil
}

// capWriter is a bounded bytes.Buffer for a child stream: it retains up
// to limit bytes, then fails the io.Copy feeding it — closing the pipe
// so the child dies on EPIPE rather than clast buffering unbounded
// output.
type capWriter struct {
	buf   bytes.Buffer
	limit int64
	over  bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.over {
		return 0, errOverCap
	}
	if room := w.limit - int64(w.buf.Len()); int64(len(p)) > room {
		if room > 0 {
			_, _ = w.buf.Write(p[:room])
		}
		w.over = true
		return 0, errOverCap
	}
	return w.buf.Write(p)
}

// classifyExit maps a non-zero exit to an errKind off its stderr text —
// the failure texts qualification pinned. Anything unrecognized is a
// plain errExit: real, retried where the caller retries, never
// downgraded.
func classifyExit(op string, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	low := strings.ToLower(msg)
	kind := errExit
	switch {
	case strings.Contains(low, "does not exist"):
		kind = errNotExist
	case strings.Contains(low, "invalid thread"):
		kind = errInvalidID
	case strings.Contains(low, "cannot reach"):
		kind = errOffline
	case authy(low):
		kind = errAuth
	}
	return &cliError{kind: kind, op: op, detail: msg}
}

// authy reports whether a stderr text smells like a credential problem —
// the fail-closed guard: an unattended run must surface auth failure as
// a real error, never hang on or honor a prompt (stdin is already the
// null device; this names the error accurately instead of errExit).
func authy(low string) bool {
	for _, frag := range []string{
		"unauthorized", "not authenticated", "authentication",
		"not logged in", "log in", "login", "sign in", "signin",
		"api key", "401", "forbidden",
	} {
		if strings.Contains(low, frag) {
			return true
		}
	}
	return false
}

// isErrKind reports whether err is a cliError of kind k.
func isErrKind(err error, k errKind) bool {
	var ce *cliError
	return errors.As(err, &ce) && ce.kind == k
}

// terminalExportErr reports whether retrying cannot possibly help: the
// thread is gone, the request itself is malformed, the binary vanished,
// auth failed, the export is quarantined over-cap, or stdout already
// exceeded this run's byte cap — none of which heal inside the ~10s
// retry bound (a capped export only ever grows, so a same-budget retry
// is provably wasted).
func terminalExportErr(err error) bool {
	return isErrKind(err, errNotExist) ||
		isErrKind(err, errInvalidID) ||
		isErrKind(err, errAuth) ||
		isErrKind(err, errSpawn) ||
		isErrKind(err, errQuarantined) ||
		isErrKind(err, errCap)
}
