//go:build darwin

// container-compose handler. Apple's `container` is macOS-only,
// and container-compose itself only ships on macOS, so restrict
// the build to darwin — Linux/Windows binaries won't register the
// handler, won't list it in transport:list, and the inline URI
// parser rejects `@container-compose://…` with "unknown scheme".

package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/run"
)

// containerComposeTransport runs commands inside a service container
// orchestrated by container-compose (https://github.com/mcrich23/container-compose).
//
// container-compose's CLI only supports build / up / down / version —
// it has no `exec` subcommand. dconsole therefore shells to Apple's
// `container exec <containerName>` directly. The container name is
// derived as "<project>-<service>" per container-compose's documented
// naming convention.
type containerComposeTransport struct {
	cfg *alias.ContainerComposeTransport
}

func init() {
	Register("container-compose", Registration{
		// We actually invoke Apple's `container` binary (container-compose
		// has no exec). The user needs container-compose installed only
		// to manage lifecycle (up/down) — dconsole talks straight to
		// the runtime once the service is up.
		RequiredCLI:     "container",
		HideWhenMissing: true,
		// container-compose is a third-party project tracking Apple's
		// pre-1.0 containerization stack; both layers are moving
		// targets. Flag list output so users see this isn't stable.
		Experimental: true,
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				ContainerCompose alias.ContainerComposeTransport `yaml:"container_compose"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type container-compose: %w", err)
			}
			cfg := w.ContainerCompose
			if cfg.Service == "" {
				return nil, fmt.Errorf("container-compose transport requires service")
			}
			if cfg.ProjectName == "" && cfg.ProjectDir == "" {
				return nil, fmt.Errorf("container-compose transport requires project_name or project_dir")
			}
			return &containerComposeTransport{cfg: &cfg}, nil
		},
	})
}

func (c *containerComposeTransport) Name() string     { return "container-compose" }
func (c *containerComposeTransport) Available() error { return CLIAvailable("container") }

// containerName follows container-compose's documented convention:
// "<projectName>-<serviceName>". projectName comes from the explicit
// project_name override when set, otherwise basename(project_dir).
func (c *containerComposeTransport) containerName() string {
	project := c.cfg.ProjectName
	if project == "" {
		project = filepath.Base(c.cfg.ProjectDir)
	}
	return project + "-" + c.cfg.Service
}

func (c *containerComposeTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := exec.CommandContext(ctx, "container", c.argv(remoteCmd)...)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (c *containerComposeTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "container", c.argv(remoteCmd)...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// argv builds `container exec -i [--user u] [exec_options] <name> <cmd>`.
// `-i` keeps stdin open for Pipe; `-t` is added in ShellPreview when a
// TTY is wanted (interactive shell).
func (c *containerComposeTransport) argv(remoteCmd []string) []string {
	args := []string{"exec", "-i"}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	args = append(args, c.cfg.ExecOptions...)
	args = append(args, c.containerName())
	args = append(args, remoteCmd...)
	return args
}

func (c *containerComposeTransport) Preview(remoteCmd []string) []string {
	return append([]string{"container"}, c.argv(remoteCmd)...)
}

func (c *containerComposeTransport) Wrap(inner []string) []string { return c.Preview(inner) }

func (c *containerComposeTransport) ShellPreview(workDir string) []string {
	args := []string{"container", "exec", "-it"}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	args = append(args, c.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, c.containerName(), "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	return args
}

func (c *containerComposeTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{"exec", "-it"}
	if c.cfg.User != "" {
		args = append(args, "--user", c.cfg.User)
	}
	args = append(args, c.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, c.containerName(), "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	cmd := exec.CommandContext(ctx, "container", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
