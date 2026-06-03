package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/dlog"
	"github.com/dconsole/dconsole/internal/run"
)

// ddevTransport runs commands via `ddev exec` against a local project.
// ddev determines the target project from the working directory (it
// walks up looking for .ddev/config.yaml), so build() always sets
// Cmd.Dir to the resolved approot.
type ddevTransport struct {
	cfg     *alias.DDEVTransport
	approot string // dir containing .ddev/config.yaml
}

func init() {
	Register("ddev", Registration{
		RequiredCLI: "ddev",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				DDEV alias.DDEVTransport `yaml:"ddev"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type ddev: %w", err)
			}
			if w.DDEV.Project == "" {
				return nil, fmt.Errorf("ddev transport requires project (the project name passed to `ddev describe`)")
			}
			approot, err := findDDEVApproot(a.Root)
			if err != nil {
				return nil, fmt.Errorf("ddev transport: %w", err)
			}
			cfg := w.DDEV
			return &ddevTransport{cfg: &cfg, approot: approot}, nil
		},
	})
}

func (d *ddevTransport) Name() string     { return "ddev" }
func (d *ddevTransport) Available() error { return CLIAvailable("ddev") }

func (d *ddevTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := d.build(ctx, remoteCmd)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (d *ddevTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := d.build(ctx, remoteCmd)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// build constructs `ddev exec [-s service] -- <cmd>`. ddev resolves the
// target project from the working directory, so Cmd.Dir is set to the
// alias's resolved approot.
func (d *ddevTransport) build(ctx context.Context, remoteCmd []string) *exec.Cmd {
	args := []string{"exec"}
	if d.cfg.Service != "" {
		args = append(args, "-s", d.cfg.Service)
	}
	args = append(args, "--")
	args = append(args, remoteCmd...)
	cmd := exec.CommandContext(ctx, "ddev", args...)
	cmd.Dir = d.approot
	return cmd
}

func (d *ddevTransport) Preview(remoteCmd []string) []string {
	return d.build(context.Background(), remoteCmd).Args
}

// Wrap returns the LOCAL argv that ddev will spawn to forward `inner`
// into the project's web container. Required by pkg/handler.Handler.
func (d *ddevTransport) Wrap(inner []string) []string { return d.Preview(inner) }

// ShellPreview returns the argv ddev.Shell() would spawn — `ddev ssh`
// rather than the generic `ddev exec -- bash -l` Wrap would produce.
// Used by `dconsole inspect @alias sh`.
func (d *ddevTransport) ShellPreview(workDir string) []string {
	args := []string{"ddev", "ssh"}
	if d.cfg.Service != "" {
		args = append(args, "-s", d.cfg.Service)
	}
	if workDir != "" {
		args = append(args, "-d", workDir)
	}
	return args
}

// ImportDB satisfies pkg/transport.DBImporter so sql:sync uses
// `ddev import-db --file=<dump.sql.gz>` instead of streaming bytes
// through drush sql:cli — significantly faster for large dumps. The
// optional target database key (alias.sql.target.database) is
// forwarded via --database=<key> if set.
func (d *ddevTransport) ImportDB(ctx context.Context, a *alias.Alias, dumpPath string) error {
	args := []string{"import-db", "--file=" + dumpPath}
	if db := a.SQL.Target.Database; db != "" && db != "default" {
		args = append(args, "--database="+db)
	}
	cmd := exec.CommandContext(ctx, "ddev", args...)
	cmd.Dir = d.approot
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	dlog.Cmdf(cmd.Args)
	return cmd.Run()
}

// ImportFiles satisfies pkg/transport.FilesImporter via
// `ddev import-files --src=<bundle>`. ddev accepts .tar.gz, .tar,
// .zip, or a directory; our cache .tar.gz works directly. Runs with
// Cmd.Dir at the approot so ddev finds .ddev/config.yaml.
func (d *ddevTransport) ImportFiles(ctx context.Context, a *alias.Alias, bundlePath string) error {
	cmd := exec.CommandContext(ctx, "ddev", "import-files", "--src="+bundlePath)
	cmd.Dir = d.approot
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	dlog.Cmdf(cmd.Args)
	return cmd.Run()
}

func (d *ddevTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{"ssh"}
	if d.cfg.Service != "" {
		args = append(args, "-s", d.cfg.Service)
	}
	if workDir != "" {
		args = append(args, "-d", workDir)
	}
	cmd := exec.CommandContext(ctx, "ddev", args...)
	cmd.Dir = d.approot
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findDDEVApproot returns the directory containing .ddev/config.yaml,
// walking up from start. If start is empty, walks up from cwd.
func findDDEVApproot(start string) (string, error) {
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = cwd
	}
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".ddev", "config.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .ddev/config.yaml found walking up from %s", start)
		}
		dir = parent
	}
}
