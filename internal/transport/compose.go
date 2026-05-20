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

type composeTransport struct {
	cfg *alias.ComposeTransport
}

func init() {
	Register("compose", Registration{
		RequiredCLI: "docker",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				Compose alias.ComposeTransport `yaml:"compose"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type compose: %w", err)
			}
			if w.Compose.Service == "" {
				return nil, fmt.Errorf("compose transport requires service")
			}
			cfg := w.Compose
			return &composeTransport{cfg: &cfg}, nil
		},
	})
}

func (c *composeTransport) Name() string     { return "compose" }
func (c *composeTransport) Available() error { return CLIAvailable("docker") }

func (c *composeTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := c.build(ctx, remoteCmd)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (c *composeTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := c.build(ctx, remoteCmd)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *composeTransport) build(ctx context.Context, remoteCmd []string) *exec.Cmd {
	args := []string{"compose"}
	if c.cfg.ProjectDir != "" {
		args = append(args, "--project-directory", c.cfg.ProjectDir)
	}
	args = append(args, "exec", "-T")
	args = append(args, c.cfg.ExecOptions...)
	args = append(args, c.cfg.Service)
	args = append(args, remoteCmd...)
	return exec.CommandContext(ctx, "docker", args...)
}

func (c *composeTransport) Preview(remoteCmd []string) []string {
	return c.build(context.Background(), remoteCmd).Args
}

func (c *composeTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{"compose"}
	if c.cfg.ProjectDir != "" {
		args = append(args, "--project-directory", c.cfg.ProjectDir)
	}
	args = append(args, "exec")
	args = append(args, c.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, c.cfg.Service, "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
