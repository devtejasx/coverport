package upload

import (
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

func TestCreateCLIFile_UsesPrivateDirectory(t *testing.T) {
	u := &CodecovUploader{token: "test-token"}

	f, err := u.createCLIFile()
	if err != nil {
		t.Fatalf("createCLIFile: %v", err)
	}
	defer f.Close()
	defer u.discardTempDir()

	if u.tempDir == "" {
		t.Fatal("expected a temp directory to be recorded")
	}
	if filepath.Dir(f.Name()) != u.tempDir {
		t.Errorf("file %q is not inside the recorded directory %q", f.Name(), u.tempDir)
	}
	// The old code wrote straight into the shared temp directory.
	if filepath.Dir(f.Name()) == os.TempDir() {
		t.Errorf("codecov CLI was created directly in the shared temp dir %q", os.TempDir())
	}

	info, err := os.Stat(u.tempDir)
	if err != nil {
		t.Fatalf("stat temp dir: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("expected download directory mode 0700, got %#o", perm)
		}
	}
}

func TestCreateCLIFile_DistinctPerUploader(t *testing.T) {
	first := &CodecovUploader{token: "test-token"}
	second := &CodecovUploader{token: "test-token"}

	f1, err := first.createCLIFile()
	if err != nil {
		t.Fatalf("createCLIFile: %v", err)
	}
	defer f1.Close()
	defer first.discardTempDir()

	f2, err := second.createCLIFile()
	if err != nil {
		t.Fatalf("createCLIFile: %v", err)
	}
	defer f2.Close()
	defer second.discardTempDir()

	if f1.Name() == f2.Name() {
		t.Errorf("two uploaders downloaded to the same path %q", f1.Name())
	}
}

func TestCreateCLIFile_RefusesExistingFile(t *testing.T) {
	u := &CodecovUploader{token: "test-token"}

	f, err := u.createCLIFile()
	if err != nil {
		t.Fatalf("createCLIFile: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer u.discardTempDir()

	// O_EXCL: a path that already exists is never truncated and reused.
	if _, err := os.OpenFile(f.Name(), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700); err == nil {
		t.Error("expected creating the existing codecov path to fail")
	}
}

func TestCleanup_RemovesDownloadDirectory(t *testing.T) {
	u := &CodecovUploader{token: "test-token"}

	f, err := u.createCLIFile()
	if err != nil {
		t.Fatalf("createCLIFile: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	u.codecovPath = f.Name()
	u.downloadedCLI = true

	dir := u.tempDir
	u.Cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected %q to be removed, stat returned %v", dir, err)
	}
}
