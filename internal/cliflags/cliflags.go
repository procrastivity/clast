// Package cliflags carries the two global flags (--json, -v/--verbose) from
// root's PersistentPreRunE down to every verb via context (CONTRACT.md
// C2.3): both bind once at root, read via context by every verb; a verb
// never redeclares either flag.
package cliflags

import "context"

type contextKey struct{}

// Flags is the set of global flag values threaded through context. A tool
// that grows more global flags (wip added --as-role with an env fallback)
// extends this struct and root's PersistentPreRunE together — nowhere else.
type Flags struct {
	JSON    bool
	Verbose bool
}

// WithFlags returns a context carrying f, for verbs to read via FromContext.
func WithFlags(ctx context.Context, f Flags) context.Context {
	return context.WithValue(ctx, contextKey{}, f)
}

// FromContext returns the Flags stored by WithFlags, or the zero value if
// none were stored.
func FromContext(ctx context.Context) Flags {
	f, _ := ctx.Value(contextKey{}).(Flags)
	return f
}
