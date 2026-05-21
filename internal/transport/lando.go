package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/run"
)

type landoTransport struct {
	cfg *alias.LandoTransport
}

func init() {
	Register("lando", Registration{
		RequiredCLI: "lando",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				Lando alias.LandoTransport `yaml:"lando"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type lando: %w", err)
			}
			if w.Lando.AppDir == "" {
				return nil, fmt.Errorf("lando transport requires app_dir (the Lando project root)")
			}
			cfg := w.Lando
			return &landoTransport{cfg: &cfg}, nil
		},
	})
}

func (l *landoTransport) Name() string     { return "lando" }
func (l *landoTransport) Available() error { return CLIAvailable("lando") }

func (l *landoTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := l.build(ctx, remoteCmd)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (l *landoTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := l.build(ctx, remoteCmd)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// build assembles `lando ssh -s <service> -c <one-line>`. Lando's CLI
// requires the working directory to sit inside the Lando app, hence the
// chdir to AppDir.
func (l *landoTransport) build(ctx context.Context, remoteCmd []string) *exec.Cmd {
	service := l.cfg.Service
	if service == "" {
		service = "appserver"
	}
	args := []string{"ssh", "-s", service, "-c", shellLine(remoteCmd)}
	cmd := exec.CommandContext(ctx, "lando", args...)
	cmd.Dir = l.cfg.AppDir
	return cmd
}

func (l *landoTransport) Preview(remoteCmd []string) []string {
	return l.build(context.Background(), remoteCmd).Args
}

func (l *landoTransport) Shell(ctx context.Context, workDir string) error {
	service := l.cfg.Service
	if service == "" {
		service = "appserver"
	}
	cdPart := ""
	if workDir != "" {
		cdPart = "cd " + singleQuote(workDir) + " && "
	}
	args := []string{"ssh", "-s", service, "-c", cdPart + `exec "${SHELL:-/bin/sh}"`}
	cmd := exec.CommandContext(ctx, "lando", args...)
	cmd.Dir = l.cfg.AppDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellLine(argv []string) string {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(singleQuote(a))
	}
	return b.String()
}
