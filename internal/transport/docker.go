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

type dockerTransport struct {
	cfg *alias.DockerTransport
}

func init() {
	Register("docker", Registration{
		RequiredCLI: "docker",
		Build: func(a *alias.Alias) (Transport, error) {
			if a.Transport.Docker == nil {
				return nil, fmt.Errorf("transport type docker requires a docker: block")
			}
			if a.Transport.Docker.Container == "" {
				return nil, fmt.Errorf("docker transport requires a container name")
			}
			return &dockerTransport{cfg: a.Transport.Docker}, nil
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
	return cmd.Run()
}

func (d *dockerTransport) argv(remoteCmd []string) []string {
	args := []string{"exec", "-i"}
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

func (d *dockerTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{"exec", "-it"}
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
