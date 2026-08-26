package upload

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewCodecovUploader_EmptyToken(t *testing.T) {
	_, err := NewCodecovUploader("")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestNewCodecovUploader_CodecovNotInPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	uploader, err := NewCodecovUploader("test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uploader.codecovPath != "" {
		t.Errorf("expected empty codecovPath, got %q", uploader.codecovPath)
	}
	if uploader.downloadedCLI != false {
		t.Error("expected downloadedCLI=false")
	}
}

func TestNewCodecovUploader_CodecovInPath(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "codecov")

	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to create fake codecov: %v", err)
	}

	t.Setenv("PATH", tmpDir)

	// Verify our fake binary is discoverable
	path, err := exec.LookPath("codecov")
	if err != nil {
		t.Skipf("LookPath can't find fake binary (platform issue): %v", err)
	}

	uploader, err := NewCodecovUploader("test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uploader.codecovPath != path {
		t.Errorf("expected codecovPath=%q, got %q", path, uploader.codecovPath)
	}
}

func TestCodecovDownloadURL(t *testing.T) {
	tests := []struct {
		goos    string
		want    string
		wantErr bool
	}{
		{goos: "linux", want: "https://cli.codecov.io/latest/linux/codecov"},
		{goos: "darwin", want: "https://cli.codecov.io/latest/macos/codecov"},
		{goos: "windows", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			got, err := codecovDownloadURL(tt.goos)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tt.goos, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestDownloadCodecovCLI_UsesPrivateDirectory(t *testing.T) {
	const payload = "#!/bin/sh\necho fake codecov\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	binPath, tempDir, err := downloadCodecovCLI(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if filepath.Dir(binPath) != tempDir {
		t.Errorf("expected the binary inside %q, got %q", tempDir, binPath)
	}
	if tempDir == os.TempDir() || filepath.Dir(binPath) == os.TempDir() {
		t.Error("the CLI must not be written straight into the shared temp directory")
	}

	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("failed to read downloaded CLI: %v", err)
	}
	if string(got) != payload {
		t.Errorf("expected downloaded content %q, got %q", payload, string(got))
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(binPath)
		if err != nil {
			t.Fatalf("failed to stat downloaded CLI: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("expected mode 0700 so no other user can replace the binary, got %#o", perm)
		}
	}
}

func TestDownloadCodecovCLI_ConcurrentRunsDoNotShareAPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "payload")
	}))
	defer srv.Close()

	first, firstDir, err := downloadCodecovCLI(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.RemoveAll(firstDir)

	second, secondDir, err := downloadCodecovCLI(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.RemoveAll(secondDir)

	if first == second {
		t.Errorf("two runs downloaded to the same path %q; one can truncate the other's binary", first)
	}
}

func TestDownloadCodecovCLI_RemovesDirectoryOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	binPath, tempDir, err := downloadCodecovCLI(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if binPath != "" || tempDir != "" {
		t.Errorf("expected empty paths on failure, got %q and %q", binPath, tempDir)
	}
}

func TestCleanup_RemovesDownloadDirectory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "payload")
	}))
	defer srv.Close()

	binPath, tempDir, err := downloadCodecovCLI(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u := &CodecovUploader{codecovPath: binPath, tempDir: tempDir, downloadedCLI: true}
	u.Cleanup()

	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		os.RemoveAll(tempDir)
		t.Errorf("expected %q to be removed, stat error was %v", tempDir, err)
	}
}
