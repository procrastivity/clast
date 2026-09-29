package analyzeverb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/clasterr"
)

// shutdownGrace bounds how long serve waits for in-flight requests once
// it is told to stop.
const shutdownGrace = 5 * time.Second

// renderPage renders page to bytes. It is the one render path: writePage
// (--out) and NewHandler (serve mode) both go through it, so both emit
// byte-identical HTML for the same page.
func renderPage(page Page) ([]byte, error) {
	var buf bytes.Buffer
	if err := Render(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// NewHandler returns the serve-mode handler: GET/HEAD on "/" gathers a
// fresh page and renders it; "/t/<session-id>" and
// "/t/<session-id>/agents/<agent-id>" render a session's or a subagent's
// transcript. Gather runs per request so the page always reflects the
// current journal and cache. A gather or render error is answered with a
// plain-text 500 and, when logf is non-nil, logged.
func NewHandler(gather func() (Page, error), logf func(format string, args ...any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var serveRoute func(w http.ResponseWriter, r *http.Request)
		switch {
		case r.URL.Path == "/":
			serveRoute = func(w http.ResponseWriter, r *http.Request) { serveOverview(w, r, gather, logf) }
		case strings.HasPrefix(r.URL.Path, "/t/"):
			sessionID, agentID, ok := parseTranscriptPath(r.URL.EscapedPath())
			if !ok {
				http.NotFound(w, r)
				return
			}
			serveRoute = func(w http.ResponseWriter, r *http.Request) {
				serveTranscript(w, r, gather, logf, sessionID, agentID)
			}
		default:
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		serveRoute(w, r)
	})
}

// parseTranscriptPath splits an escaped "/t/<session-id>" or
// "/t/<session-id>/agents/<agent-id>" path into its unescaped ids. It
// reports false for any other shape, and for an id that is empty, "." or
// "..", or that carries a slash, backslash or NUL after unescaping — an
// id is only ever compared with ids the journal lists, but no such
// oddity is worth routing.
func parseTranscriptPath(escaped string) (sessionID, agentID string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(escaped, "/t/"), "/")
	switch {
	case len(parts) == 1:
	case len(parts) == 3 && parts[1] == "agents":
	default:
		return "", "", false
	}
	ids := make([]string, 0, 2)
	for _, i := range []int{0, 2} {
		if i >= len(parts) {
			break
		}
		id, err := url.PathUnescape(parts[i])
		if err != nil || id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
			return "", "", false
		}
		ids = append(ids, id)
	}
	if len(ids) == 2 {
		return ids[0], ids[1], true
	}
	return ids[0], "", true
}

func serveOverview(w http.ResponseWriter, r *http.Request, gather func() (Page, error), logf func(string, ...any)) {
	page, err := gather()
	var body []byte
	if err == nil {
		body, err = renderPage(page)
	}
	if err != nil {
		serverError(w, r, logf, err)
		return
	}
	writeHTML(w, body)
}

// serveTranscript answers one transcript route. The session is looked up
// in the gathered window, so a transcript is reachable only from a page
// the user could have clicked from; one outside the window is a 404. A
// read that failed midway still renders what it read, under a warning
// banner — an unfinished transcript beats none. One that yielded nothing
// is a 500.
func serveTranscript(w http.ResponseWriter, r *http.Request, gather func() (Page, error), logf func(string, ...any), sessionID, agentID string) {
	page, err := gather()
	if err != nil {
		serverError(w, r, logf, err)
		return
	}
	var sess Session
	found := false
	for _, s := range page.Sessions() {
		if s.ID == sessionID {
			sess, found = s, true
			break
		}
	}
	if !found {
		http.Error(w, fmt.Sprintf("session %s is not in the window %s to %s", sessionID, page.WindowStart, page.Day), http.StatusNotFound)
		return
	}
	tp, err := GatherTranscript(sess.dir, sess, agentID)
	if err != nil {
		var ce *clasterr.Error
		switch {
		case errors.As(err, &ce) && ce.Code == "analyze.agent-not-found":
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case len(tp.Events) == 0:
			serverError(w, r, logf, err)
			return
		}
		if logf != nil {
			logf("analyze: %s %s: partial transcript: %v", r.Method, r.URL.Path, err)
		}
		tp.Warning = "This transcript could not be read to the end; the events read so far are shown."
	}
	var buf bytes.Buffer
	if err := RenderTranscript(&buf, tp); err != nil {
		serverError(w, r, logf, err)
		return
	}
	writeHTML(w, buf.Bytes())
}

func serverError(w http.ResponseWriter, r *http.Request, logf func(string, ...any), err error) {
	if logf != nil {
		logf("analyze: %s %s: %v", r.Method, r.URL.Path, err)
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeHTML(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// serve runs handler on ln until ctx is cancelled, then shuts down
// gracefully. A stop requested through ctx is a clean exit (nil).
func serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close()
	}
	return nil
}

// logfTo returns a printf-style logger writing one line per call to w.
func logfTo(w io.Writer) func(string, ...any) {
	return func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, format+"\n", args...)
	}
}
