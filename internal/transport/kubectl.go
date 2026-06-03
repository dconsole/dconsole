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

type kubectlTransport struct {
	cfg *alias.KubectlTransport
}

func init() {
	Register("kubectl", Registration{
		RequiredCLI: "kubectl",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				Kubectl alias.KubectlTransport `yaml:"kubectl"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type kubectl: %w", err)
			}
			if w.Kubectl.Resource == "" {
				return nil, fmt.Errorf("kubectl transport requires resource (e.g. deployment/drupal or pods/foo)")
			}
			cfg := w.Kubectl
			return &kubectlTransport{cfg: &cfg}, nil
		},
	})
}

func (k *kubectlTransport) Name() string     { return "kubectl" }
func (k *kubectlTransport) Available() error { return CLIAvailable("kubectl") }

func (k *kubectlTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := k.build(ctx, remoteCmd, stdio.In != nil)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (k *kubectlTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := k.build(ctx, remoteCmd, in != nil)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (k *kubectlTransport) build(ctx context.Context, remoteCmd []string, withStdin bool) *exec.Cmd {
	args := []string{}
	if k.cfg.Kubeconfig != "" {
		args = append(args, "--kubeconfig", k.cfg.Kubeconfig)
	}
	if k.cfg.Namespace != "" {
		args = append(args, "-n", k.cfg.Namespace)
	}
	args = append(args, "exec")
	if withStdin {
		args = append(args, "-i")
	}
	args = append(args, k.cfg.Resource)
	if k.cfg.Container != "" {
		args = append(args, "-c", k.cfg.Container)
	}
	args = append(args, "--")
	args = append(args, remoteCmd...)
	return exec.CommandContext(ctx, "kubectl", args...)
}

func (k *kubectlTransport) Preview(remoteCmd []string) []string {
	return k.build(context.Background(), remoteCmd, false).Args
}

// Wrap returns the LOCAL argv that kubectl will spawn to forward
// `inner` into the pod. Required by pkg/handler.Handler.
func (k *kubectlTransport) Wrap(inner []string) []string { return k.Preview(inner) }

// ShellPreview returns the argv kubectlTransport.Shell() would spawn —
// `kubectl [--kubeconfig …] [-n ns] exec -it <resource> [-c container]
// -- sh -c 'cd workDir && exec "${SHELL:-/bin/sh}"'`.
func (k *kubectlTransport) ShellPreview(workDir string) []string {
	args := []string{"kubectl"}
	if k.cfg.Kubeconfig != "" {
		args = append(args, "--kubeconfig", k.cfg.Kubeconfig)
	}
	if k.cfg.Namespace != "" {
		args = append(args, "-n", k.cfg.Namespace)
	}
	args = append(args, "exec", "-it", k.cfg.Resource)
	if k.cfg.Container != "" {
		args = append(args, "-c", k.cfg.Container)
	}
	cdPart := ""
	if workDir != "" {
		cdPart = "cd '" + workDir + "' && "
	}
	args = append(args, "--", "sh", "-c", cdPart+`exec "${SHELL:-/bin/sh}"`)
	return args
}

func (k *kubectlTransport) Shell(ctx context.Context, workDir string) error {
	args := []string{}
	if k.cfg.Kubeconfig != "" {
		args = append(args, "--kubeconfig", k.cfg.Kubeconfig)
	}
	if k.cfg.Namespace != "" {
		args = append(args, "-n", k.cfg.Namespace)
	}
	args = append(args, "exec", "-it", k.cfg.Resource)
	if k.cfg.Container != "" {
		args = append(args, "-c", k.cfg.Container)
	}
	cdPart := ""
	if workDir != "" {
		cdPart = "cd '" + workDir + "' && "
	}
	args = append(args, "--", "sh", "-c", cdPart+`exec "${SHELL:-/bin/sh}"`)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
