package wakeverb

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/entry"
	"github.com/procrastivity/clast/internal/iostreams"
	"github.com/procrastivity/clast/internal/journal"
	"github.com/procrastivity/clast/internal/llm"
	"github.com/procrastivity/clast/internal/verbs/curate"
	"github.com/procrastivity/clast/internal/verbs/dismiss"
	wakeplumbing "github.com/procrastivity/clast/internal/verbs/wake"
)

// menu is the number-menu's own text — a verb-form presentation choice
// (flows/wake.md marks "the edit/accept/dismiss/skip retry menu's exact
// shape" non-normative for this form): a number menu rather than the old
// porcelain's single-letter one (a, e, d, s), so the choice reads
// unambiguously off piped stdin in tests as well as at a real terminal —
// the old tool's FEEL (a short, explicit per-session menu), not its
// mechanics (HANDOFF §7).
const menu = "1) Accept\n2) Edit\n3) Dismiss\n4) Skip"

const promotionMenu = "Promote a section before writing?\n1) Decision\n2) Common issue\n3) Workflow\n4) Continue (no more promotions)"

// RunInteractive implements flows/wake.md §2-§6 for the entire working
// set, presenting each drafted session in turn: the number menu decides
// accept/edit/dismiss/skip (§4), an Accept choice first offers the
// promotion sub-menu (§5). Choices are read as lines from streams.In (a
// bufio.Scanner, never an os.Stdin/isatty check) so the e2e harness can
// drive the whole thing over a piped stdin exactly the way `plumbing
// curate` already reads its stdin-borne document — a genuinely
// interactive terminal is simply the case where that pipe is a tty
// instead of a test fixture.
//
// Running out of input (scanner.Scan returns false — stdin closed) ends
// the run early: whatever was decided for earlier sessions in this call
// stands, and the remaining, not-yet-reached rows are left exactly as
// wake.Run found them (available for a later run) — the interactive
// counterpart to Auto mode's own "nothing left to decide, stop" case,
// without adding a fifth menu choice V9's four-state disposition never
// named.
//
// Every draft, menu, and prompt this function (and accept/openEditor)
// prints goes to streams.Err, never streams.Out (C2.1: stdout carries
// exactly one thing) — command.go reserves Out for the run's own closing
// summary (human text, or the --json payload), the same way --verbose
// diagnostics ride on Err everywhere else in this tree. This is what lets
// `wake --json` stay driveable over piped stdin in a script: the
// in-session chatter is on stderr, stdout carries only the final,
// parseable summary.
func RunInteractive(ctx context.Context, streams *iostreams.Streams, root string, cutoff journal.Cutoff, yesterday journal.Day, rows []wakeplumbing.Row, client *llm.Client, machine string) (Summary, error) {
	var summary Summary
	summary.Considered = len(rows)

	scanner := bufio.NewScanner(streams.In)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for i, row := range rows {
		label := unprojectedLabel
		if row.Item.Session.Project != nil {
			label = row.Item.Session.Project.Slug
		}
		if _, err := fmt.Fprintf(streams.Err, "\n== Session %d/%d: %s (%s, %s) ==\n",
			i+1, len(rows), label, row.Item.Key.DirName(), row.Day); err != nil {
			return summary, err
		}

		raw, err := Draft(ctx, root, cutoff, yesterday, row.Item, client)
		if err != nil {
			if _, werr := fmt.Fprintf(streams.Err, "  draft generation failed: %v — skipping\n", err); werr != nil {
				return summary, werr
			}
			summary.Skipped++
			continue
		}
		summary.Drafted++

		stop, err := disposition(ctx, streams, scanner, root, row.Item, raw, machine, &summary)
		if err != nil {
			return summary, err
		}
		if stop {
			break
		}
	}

	return summary, nil
}

// disposition drives one session's §4 decision loop (accept/edit/
// dismiss/skip), looping on Edit until the reviewer picks one of the
// other three or stdin runs out.
func disposition(ctx context.Context, streams *iostreams.Streams, scanner *bufio.Scanner, root string, item journal.WalkItem, raw, machine string, summary *Summary) (stop bool, err error) {
	for {
		if _, err := fmt.Fprintf(streams.Err, "\n%s\n\n%s\nChoice: ", raw, menu); err != nil {
			return false, err
		}
		choice, ok := readLine(scanner)
		if !ok {
			return true, nil
		}

		switch strings.TrimSpace(choice) {
		case "1":
			if err := accept(streams, scanner, root, item, raw, summary); err != nil {
				return false, err
			}
			return false, nil
		case "2":
			edited, ok := openEditor(ctx, raw)
			if !ok {
				if _, err := fmt.Fprintln(streams.Err, "  $EDITOR is not set (or failed) — skipping this session."); err != nil {
					return false, err
				}
				summary.Skipped++
				return false, nil
			}
			raw = edited
		case "3":
			if _, err := dismiss.Run(root, item.Key.DirName(), dismiss.DefaultReason, time.Now(), machine); err != nil {
				return false, err
			}
			summary.Dismissed++
			return false, nil
		case "4":
			summary.Skipped++
			return false, nil
		default:
			if _, err := fmt.Fprintln(streams.Err, "  unrecognized choice — enter 1, 2, 3, or 4"); err != nil {
				return false, err
			}
		}
	}
}

