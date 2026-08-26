package upload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// codecovCLIBaseURL is the root of the Codecov CLI download site. It is a
// variable so tests can point the download at a local server.
var codecovCLIBaseURL = "https://cli.codecov.io/latest"

// codecovDownloadTimeout bounds the CLI download. http.DefaultClient, used
// before, has no timeout at all, so a stalled connection hung the upload.
const codecovDownloadTimeout = 5 * time.Minute

// CodecovUploader handles uploading coverage to Codecov
type CodecovUploader struct {
	token         string
	codecovPath   string
	tempDir       string
	downloadedCLI bool
}

// CodecovOptions contains options for uploading to Codecov
type CodecovOptions struct {
	Token        string
	CommitSHA    string
	Branch       string
	PullRequest  string // PR number (e.g., "123") - helps Codecov associate coverage with PRs
	RepoRoot     string
	RepoSlug     string // Repository slug (e.g., "owner/repo")
	GitService   string // Git service: github, gitlab, bitbucket, etc.
	CoverageFile string
	Flags        []string
	Name         string
	Verbose      bool
}

// NewCodecovUploader creates a new Codecov uploader
func NewCodecovUploader(token string) (*CodecovUploader, error) {
	if token == "" {
		return nil, fmt.Errorf("codecov token is required")
	}

	// Check if codecov CLI is already available
	codecovPath, err := exec.LookPath("codecov")
	if codecovPath == "" || err != nil {
		// Not found or problematic path, we'll download it
		return &CodecovUploader{ //nolint:nilerr // intentionally ignore LookPath error, fall through to download
			token:         token,
			downloadedCLI: false,
		}, nil
	}

	return &CodecovUploader{
		token:       token,
		codecovPath: codecovPath,
	}, nil
}

// ensureCodecovCLI ensures the codecov CLI is available
func (u *CodecovUploader) ensureCodecovCLI(ctx context.Context) error {
	if u.codecovPath != "" {
		return nil // Already have it
	}

	fmt.Println("📥 Downloading Codecov CLI...")

	// Determine download URL based on OS
	downloadURL, err := codecovDownloadURL(runtime.GOOS)
	if err != nil {
		return err
	}

	codecovPath, tempDir, err := downloadCodecovCLI(ctx, downloadURL)
	if err != nil {
		return err
	}

	u.codecovPath = codecovPath
	u.tempDir = tempDir
	u.downloadedCLI = true

	fmt.Printf("Codecov CLI downloaded to: %s\n", codecovPath)
	return nil
}

// codecovDownloadURL returns the CLI download URL for the given GOOS.
func codecovDownloadURL(goos string) (string, error) {
	switch goos {
	case "linux":
		return codecovCLIBaseURL + "/linux/codecov", nil
	case "darwin":
		return codecovCLIBaseURL + "/macos/codecov", nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", goos)
	}
}

// downloadCodecovCLI fetches the CLI into a directory created for this process
// alone and returns the binary path plus the directory to remove afterwards.
//
// The directory matters. Writing to a fixed shared path such as $TMPDIR/codecov
// lets a second coverport run truncate the binary while the first is executing
// it, and lets any other user on the host pre-create or replace that path
// between the write and the exec, so coverport would run code it never
// downloaded.
func downloadCodecovCLI(ctx context.Context, downloadURL string) (binPath, tempDir string, err error) {
	tempDir, err = os.MkdirTemp("", "coverport-codecov-")
	if err != nil {
		return "", "", fmt.Errorf("failed to create download directory: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()

	binPath = filepath.Join(tempDir, "codecov")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create download request: %w", err)
	}

	client := &http.Client{Timeout: codecovDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to download codecov CLI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to download codecov CLI: status %d", resp.StatusCode)
	}

	// O_EXCL: the directory is brand new, so an entry already sitting here was
	// put there by something other than this download.
	out, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		return "", "", fmt.Errorf("failed to create codecov file: %w", err)
	}

	if _, copyErr := io.Copy(out, resp.Body); copyErr != nil {
		out.Close()
		return "", "", fmt.Errorf("failed to save codecov CLI: %w", copyErr)
	}

	// Closed explicitly: a deferred close discards the flush error and would
	// leave a truncated binary that is then executed.
	if err = out.Close(); err != nil {
		return "", "", fmt.Errorf("failed to write codecov CLI: %w", err)
	}

	return binPath, tempDir, nil
}

// Upload uploads coverage data to Codecov
func (u *CodecovUploader) Upload(ctx context.Context, opts CodecovOptions) error {
	// Ensure codecov CLI is available
	if err := u.ensureCodecovCLI(ctx); err != nil {
		return err
	}

	fmt.Println("Uploading coverage to Codecov...")
	fmt.Printf("   File: %s\n", opts.CoverageFile)
	fmt.Printf("   Commit: %s\n", opts.CommitSHA)
	if opts.Branch != "" {
		fmt.Printf("   Branch: %s\n", opts.Branch)
	}

	// Convert coverage file to absolute path (needed when running from repo root)
	absCoverageFile, err := filepath.Abs(opts.CoverageFile)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for coverage file: %w", err)
	}

	// Verify coverage file exists
	if _, err := os.Stat(absCoverageFile); err != nil {
		return fmt.Errorf("coverage file not found: %w", err)
	}

	// Build codecov command
	args := []string{
		"upload-coverage",
		"-t", opts.Token,
		"-f", absCoverageFile,
		"--sha", opts.CommitSHA,
		"--disable-search", // Don't search for other coverage files
	}

	// Add repository slug if provided
	if opts.RepoSlug != "" {
		args = append(args, "--slug", opts.RepoSlug)
	}

	// Add git service if provided
	if opts.GitService != "" {
		args = append(args, "--git-service", opts.GitService)
	}

	// Add optional parameters
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	}

	if opts.PullRequest != "" {
		args = append(args, "--pr", opts.PullRequest)
	}

	for _, flag := range opts.Flags {
		args = append(args, "--flag", flag)
	}

	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}

	// Note: Codecov CLI doesn't support --verbose flag
	// if opts.Verbose {
	// 	args = append(args, "--verbose")
	// }

	// Execute upload
	cmd := exec.CommandContext(ctx, u.codecovPath, args...)
	cmd.Dir = opts.RepoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codecov upload failed: %w", err)
	}

	fmt.Println("Coverage uploaded to Codecov successfully!")
	return nil
}

// Cleanup removes the downloaded codecov CLI if it was downloaded
func (u *CodecovUploader) Cleanup() {
	if u.downloadedCLI && u.tempDir != "" {
		os.RemoveAll(u.tempDir)
	}
}
