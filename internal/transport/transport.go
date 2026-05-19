package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/run"
)

// Transport reaches a remote environment described by an Alias. Each
// implementation wraps a CLI on $PATH (ssh, docker, kubectl, ddev, …).
type Transport interface {
	Name() string
	Available() error
	Exec(ctx context.Context, cmd []string, stdio run.Stdio) error
	Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error
	// Shell drops the user into an interactive shell on the remote with
	// workDir as the starting directory. The user's TTY is attached.
	Shell(ctx context.Context, workDir string) error
	// Preview returns the LOCAL argv that Exec would spawn for the given
	// remote command, without running anything. Used by `dconsole inspect`
	// so the user can see exactly what dconsole would invoke.
	Preview(remoteCmd []string) []string
}

// Factory builds a Transport from the alias's transport block.
type Factory func(a *alias.Alias) (Transport, error)

var registry = map[string]Factory{}

// Register adds a Factory under a transport type name. Idempotent.
func Register(name string, f Factory) {
	registry[name] = f
}

// For returns the Transport implementation that an alias has configured.
func For(a *alias.Alias) (Transport, error) {
	t := a.Transport.Type
	if t == "" {
		return nil, fmt.Errorf("alias @%s.%s has no transport configured", a.Site, a.Env)
	}
	f, ok := registry[t]
	if !ok {
		return nil, fmt.Errorf("unknown transport type %q on @%s.%s", t, a.Site, a.Env)
	}
	return f(a)
}

// Names returns registered transport names; useful for help text.
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// ProbeAvailable checks whether the underlying CLI for a transport is on
// PATH. Independent of any alias configuration — only checks the CLI
// binary, not the configuration. The transport name maps to a CLI name.
func ProbeAvailable(name string) error {
	cli := cliFor(name)
	if cli == "" {
		return fmt.Errorf("unknown transport %q", name)
	}
	return CLIAvailable(cli)
}

func cliFor(transport string) string {
	switch transport {
	case "exec":
		return "sh" // proxy: exec transport just needs a shell-launchable binary
	case "ssh":
		return "ssh"
	case "docker", "compose":
		return "docker"
	case "kubectl":
		return "kubectl"
	case "ddev":
		return "ddev"
	case "lando":
		return "lando"
	}
	return ""
}
