package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneOptionsValidate(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		name        string
		opts        CloneOptions
		wantErrPart string
	}{
		{
			name: "well formed options",
			opts: CloneOptions{RepoURL: "https://github.com/konflux-ci/coverport", CommitSHA: sha, Branch: "main"},
		},
		{
			name: "commit is optional",
			opts: CloneOptions{RepoURL: "https://github.com/konflux-ci/coverport"},
		},
		{
			name: "abbreviated commit is accepted",
			opts: CloneOptions{RepoURL: "https://example.com/repo.git", CommitSHA: "c914ae0"},
		},
		{
			name:        "empty repository URL",
			opts:        CloneOptions{},
			wantErrPart: "empty",
		},
		{
			name:        "repository URL parsed as a git option",
			opts:        CloneOptions{RepoURL: "--upload-pack=touch /tmp/pwned"},
			wantErrPart: "leading dash",
		},
		{
			name:        "commit parsed as a git option",
			opts:        CloneOptions{RepoURL: "https://example.com/repo.git", CommitSHA: "--upload-pack=touch /tmp/pwned"},
			wantErrPart: "hexadecimal",
		},
		{
			name:        "commit carrying a shell fragment",
			opts:        CloneOptions{RepoURL: "https://example.com/repo.git", CommitSHA: sha + "; touch /tmp/pwned"},
			wantErrPart: "hexadecimal",
		},
		{
			name:        "commit that is a ref name, not an object name",
			opts:        CloneOptions{RepoURL: "https://example.com/repo.git", CommitSHA: "refs/heads/main"},
			wantErrPart: "hexadecimal",
		},
		{
			name:        "branch parsed as a git option",
			opts:        CloneOptions{RepoURL: "https://example.com/repo.git", Branch: "--upload-pack=touch /tmp/pwned"},
			wantErrPart: "leading dash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()

			if tt.wantErrPart == "" {
				if err != nil {
					t.Fatalf("validate() failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() accepted %+v, want an error about %q", tt.opts, tt.wantErrPart)
			}
			if !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Errorf("validate() error = %q, want it to mention %q", err, tt.wantErrPart)
			}
		})
	}
}

// newLocalRepo builds a throwaway repository and returns its path and HEAD sha.
func newLocalRepo(t *testing.T) (string, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=coverport", "GIT_AUTHOR_EMAIL=coverport@example.com",
			"GIT_COMMITTER_NAME=coverport", "GIT_COMMITTER_EMAIL=coverport@example.com",
			"GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	run("add", "file.txt")
	run("commit", "-q", "--no-gpg-sign", "-m", "initial")

	return dir, run("rev-parse", "HEAD")
}

func TestCloneRejectsInjectedArguments(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repo, sha := newLocalRepo(t)
	cloner, err := NewRepositoryCloner()
	if err != nil {
		t.Fatalf("NewRepositoryCloner() failed: %v", err)
	}

	t.Run("a legitimate clone still works", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "checkout")
		if err := cloner.Clone(context.Background(), CloneOptions{
			RepoURL:   repo,
			CommitSHA: sha,
			TargetDir: target,
		}); err != nil {
			t.Fatalf("Clone() failed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(target, "file.txt")); err != nil {
			t.Errorf("cloned tree is missing file.txt: %v", err)
		}
	})

	// --upload-pack is a git option, not a shell one: git runs its value as a
	// command when it talks to a local or ssh remote, so these inputs have to
	// be refused before any git process starts.
	injected := []struct {
		name string
		opts CloneOptions
	}{
		{
			name: "commit that is really a git option",
			opts: CloneOptions{RepoURL: repo, CommitSHA: "--upload-pack=id; git-upload-pack", Depth: 1},
		},
		{
			name: "repository URL that is really a git option",
			opts: CloneOptions{RepoURL: "--upload-pack=id; git-upload-pack"},
		},
		{
			name: "branch that is really a git option",
			opts: CloneOptions{RepoURL: repo, Branch: "--upload-pack=id; git-upload-pack"},
		},
	}

	for _, tt := range injected {
		t.Run(tt.name+" is refused", func(t *testing.T) {
			opts := tt.opts
			opts.TargetDir = filepath.Join(t.TempDir(), "checkout")

			err := cloner.Clone(context.Background(), opts)
			if err == nil {
				t.Fatalf("Clone() accepted %+v", opts)
			}
			if !strings.Contains(err.Error(), "invalid clone options") {
				t.Errorf("Clone() error = %q, want it rejected by validation", err)
			}
			// Nothing should have run: creating the target directory is
			// Clone's first side effect after validation.
			if _, statErr := os.Stat(opts.TargetDir); !os.IsNotExist(statErr) {
				t.Errorf("Clone() started work on a rejected request: %v", statErr)
			}
		})
	}
}
