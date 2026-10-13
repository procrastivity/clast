package pi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"time"
)

// maxLineSize bounds one JSONL line the scan buffers (as claude): a
// longer line is a bad line, counted and skipped, never buffered.
const maxLineSize = 16 << 20

// rec is the per-entry record the leaf walk needs: no message bodies.
type rec struct {
	parent      string
	hasParent   bool
	user        bool
	assistant   bool
	substantive bool
}

// entryLine is the half of an entry line facts read.
type entryLine struct {
	Type      string          `json:"type"`
	ID        *string         `json:"id"`
	ParentID  json.RawMessage `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   *struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stopReason"`
	} `json:"message"`
}

// scan is the copy pass's write-side observer: it hashes every byte,
// counts lines like source.FingerprintFile (newline bytes, plus one for
// a non-empty unterminated final line), validates line 1 as the header,
// and folds each complete later line into the fact state.
type scan struct {
	wantID string
	h      hash.Hash
	lines  int
	buf    []byte
	over   bool
	empty  bool
	last   byte

	hdr    header
	hdrOK  bool
	fatal  error // header-level failure; aborts the copy
	lineNo int   // 1-based number of the line being buffered

	byID       map[string]rec
	leaf       string
	haveLeaf   bool
	lastActive time.Time
	badLines   int
	firstBad   int

	wholeUser, wholeAsst int
	wholeSubst           bool
}

func newScan(wantID string) *scan {
	return &scan{wantID: wantID, h: sha256.New(), empty: true, lineNo: 1, byID: map[string]rec{}}
}

// Write implements io.Writer for the tee. It errors only to abort the
// copy once the header has failed.
func (c *scan) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if c.fatal != nil {
		return 0, c.fatal
	}
	c.empty = false
	c.last = p[n-1]
	c.h.Write(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			c.appendPartial(p)
			break
		}
		c.appendPartial(p[:i])
		c.endLine()
		if c.fatal != nil {
			return 0, c.fatal
		}
		p = p[i+1:]
	}
	return n, nil
}

func (c *scan) appendPartial(b []byte) {
	if c.over {
		return
	}
	if len(c.buf)+len(b) > maxLineSize {
		c.over = true
		c.buf = c.buf[:0]
		return
	}
	c.buf = append(c.buf, b...)
}

// endLine closes a newline-terminated line.
func (c *scan) endLine() {
	c.lines++
	c.handle(true)
	c.buf = c.buf[:0]
	c.over = false
	c.lineNo++
}

// finish closes an unterminated final line (a live file mid-append).
func (c *scan) finish() {
	if c.empty {
		c.fatal = errNoHeader
		return
	}
	if c.last == '\n' {
		return
	}
	c.lines++
	c.handle(false)
}

// handle processes one complete line. terminated is false only for the
// final line of a file with no trailing newline.
func (c *scan) handle(terminated bool) {
	if c.lineNo == 1 {
		c.header(terminated)
		return
	}
	if c.over {
		if terminated {
			c.bad()
		}
		return // oversize torn tail: silent, like any torn tail
	}
	if len(bytes.TrimSpace(c.buf)) == 0 {
		return // blank line: counted in Lines only
	}
	if !terminated && !json.Valid(c.buf) {
		return // torn tail: copied, excluded from facts, no diagnostic
	}
	var e entryLine
	if json.Unmarshal(c.buf, &e) != nil {
		c.bad()
		return
	}
	if e.Type == "session" {
		return // a later header line is never an entry
	}
	if e.ID == nil {
		c.bad()
		return
	}
	r := rec{}
	r.parent, r.hasParent = parentOf(e.ParentID)
	if e.Type == "message" && e.Message != nil {
		switch e.Message.Role {
		case "user":
			r.user = true
			c.wholeUser++
		case "assistant":
			r.assistant = true
			c.wholeAsst++
			if e.Message.StopReason != "error" && nonEmptyArray(e.Message.Content) {
				r.substantive = true
				c.wholeSubst = true
			}
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && t.After(c.lastActive) {
		c.lastActive = t
	}
	c.byID[*e.ID] = r
	c.leaf, c.haveLeaf = *e.ID, true
}

func (c *scan) bad() {
	c.badLines++
	if c.firstBad == 0 {
		c.firstBad = c.lineNo
	}
}

// header validates line 1 through the shared reader.
func (c *scan) header(terminated bool) {
	if c.over || len(c.buf) > maxHeaderLine {
		c.fatal = fmt.Errorf("header line longer than %d bytes", maxHeaderLine)
		return
	}
	line := c.buf
	if terminated {
		line = append(append([]byte(nil), c.buf...), '\n')
	}
	h, err := readHeader(bytes.NewReader(line))
	switch {
	case errors.Is(err, errSilentSkip):
		c.fatal = errNoHeader
	case err != nil:
		c.fatal = err
	case h.ID != c.wantID:
		c.fatal = fmt.Errorf("header id %q differs from discovered id %q (file replaced)", h.ID, c.wantID)
	default:
		c.hdr, c.hdrOK = h, true
		c.lastActive = h.Timestamp
	}
}

type scanResult struct {
	lastActive         time.Time
	user, assistant    int
	substantive        bool
	badLines, firstBad int
	brokenAt           string
}

// result walks the leaf path (contract section 5). A missing parent or
// a cycle falls back to whole-file counts.
func (c *scan) result() scanResult {
	r := scanResult{lastActive: c.lastActive, badLines: c.badLines, firstBad: c.firstBad}
	if !c.haveLeaf {
		return r
	}
	path, broken := walkLeaf(c.leaf, func(id string) (string, bool, bool) {
		rc, ok := c.byID[id]
		return rc.parent, rc.hasParent, ok
	})
	if broken != "" {
		r.brokenAt = broken
		r.user, r.assistant, r.substantive = c.wholeUser, c.wholeAsst, c.wholeSubst
		return r
	}
	for _, id := range path {
		rc := c.byID[id]
		if rc.user {
			r.user++
		}
		if rc.assistant {
			r.assistant++
		}
		if rc.substantive {
			r.substantive = true
		}
	}
	return r
}

// walkLeaf follows parentId from leaf to a root (contract section 5),
// shared by capture's counts and the offline readers so they can never
// disagree about which entries are the conversation. lookup reports an
// id's parent, whether it has one, and whether the id exists. The path
// is returned leaf first. A missing parent or a cycle returns the
// offending id as broken (and the walked prefix is not meaningful).
func walkLeaf(leaf string, lookup func(id string) (parent string, hasParent, ok bool)) (path []string, broken string) {
	seen := map[string]bool{}
	for cur := leaf; ; {
		parent, hasParent, ok := lookup(cur)
		if !ok || seen[cur] {
			return nil, cur
		}
		seen[cur] = true
		path = append(path, cur)
		if !hasParent {
			return path, ""
		}
		cur = parent
	}
}

func nonEmptyArray(raw json.RawMessage) bool {
	var a []json.RawMessage
	return json.Unmarshal(raw, &a) == nil && len(a) > 0
}

// parentOf decodes parentId: a non-empty string is a parent; absent,
// null, empty or any other type means a root.
func parentOf(raw json.RawMessage) (string, bool) {
	var p string
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p == "" {
		return "", false
	}
	return p, true
}
