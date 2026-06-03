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

type dockerTransport struct {
	cfg *alias.DockerTransport
}

func init() {
	Register("docker", Registration{
		RequiredCLI: "docker",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				Docker alias.DockerTransport `yaml:"docker"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type docker: %w", err)
			}
			if w.Docker.Container == "" {
				return nil, fmt.Errorf("docker transport requires a container name")
			}
			if w.Docker.Host != "" && w.Docker.Context != "" {
				return nil, fmt.Errorf("docker transport: `host:` and `context:` are mutually exclusive")
			}
			cfg := w.Docker
			return &dockerTransport{cfg: &cfg}, nil
		},
	})
}

func (d *dockerTransport) Name() string     { return "docker" }
func (d *dockerTransport) Available() error { return CLIAvailable("docker") }

func (d *dockerTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := exec.CommandContext(ctx, "docker", d.argv(remoteCmd)...)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (d *dockerTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "docker", d.argv(remoteCmd)...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// daemonFlags returns the `docker` global flags that pin every
// invocation to the right daemon: `-H ssh://…`, `--context name`, or
// nothing for the local daemon. Sits BEFORE the subcommand (exec,
// ps, compose, …) because docker requires global flags up front.
func (d *dockerTransport) daemonFlags() []string {
	switch {
	case d.cfg.Host != "":
		return []string{"-H", d.cfg.Host}
	case d.cfg.Context != "":
		return []string{"--context", d.cfg.Context}
	}
	return nil
}

func (d *dockerTransport) argv(remoteCmd []string) []string {
	args := append(d.daemonFlags(), "exec", "-i")
	if d.cfg.User != "" {
		args = append(args, "--user", d.cfg.User)
	}
	args = append(args, d.cfg.ExecOptions...)
	args = append(args, d.cfg.Container)
	args = append(args, remoteCmd...)
	return args
}

func (d *dockerTransport) Preview(remoteCmd []string) []string {
	return append([]string{"docker"}, d.argv(remoteCmd)...)
}

// Wrap returns the LOCAL argv that docker will spawn to forward `inner`
// into the container. Required by pkg/handler.Handler.
func (d *dockerTransport) Wrap(inner []string) []string { return d.Preview(inner) }

// ShellPreview returns the argv dockerTransport.Shell() would spawn —
// `docker [-H host | --context name] exec -it [--user u] [-w workDir]
// <container> sh -c 'exec "${SHELL:-/bin/sh}"'`.
func (d *dockerTransport) ShellPreview(workDir string) []string {
	args := append([]string{"docker"}, d.daemonFlags()...)
	args = append(args, "exec", "-it")
	if d.cfg.User != "" {
		args = append(args, "--user", d.cfg.User)
	}
	args = append(args, d.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, d.cfg.Container, "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	return args
}

func (d *dockerTransport) Shell(ctx context.Context, workDir string) error {
	args := append(d.daemonFlags(), "exec", "-it")
	if d.cfg.User != "" {
		args = append(args, "--user", d.cfg.User)
	}
	args = append(args, d.cfg.ExecOptions...)
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, d.cfg.Container, "sh", "-c", `exec "${SHELL:-/bin/sh}"`)
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
