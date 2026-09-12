package claudecode

// ShimCommand is the V33 SessionStart shim: the one inline command the
// splice (splice.go) writes as the hook's `command` field. No shim file
// ever touches disk and there is no `--background` flag (V13 already makes
// `clast plumbing capture` silent and self-sufficient) — this is "the
// smallest shim that invokes the binary and re-expresses no verbs" (C4.10).
//
// Read left to right: `command -v clast` checks PATH; if clast is not
// found, the `&&` short-circuits and nothing else runs. If it is found,
// `clast plumbing capture` is launched in a backgrounded subshell with
// both stdout and stderr discarded, so session start never blocks on it
// and never prints anything on its account. The trailing `; exit 0` is a
// separate statement, unconditional on everything before it, so the hook
// always reports success — session start can never fail on clast's
// account, whether or not clast is installed.
//
// Exact quoting is a build detail (SURFACE V33): this is the byte-golden
// value, pinned the moment the splice golden tests first asserted it
// (testdata/golden/splice/), and never touched after.
const ShimCommand = `command -v clast >/dev/null 2>&1 && (clast plumbing capture >/dev/null 2>&1 &); exit 0`
