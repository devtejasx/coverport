package upload

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCodecovEnv makes the test binary act as the codecov CLI: it records its
// arguments and CODECOV_TOKEN to the file named by the variable and exits.
const fakeCodecovEnv = "COVERPORT_FAKE_CODECOV_RECORD"

type fakeCodecovCall struct {
	Args  []string `json:"args"`
	Token string   `json:"token"`
}

func TestMain(m *testing.M) {
	if record := os.Getenv(fakeCodecovEnv); record != "" {
		data, err := json.Marshal(fakeCodecovCall{Args: os.Args[1:], Token: os.Getenv("CODECOV_TOKEN")})
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(record, data, 0o600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeUpload(t *testing.T, uploaderToken, optsToken string) fakeCodecovCall {
	t.Helper()

	dir := t.TempDir()
	coverageFile := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(coverageFile, []byte("mode: set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "call.json")
	t.Setenv(fakeCodecovEnv, record)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	uploader := &CodecovUploader{token: uploaderToken, codecovPath: self}
	err = uploader.Upload(context.Background(), CodecovOptions{
		Token:        optsToken,
		CommitSHA:    "0123456789abcdef",
		RepoRoot:     dir,
		CoverageFile: coverageFile,
	})
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("fake codecov was not run: %v", err)
	}
	var call fakeCodecovCall
	if err := json.Unmarshal(data, &call); err != nil {
		t.Fatal(err)
	}
	return call
}

func TestUpload_TokenNotOnCommandLine(t *testing.T) {
	const secret = "s3cr3t-upload-token"
	call := runFakeUpload(t, "uploader-token", secret)

	for _, arg := range call.Args {
		if strings.Contains(arg, secret) {
			t.Errorf("token leaked into the command line: %q", call.Args)
		}
		if arg == "-t" || arg == "--token" {
			t.Errorf("unexpected token flag in %q", call.Args)
		}
	}
	if call.Token != secret {
		t.Errorf("CODECOV_TOKEN = %q, want %q", call.Token, secret)
	}
	if len(call.Args) == 0 || call.Args[0] != "upload-coverage" {
		t.Errorf("unexpected arguments %q", call.Args)
	}
}

func TestUpload_FallsBackToUploaderToken(t *testing.T) {
	call := runFakeUpload(t, "uploader-token", "")

	if call.Token != "uploader-token" {
		t.Errorf("CODECOV_TOKEN = %q, want the uploader's token", call.Token)
	}
}
