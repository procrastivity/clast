// Package iostreams carries the stdout/stderr writer pair every verb
// constructor receives, so the stdout-carries-exactly-one-thing discipline is
// structural (threaded in) rather than a matter of remembering not to call
// fmt.Println (CONTRACT.md C2.1).
package iostreams

import (
	"io"
	"os"
)

// Streams is the reader/writer set threaded into every verb constructor. In
// is nil for a verb that never reads input — only curate (SURFACE V14) reads
// it, for its stdin-or---file document source.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// System returns the real process streams.
func System() *Streams {
	return &Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}
