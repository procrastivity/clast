package analyzeverb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/procrastivity/clast/internal/clasterr"
	"github.com/procrastivity/clast/internal/cliflags"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/surface"
)

// defaultDayArg and defaultSince are analyze's window: the last week
// ending today — deliberately not retro's single-day `yesterday`, since
// the explorer is for comparing days.
const (
	defaultDayArg = "today"
	defaultSince  = "-6d"
)

// Command constructs the top-level `clast analyze [<day>]` verb: an HTML
// explorer over a journal window. It reads summaries from the retro
// cache and never calls the LLM.
func Command(streams *iostreams.Streams) *cobra.Command {
	var since, addr, out string

	cmd := &cobra.Command{
		Use:   "analyze [<day>]",
		Short: "explore a journal window as an HTML page (entries beside cached retro summaries)",
		Long: "analyze renders an HTML explorer over a journal window: an overview table with entry " +
			"and retro word counts, a day→project sidebar, each curated session's entry draft beside " +
			"its retro summary, dismissed sessions greyed with their reason, and breadcrumbs by " +
			"project. <day> is the V5 day grammar (YYYY-MM-DD, today, yesterday, -Nd); default: " +
			"today. --since <duration> sets how far the window reaches back from <day> (-Nd or -Nw); " +
			"default: -6d, and -0d gives exactly <day>. Summaries come only from the cache `clast " +
			"retro` fills under $XDG_CACHE_HOME/clast/retro/; analyze never calls the LLM, and a " +
			"session with no cached summary shows a hint to run `clast retro <day>`. --out <file> " +
			"writes the page as one static HTML file and exits; it excludes --addr. Without --out, " +
			"analyze serves the page on --addr (default 127.0.0.1:0), prints the URL, and stops on " +
			"Ctrl-C. This flow writes nothing to the journal.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cliflags.FromContext(cmd.Context())

			dayArg := defaultDayArg
			if len(args) == 1 {
				dayArg = args[0]
			}
			gather := func() (Page, error) { return Gather(dayArg, since) }

			if out == "" {
				// TODO(step-04): serve gather per request on addr.
				return clasterr.New("analyze.serve-unavailable",
					"analyze: serve mode is not implemented yet; use --out <file.html>")
			}

			page, err := gather()
			if err != nil {
				return err
			}
			if err := writePage(out, page); err != nil {
				return err
			}

			if flags.Verbose {
				st := page.Stats()
				if _, err := fmt.Fprintf(streams.Err,
					"analyze: %s to %s, %d day(s), %d session(s), %d unsummarized\n",
					page.WindowStart, page.Day, len(page.Days), len(page.Sessions()), st.Unsummarized,
				); err != nil {
					return err
				}
			}
			if flags.JSON {
				b, err := json.Marshal(struct {
					Out         string `json:"out"`
					WindowStart string `json:"window_start"`
					Day         string `json:"day"`
				}{out, string(page.WindowStart), string(page.Day)})
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(streams.Out, string(b))
				return err
			}
			_, err = fmt.Fprintf(streams.Out, "wrote %s\n", out)
			return err
		},
	}

	cmd.Flags().StringVar(&since, "since", defaultSince, "how far the window reaches back from <day> (-Nd or -Nw)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "address to serve the page on (host:port)")
	cmd.Flags().StringVar(&out, "out", "", "write the page to this file and exit instead of serving")
	cmd.MarkFlagsMutuallyExclusive("addr", "out")

	surface.Annotate(cmd, surface.Plumbing)
	return cmd
}

// writePage renders page and writes it to path, via a temp file in the
// same directory renamed into place, so a failed render or write never
// leaves a truncated page behind.
func writePage(path string, page Page) error {
	var buf bytes.Buffer
	if err := Render(&buf, page); err != nil {
		return err
	}
	fail := func(err error) error {
		return clasterr.New("analyze.write-failed", fmt.Sprintf("analyze: writing %s: %v", path, err))
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-analyze-*")
	if err != nil {
		return fail(err)
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(buf.Bytes())
	closeErr := tmp.Close()
	if err := firstErr(writeErr, closeErr); err != nil {
		_ = os.Remove(tmpPath)
		return fail(err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fail(err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fail(err)
	}
	return nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
