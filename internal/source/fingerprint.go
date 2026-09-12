package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// FingerprintFile computes a transcript file's line count and sha256 in
// one streaming pass — the Lines/SHA256 half of journal's
// TranscriptFingerprint (M7's growth comparison; Format is the caller's
// to name, M13). It counts newline bytes and treats a non-empty final
// line without a trailing newline as a line, so an oversized or
// half-flushed last line never needs a line buffer at all (the 16 MiB
// lines duo's claude runtime met are just bytes here).
func FingerprintFile(path string) (lines int, sum string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", fmt.Errorf("source: opening %s: %w", path, err)
	}
	// Deliberate discard: the file is only read, and the hash below has
	// already consumed every byte by the time close matters.
	defer func() { _ = f.Close() }()

	h := sha256.New()
	buf := make([]byte, 256*1024)
	lastByte := byte('\n')
	empty := true
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			empty = false
			h.Write(buf[:n])
			for _, b := range buf[:n] {
				if b == '\n' {
					lines++
				}
			}
			lastByte = buf[n-1]
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, "", fmt.Errorf("source: reading %s: %w", path, readErr)
		}
	}
	if !empty && lastByte != '\n' {
		lines++
	}
	return lines, hex.EncodeToString(h.Sum(nil)), nil
}
