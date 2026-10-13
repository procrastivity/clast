package pi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"
)

// maxHeaderLine bounds the first-line read: Pi's own header scan cap.
const maxHeaderLine = 1 << 20

// idRule is Pi's session id rule (assertValidSessionId). The id becomes
// a journal directory name, so this is a security boundary.
var idRule = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// errSilentSkip marks a file Discover skips without a diagnostic: zero
// bytes, or a header still being written.
var errSilentSkip = errors.New("pi: skipped silently")

// header is what line 1 of a v3 session file yields.
type header struct {
	ID string
	// Timestamp is the parsed header timestamp; zero when absent or
	// unparseable (StartedAt stays zero then; Capture, step-05).
	Timestamp time.Time
	// CWD is the header cwd exactly as Pi wrote it ("" when absent or
	// not a string). Correlate applies the absolute-path rule.
	CWD string
}

// readHeaderFile opens path and reads its header. The shared reader for
// Discover, Correlate and (step-05) Capture. Errors: an open/read error
// as is, errSilentSkip, or a descriptive rejection (no path prefix).
func readHeaderFile(path string) (header, error) {
	f, err := os.Open(path)
	if err != nil {
		return header{}, err
	}
	// Deliberate discard: read-only file.
	defer func() { _ = f.Close() }()
	return readHeader(f)
}

// readHeader reads at most maxHeaderLine bytes of the first line of r
// and applies the header, id and version rules.
func readHeader(r io.Reader) (header, error) {
	buf := make([]byte, maxHeaderLine+1)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return header{}, err
	}
	buf = buf[:n]
	if n == 0 {
		return header{}, errSilentSkip
	}
	line, terminated := buf, false
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		line, terminated = buf[:i], true
	} else if n > maxHeaderLine {
		return header{}, fmt.Errorf("header line longer than %d bytes", maxHeaderLine)
	}

	var fields map[string]json.RawMessage
	if jerr := json.Unmarshal(line, &fields); jerr != nil {
		if !terminated {
			return header{}, errSilentSkip // header being written
		}
		return header{}, errors.New("first line is not a session header")
	}
	var typ string
	_ = json.Unmarshal(fields["type"], &typ)
	var id string
	idErr := json.Unmarshal(fields["id"], &id)
	if typ != "session" || idErr != nil {
		return header{}, errors.New("first line is not a session header")
	}
	if !idRule.MatchString(id) {
		return header{}, fmt.Errorf("session id %q is not a valid id", id)
	}
	if err := checkVersion(fields["version"]); err != nil {
		return header{}, err
	}

	h := header{ID: id}
	_ = json.Unmarshal(fields["cwd"], &h.CWD)
	var ts string
	if json.Unmarshal(fields["timestamp"], &ts) == nil {
		if t, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
			h.Timestamp = t
		}
	}
	return h, nil
}

// checkVersion applies the version gate: the supported set is exactly
// {3}; missing, non-integer, lower and higher all share one diagnostic.
func checkVersion(raw json.RawMessage) error {
	what := "missing"
	if len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		lit := string(bytes.TrimSpace(raw))
		if v, err := strconv.ParseInt(lit, 10, 64); err == nil && v == supportedVersion {
			return nil
		}
		what = lit
	}
	return fmt.Errorf("unsupported session version %s (supported: %d)", what, supportedVersion)
}