// accept implements §4's Accept path: first the §5 promotion sub-menu
// (repeatable — a reviewer may fold in a decision, a common issue, and a
// workflow all in one pass), then builds and writes the entry.md document
// through `curate`.
func accept(streams *iostreams.Streams, scanner *bufio.Scanner, root string, item journal.WalkItem, raw string, summary *Summary) error {
	parsed := ParseDraft(raw, FallbackTitle(item))

	for {
		if _, err := fmt.Fprintf(streams.Err, "\n%s\nChoice: ", promotionMenu); err != nil {
			return err
		}
		choice, ok := readLine(scanner)
		if !ok || strings.TrimSpace(choice) == "4" {
			break
		}

		var kind string
		switch strings.TrimSpace(choice) {
		case "1":
			kind = "Decision"
		case "2":
			kind = "Common issue"
		case "3":
			kind = "Workflow"
		default:
			if _, err := fmt.Fprintln(streams.Err, "  unrecognized choice — enter 1, 2, 3, or 4"); err != nil {
				return err
			}
			continue
		}

		if _, err := fmt.Fprint(streams.Err, "  Title: "); err != nil {
			return err
		}
		title, ok := readLine(scanner)
		if !ok {
			break
		}
		if _, err := fmt.Fprint(streams.Err, "  Body: "); err != nil {
			return err
		}
		body, ok := readLine(scanner)
		if !ok {
			break
		}

		parsed.Body = PromoteSection(parsed.Body, kind, title, body)
		switch kind {
		case "Decision":
			summary.PromotedDecisions++
		case "Common issue":
			summary.PromotedCommonIssues++
		case "Workflow":
			summary.PromotedWorkflows++
		}
	}

	doc, err := BuildEntryDocument(entry.Frontmatter{Title: parsed.Title, Tags: parsed.Tags}, parsed.Body)
	if err != nil {
		return err
	}
	if _, err := curate.Run(root, item.Key.DirName(), doc, time.Now()); err != nil {
		return err
	}
	summary.Accepted++
	summary.touchProject(item)
	return nil
}

// readLine reads one line from scanner, trimming a trailing "\r" (a
// pasted-from-Windows or crlf-piped fixture's own line ending) — ok is
// false exactly when scanner.Scan() returns false (EOF or a read error;
// scanner.Err() is not surfaced separately, mirroring curate's own
// readEntryInput which likewise folds a stdin read failure into its
// caller's error path rather than a distinct code).
func readLine(scanner *bufio.Scanner) (line string, ok bool) {
	if !scanner.Scan() {
		return "", false
	}
	return strings.TrimRight(scanner.Text(), "\r"), true
}

// openEditor implements §4's Edit path per the step brief: opens $EDITOR
// on a temp file carrying raw, waits for it to exit, and returns the
// file's content as the revised draft. ok is false — the caller's
// skip-with-message fallback (a wakeverb-owned decision, recorded per the
// step brief's own ask) — when $EDITOR is unset or blank, the temp file
// can't be created/written, or the editor command itself fails to run or
// exits non-zero; a successfully-run editor that left the file
// unchanged still returns ok true with the same content.
//
// The child process's stdio is wired to the real OS handles (os.Stdin/
// Stdout/Stderr), not streams' own In/Out — an interactive $EDITOR (vim,
// nano, ...) needs a real terminal to run at all, exactly the same as any
// other CLI tool shelling out to one; a non-interactive stand-in (a test
// fixture script that rewrites the file and exits) never touches stdin,
// so this still drives cleanly under the e2e harness's piped stdin.
func openEditor(ctx context.Context, raw string) (string, bool) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return "", false
	}

	tmp, err := os.CreateTemp("", "clast-wake-draft-*.md")
	if err != nil {
		return "", false
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.WriteString(raw); err != nil {
		_ = tmp.Close()
		return "", false
	}
	if err := tmp.Close(); err != nil {
		return "", false
	}

	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return "", false
	}
	args := append(append([]string{}, parts[1:]...), tmpPath)
	cmd := exec.CommandContext(ctx, parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", false
	}

	edited, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", false
	}
	return string(edited), true
}
