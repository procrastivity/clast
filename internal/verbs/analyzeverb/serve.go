package analyzeverb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
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
// fresh page and renders it. Gather runs per request so the page always
// reflects the current journal and cache. A gather or render error is
// answered with a plain-text 500 and, when logf is non-nil, logged.
func NewHandler(gather func() (Page, error), logf func(format string, args ...any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page, err := gather()
		var body []byte
		if err == nil {
			body, err = renderPage(page)
		}
		if err != nil {
			if logf != nil {
				logf("analyze: %s %s: %v", r.Method, r.URL.Path, err)
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
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
