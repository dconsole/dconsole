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
			if w.Compose.Host != "" && w.Compose.Context != "" {
				return nil, fmt.Errorf("compose transport: `host:` and `context:` are mutually exclusive")
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

// daemonFlags returns the global `docker` flags that pin every
// invocation to the right daemon: `-H ssh://…`, `--context name`, or
// nothing for the local daemon. Live BEFORE the `compose` subcommand.
func (c *composeTransport) daemonFlags() []string {
	switch {
	case c.cfg.Host != "":
		return []string{"-H", c.cfg.Host}
	case c.cfg.Context != "":
		return []string{"--context", c.cfg.Context}
	}
	return nil
}

func (c *composeTransport) build(ctx context.Context, remoteCmd []string) *exec.Cmd {
	args := append(c.daemonFlags(), "compose")
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

// Wrap returns the LOCAL argv that `docker compose exec` will spawn
// to forward `inner` into the service container. Required by
// pkg/handler.Handler.
func (c *composeTransport) Wrap(inner []string) []string { return c.Preview(inner) }

// WrapShell is the TTY-aware sibling of Wrap. Wrap adds `-T` (no
// pseudo-tty) which is right for non-interactive command forwarding
// but wrong for an interactive shell. WrapShell drops -T so docker
// allocates a tty inside the container. Used by Chain.Shell.
func (c *composeTransport) WrapShell(inner []string) []string {
	args := append([]string{"docker"}, c.daemonFlags()...)
	args = append(args, "compose")
	if c.cfg.ProjectDir != "" {
		args = append(args, "--project-directory", c.cfg.ProjectDir)
	}
	args = append(args, "exec")
	args = append(args, c.cfg.ExecOptions...)
	args = append(args, c.cfg.Service)
	args = append(args, inner...)
	return args
}

// ShellPreview returns the argv composeTransport.Shell() would spawn.
// Matches its existing Shell() implementation exactly: `docker
// [-H host | --context name] compose ... exec [-w workDir] <service>
// sh -c 'exec "${SHELL:-/bin/sh}"'`.
func (c *composeTransport) ShellPreview(workDir string) []string {
	args := append([]string{"docker"}, c.daemonFlags()...)
	args = append(args, "compose")
	if c.cfg.ProjectDir != "" {
		args = append(args, "--project-directory", c.cfg.ProjectDir)
	}
	args = append(args, "exec")
	args = append(args, c.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, c.cfg.Service, "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	return args
}

func (c *composeTransport) Shell(ctx context.Context, workDir string) error {
	args := append(c.daemonFlags(), "compose")
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
