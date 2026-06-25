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

// containerTransport runs commands via Apple's `container` CLI —
// the lightweight OCI runtime that ships with macOS 15+. The command
// surface mirrors docker (`exec -it`, `--user`, `--workdir`, …) so
// this implementation is a near-copy of dockerTransport without the
// remote-daemon flags (`-H` / `--context`) the Apple CLI doesn't
// support.
type containerTransport struct {
	cfg *alias.ContainerTransport
}

func init() {
	Register("container", Registration{
		RequiredCLI: "container",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				Container alias.ContainerTransport `yaml:"container"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type container: %w", err)
			}
			if w.Container.Container == "" {
				return nil, fmt.Errorf("container transport requires a container name")
			}
			cfg := w.Container
			return &containerTransport{cfg: &cfg}, nil
		},
	})
}

func (c *containerTransport) Name() string     { return "container" }
func (c *containerTransport) Available() error { return CLIAvailable("container") }

func (c *containerTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := exec.CommandContext(ctx, "container", c.argv(remoteCmd)...)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (c *containerTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "container", c.argv(remoteCmd)...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// argv builds `container exec -i [--user u] [exec_options] <container> <cmd>`.
// `-i` keeps stdin open for Pipe; we omit `-t` here because we may not
// have a TTY attached (sql:dump pipes binary through). ShellPreview
// adds `-t` for interactive use.
func (c *containerTransport) argv(remoteCmd []string) []string {
	args := []string{"exec", "-i"}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	args = append(args, c.cfg.ExecOptions...)
	args = append(args, c.cfg.Container)
	args = append(args, remoteCmd...)
	return args
}

func (c *containerTransport) Preview(remoteCmd []string) []string {
	return append([]string{"container"}, c.argv(remoteCmd)...)
}

// Wrap returns the LOCAL argv that `container` will spawn to forward
// `inner` into the container. Required by pkg/handler.Handler.
func (c *containerTransport) Wrap(inner []string) []string { return c.Preview(inner) }

// ShellPreview returns the argv containerTransport.Shell() would
// spawn — `container exec -it [--user u] [-w workDir] <c> sh -c
// 'exec "${SHELL:-/bin/sh}"'`. Used by `dconsole inspect @alias sh`.
func (c *containerTransport) ShellPreview(workDir string) []string {
	args := []string{"container", "exec", "-it"}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	args = append(args, c.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, c.cfg.Container, "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	return args
}

func (c *containerTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{"exec", "-it"}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	args = append(args, c.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, c.cfg.Container, "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	cmd := exec.CommandContext(ctx, "container", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
