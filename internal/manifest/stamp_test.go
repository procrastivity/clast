package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadStamp_Unparseable asserts that a stamp file present on disk but
// not valid JSON is reported through the ErrStampUnparseable sentinel
// (§1.2), distinct from "no stamp" (ok=false, err=nil) and from any other
// read error, so harness.Status can tell the three apart with errors.Is
// rather than matching error text.
func TestReadStamp_Unparseable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StampFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	_, ok, err := ReadStamp(dir)
	if ok {
		t.Errorf("ReadStamp: ok = true, want false for an unparseable stamp")
	}
	if !errors.Is(err, ErrStampUnparseable) {
		t.Errorf("ReadStamp: err = %v, want errors.Is(err, ErrStampUnparseable)", err)
	}
}

// TestReadStamp_NotExist asserts that a missing stamp file is not an
// error: ok is false and err is nil, so a caller can tell "no stamp" apart
// from "a stamp that could not be read" (§1.2).
func TestReadStamp_NotExist(t *testing.T) {
	dir := t.TempDir()

	stamp, ok, err := ReadStamp(dir)
	if err != nil {
		t.Fatalf("ReadStamp: unexpected error: %v", err)
	}
	if ok {
		t.Errorf("ReadStamp: ok = true, want false when no stamp is present")
	}
	if stamp.ToolVersion != "" || stamp.SchemaVersion != 0 || len(stamp.Files) != 0 {
		t.Errorf("ReadStamp: stamp = %+v, want zero value", stamp)
	}
}
