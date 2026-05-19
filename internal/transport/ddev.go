package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/run"
)

type ddevTransport struct {
	cfg *alias.DDEVTransport
}

func init() {
	Register("ddev", func(a *alias.Alias) (Transport, error) {
		if a.Transport.DDEV == nil {
			return nil, fmt.Errorf("transport type ddev requires a ddev: block")
		}
		if a.Transport.DDEV.Project == "" {
			return nil, fmt.Errorf("ddev transport requires project (the project name passed to `ddev describe`)")
		}
		return &ddevTransport{cfg: a.Transport.DDEV}, nil
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

// build constructs `ddev exec --project=<name> [-s service] -- <cmd>`.
// `ddev` exec runs in the project's web container by default.
func (d *ddevTransport) build(ctx context.Context, remoteCmd []string) *exec.Cmd {
	args := []string{"--project", d.cfg.Project, "exec"}
	if d.cfg.Service != "" {
		args = append(args, "-s", d.cfg.Service)
	}
	args = append(args, "--")
	args = append(args, remoteCmd...)
	return exec.CommandContext(ctx, "ddev", args...)
}

func (d *ddevTransport) Preview(remoteCmd []string) []string {
	return d.build(context.Background(), remoteCmd).Args
}

func (d *ddevTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{"--project", d.cfg.Project, "ssh"}
	if d.cfg.Service != "" {
		args = append(args, "-s", d.cfg.Service)
	}
	if workDir != "" {
		args = append(args, "-d", workDir)
	}
	cmd := exec.CommandContext(ctx, "ddev", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
