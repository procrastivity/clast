package scripts

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseInstaller(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		goarch    string
		version   string
		asset     string
		badSum    bool
		preexist  bool
		wantError bool
	}{
		{name: "latest linux amd64", goos: "Linux", goarch: "x86_64", asset: "clast-linux-amd64"},
		{name: "pinned darwin arm64", goos: "Darwin", goarch: "arm64", version: "v1.2.3", asset: "clast-darwin-arm64"},
		{name: "checksum mismatch leaves destination absent", goos: "Linux", goarch: "x86_64", asset: "clast-linux-amd64", badSum: true, wantError: true},
		{name: "checksum mismatch preserves destination", goos: "Linux", goarch: "x86_64", asset: "clast-linux-amd64", badSum: true, preexist: true, wantError: true},
		{name: "unsupported platform stops before network", goos: "FreeBSD", goarch: "amd64", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			binDir := filepath.Join(root, "bin")
			fixtureDir := filepath.Join(root, "fixtures")
			tmpDir := filepath.Join(root, "tmp")
			installDir := filepath.Join(root, "install")
			for _, dir := range []string{binDir, fixtureDir, tmpDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			binary := []byte("fixture executable bytes: " + tt.asset)
			if tt.asset != "" {
				if err := os.WriteFile(filepath.Join(fixtureDir, tt.asset), binary, 0o644); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(binary)
				if tt.badSum {
					sum[0] ^= 0xff
				}
				checksums := fmt.Sprintf("%x  %s\n", sum, tt.asset)
				if err := os.WriteFile(filepath.Join(fixtureDir, "SHA256SUMS"), []byte(checksums), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			logPath := filepath.Join(root, "curl.log")
			writeExecutable(t, filepath.Join(binDir, "curl"), `#!/bin/sh
set -eu
url=$2
out=$4
printf '%s\n' "$url" >> "$CURL_LOG"
name=${url##*/}
cp "$FIXTURE_DIR/$name" "$out"
`)
			writeExecutable(t, filepath.Join(binDir, "uname"), `#!/bin/sh
case "${1:-}" in
-s) printf '%s\n' "$FAKE_UNAME_S" ;;
-m) printf '%s\n' "$FAKE_UNAME_M" ;;
*) exit 2 ;;
esac
`)

			if tt.preexist {
				if err := os.MkdirAll(installDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(installDir, "clast"), []byte("old binary"), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			_, file, _, _ := runtime.Caller(0)
			installer := filepath.Join(filepath.Dir(file), "install.sh")
			cmd := exec.Command("/bin/sh", installer)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+":"+os.Getenv("PATH"),
				"CURL_LOG="+logPath,
				"FIXTURE_DIR="+fixtureDir,
				"TMPDIR="+tmpDir,
				"CLAST_INSTALL_DIR="+installDir,
				"CLAST_BASE_URL=https://github.com/example/clast/",
				"CLAST_VERSION="+tt.version,
				"FAKE_UNAME_S="+tt.goos,
				"FAKE_UNAME_M="+tt.goarch,
			)
			output, err := cmd.CombinedOutput()
			if tt.wantError && err == nil {
				t.Fatalf("expected failure, got success: %s", output)
			}
			if !tt.wantError && err != nil {
				t.Fatalf("installer failed: %v\n%s", err, output)
			}

			base := "https://github.com/example/clast"
			var wantURLs []string
			if tt.asset != "" {
				prefix := base + "/releases/latest/download"
				if tt.version != "" {
					prefix = base + "/releases/download/" + tt.version
				}
				wantURLs = []string{prefix + "/" + tt.asset, prefix + "/SHA256SUMS"}
			}
			gotLog, err := os.ReadFile(logPath)
			if err != nil && len(wantURLs) > 0 {
				t.Fatal(err)
			}
			gotURLs := strings.Fields(string(gotLog))
			if strings.Join(gotURLs, "\n") != strings.Join(wantURLs, "\n") {
				t.Fatalf("curl URLs = %v, want ordered %v", gotURLs, wantURLs)
			}

			destination := filepath.Join(installDir, "clast")
			installed, readErr := os.ReadFile(destination)
			if tt.badSum {
				if tt.preexist {
					if readErr != nil || string(installed) != "old binary" {
						t.Fatalf("checksum failure changed existing destination: bytes=%q err=%v", installed, readErr)
					}
				} else if !os.IsNotExist(readErr) {
					t.Fatalf("checksum failure created destination: bytes=%q err=%v", installed, readErr)
				}
			} else if tt.asset != "" {
				if readErr != nil || string(installed) != string(binary) {
					t.Fatalf("installed bytes=%q err=%v, want %q", installed, readErr, binary)
				}
				info, err := os.Stat(destination)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm()&0o111 == 0 {
					t.Fatalf("installed binary is not executable: mode %v", info.Mode())
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("unsupported platform created destination; read err=%v", readErr)
			}

			for _, dir := range []string{tmpDir, installDir} {
				entries, err := os.ReadDir(dir)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "clast-install.") || strings.HasPrefix(entry.Name(), ".clast.") {
						t.Errorf("temporary installer file remains: %s/%s", dir, entry.Name())
					}
				}
			}
		})
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
