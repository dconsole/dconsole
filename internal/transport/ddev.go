package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/run"
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
			if a.Transport.DDEV == nil {
				return nil, fmt.Errorf("transport type ddev requires a ddev: block")
			}
			if a.Transport.DDEV.Project == "" {
				return nil, fmt.Errorf("ddev transport requires project (the project name passed to `ddev describe`)")
			}
			approot, err := findDDEVApproot(a.Root)
			if err != nil {
				return nil, fmt.Errorf("ddev transport: %w", err)
			}
			return &ddevTransport{cfg: a.Transport.DDEV, approot: approot}, nil
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
