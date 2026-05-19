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

// execTransport runs the remote command on the local machine. Useful when
// dconsole is colocated with Drush/Drupal (e.g. on a server), and as a test
// hook for integration tests that don't want to spin up SSH or Docker.
type execTransport struct {
	cfg *alias.ExecTransport
}

func init() {
	Register("exec", func(a *alias.Alias) (Transport, error) {
		// No required fields; the block may be omitted entirely.
		cfg := a.Transport.Exec
		if cfg == nil {
			cfg = &alias.ExecTransport{}
		}
		return &execTransport{cfg: cfg}, nil
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
