package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/heydon/dconsole/internal/alias"
)

// ironstar implements Provider on top of the `iron` CLI shipped by
// Ironstar (github.com/ironstar-io/ironstar-cli). It does not import the
// CLI as a Go module — we shell out so the user-installed `iron` carries
// the auth context they've already set up.
type ironstar struct {
	cfg *alias.IronstarProvider
	// ironBin is the path to the iron CLI; overridable for tests.
	ironBin string
}

func init() {
	Register("ironstar", func(a *alias.Alias) (Provider, error) {
		if a.Provider.Ironstar == nil {
			return nil, fmt.Errorf("provider type ironstar requires an ironstar: block")
		}
		if a.Provider.Ironstar.Subscription == "" {
			return nil, fmt.Errorf("ironstar provider requires subscription")
		}
		if a.Provider.Ironstar.Environment == "" {
			return nil, fmt.Errorf("ironstar provider requires environment")
		}
		return &ironstar{cfg: a.Provider.Ironstar, ironBin: "iron"}, nil
	})
}

func (i *ironstar) Name() string { return "ironstar" }

// Login runs `iron login` interactively. The iron CLI manages its own
// credential storage (username + password + MFA), so dconsole just hands
// the user's TTY to it. Captured by tests via the loginIO override below.
func (i *ironstar) Login(ctx context.Context, a *alias.Alias) error {
	cmd := exec.CommandContext(ctx, i.ironBin, "login")
	io := loginIOFor(a)
	cmd.Stdin = io.in
	cmd.Stdout = io.out
	cmd.Stderr = io.err
	return cmd.Run()
}

// loginIO holds the I/O streams for an interactive login. Defaults to
// the dconsole process's own streams; tests can override via
// SetLoginIOForTest.
type loginIO struct {
	in  *os.File
	out *os.File
	err *os.File
}

var loginIOOverride *loginIO

func loginIOFor(*alias.Alias) loginIO {
	if loginIOOverride != nil {
		return *loginIOOverride
	}
	return loginIO{in: os.Stdin, out: os.Stdout, err: os.Stderr}
}

// SetLoginIOForTest overrides the streams used by Login. Test-only.
func SetLoginIOForTest(in, out, err *os.File) func() {
	prev := loginIOOverride
	loginIOOverride = &loginIO{in: in, out: out, err: err}
	return func() { loginIOOverride = prev }
}

// DumpFor downloads the latest database backup for the alias's
// subscription+environment via `iron backup download --latest`. The
// returned dump path is the .sql.gz file iron wrote into our tempdir.
func (i *ironstar) DumpFor(ctx context.Context, a *alias.Alias) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "dconsole-ironstar-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	args := []string{
		"backup", "download",
		"--subscription", i.cfg.Subscription,
		"--environment", i.cfg.Environment,
		"--latest",
		"--component", "database:all",
		"--save-path", tmpDir,
		"--yes",
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, i.ironBin, args...)
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr // iron prints status to stdout; capture for error context
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("iron backup download: %w\n%s", err, stderr.String())
	}

	dump, err := findSQLDump(tmpDir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("iron downloaded no database dump into %s: %w", tmpDir, err)
	}
	return dump, cleanup, nil
}

// LoadFor on Ironstar isn't implemented — pushing dumps *into* Ironstar
// happens over their own deployment flow, not the CLI. Caller falls back
// to the generic transport-based import.
func (i *ironstar) LoadFor(ctx context.Context, a *alias.Alias, dumpPath string) error {
	return ErrNotSupported
}

// FilesDownload uses `iron backup download --component path:all` to fetch
// the files component(s) of the latest backup. The downloaded tarball is
// extracted into targetDir.
func (i *ironstar) FilesDownload(ctx context.Context, a *alias.Alias, targetDir string) error {
	tmpDir, err := os.MkdirTemp("", "dconsole-ironstar-files-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	args := []string{
		"backup", "download",
		"--subscription", i.cfg.Subscription,
		"--environment", i.cfg.Environment,
		"--latest",
		"--component", "path:all",
		"--save-path", tmpDir,
		"--yes",
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, i.ironBin, args...)
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("iron backup download (files): %w\n%s", err, stderr.String())
	}

	tarball, err := findTarball(tmpDir)
	if err != nil {
		return fmt.Errorf("iron downloaded no files tarball into %s: %w", tmpDir, err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	if err := extractTarball(ctx, tarball, targetDir); err != nil {
		return fmt.Errorf("extract %s: %w", tarball, err)
	}
	return nil
}

func findSQLDump(dir string) (string, error) {
	var found string
	walkErr := filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".gz" && len(path) > 7 && path[len(path)-7:] == ".sql.gz" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if found == "" {
		return "", fmt.Errorf("no *.sql.gz file under %s", dir)
	}
	return found, nil
}

func findTarball(dir string) (string, error) {
	var found string
	walkErr := filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if len(path) > 7 && path[len(path)-7:] == ".tar.gz" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if found == "" {
		return "", fmt.Errorf("no *.tar.gz file under %s", dir)
	}
	return found, nil
}

func extractTarball(ctx context.Context, tarball, targetDir string) error {
	cmd := exec.CommandContext(ctx, "tar", "xf", tarball, "-C", targetDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar: %w\n%s", err, stderr.String())
	}
	return nil
}
