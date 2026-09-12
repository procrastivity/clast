package source

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFingerprintFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
		lines   int
	}{
		{"empty", "", 0},
		{"one line terminated", "{\"a\":1}\n", 1},
		{"one line unterminated", "{\"a\":1}", 1},
		{"three lines last unterminated", "{}\n{}\n{}", 3},
		{"oversized line", strings.Repeat("x", 20<<20) + "\n{}\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixture(t, tc.content)
			lines, sum, err := FingerprintFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if lines != tc.lines {
				t.Errorf("lines = %d, want %d", lines, tc.lines)
			}
			raw := sha256.Sum256([]byte(tc.content))
			if want := hex.EncodeToString(raw[:]); sum != want {
				t.Errorf("sha256 = %s, want %s", sum, want)
			}
		})
	}
}

func TestFingerprintFileMissing(t *testing.T) {
	if _, _, err := FingerprintFile(filepath.Join(t.TempDir(), "absent.jsonl")); err == nil {
		t.Fatal("want error for a missing file")
	}
}
