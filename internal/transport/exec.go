package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/run"
)

// execTransport runs the remote command on the local machine. Useful when
// dconsole is colocated with Drush/Drupal (e.g. on a server), and as a test
// hook for integration tests that don't want to spin up SSH or Docker.
type execTransport struct {
	cfg *alias.ExecTransport
}

func init() {
	Register("exec", Registration{
		// exec runs commands directly via os/exec.Command — no external CLI
		// to depend on, so RequiredCLI is empty.
		Build: func(a *alias.Alias) (Transport, error) {
			// No required fields; the exec: block may be omitted entirely.
			var w struct {
				Exec alias.ExecTransport `yaml:"exec"`
			}
			if a.Transport.Raw.Kind != 0 {
				if err := a.Transport.Decode(&w); err != nil {
					return nil, fmt.Errorf("transport type exec: %w", err)
				}
			}
			cfg := w.Exec
			return &execTransport{cfg: &cfg}, nil
		},
	})
}

func (e *execTransport) Name() string     { return "exec" }
func (e *execTransport) Available() error { return nil }

func (e *execTransport) Exec(ctx context.Context, cmd []string, stdio run.Stdio) error {
	if len(cmd) == 0 {
		return fmt.Errorf("empty command")
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = e.cfg.Dir
	c.Stdin = stdio.In
	c.Stdout = stdio.Out
	c.Stderr = stdio.Err
	return c.Run()
}

func (e *execTransport) Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error {
	if len(cmd) == 0 {
		return fmt.Errorf("empty command")
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = e.cfg.Dir
	c.Stdin = in
	c.Stdout = out
	c.Stderr = os.Stderr
	return c.Run()
}

func (e *execTransport) Preview(remoteCmd []string) []string {
	return remoteCmd
}

func (e *execTransport) Shell(ctx context.Context, workDir string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	c := exec.CommandContext(ctx, shell, "-l")
	switch {
	case workDir != "":
		c.Dir = workDir
	case e.cfg.Dir != "":
		c.Dir = e.cfg.Dir
	}
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
