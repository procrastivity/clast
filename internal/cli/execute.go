package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/exitcode"
	"github.com/procrastivity/clast/internal/iostreams"
)

// Execute runs root and returns the process exit code, per the contract's
// exit-code table (C2.4). It distinguishes a Cobra argument-parsing error
// (code 2) from a verb-raised structured error (codes 1/3/4, read off the
// error's own code field) and from a silent parity exit (the verb's own
// code, nothing rendered) — distinct return paths, not one blanket
// non-zero exit.
func Execute(root *cobra.Command, streams *iostreams.Streams) int {
	cmd, err := root.ExecuteC()
	if err == nil {
		return exitcode.Success
	}

	var serr *exitcode.SilentError
	if errors.As(err, &serr) {
		return serr.Code
	}

	var terr *clasterr.Error
	if errors.As(err, &terr) {
		jsonOut, _ := cmd.Flags().GetBool("json")
		clasterr.Render(streams.Err, verbPath(root, cmd), terr, jsonOut)
		return exitcode.FromError(terr)
	}

	// Anything that isn't our own structured error type reached here via
	// Cobra's own argument-parsing path (bad flags, unknown command,
	// missing required arg) — a usage error, not a verb failure.
	_, _ = fmt.Fprintf(streams.Err, "clast: %s\n", err)
	return exitcode.Usage
}

// verbPath names the failing verb for the human-mode
// "clast: <verb>: <message>" line — the full subcommand path minus the
// root command's own name, so nested verbs (e.g. "step create") read
// naturally without redesign later.
func verbPath(root, cmd *cobra.Command) string {
	path := strings.TrimPrefix(cmd.CommandPath(), root.Name())
	return strings.TrimSpace(path)
}
