// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Chain composes multiple handlers into a single Handler whose Wrap
// applies each layer's Wrap from inside out:
//
//	layers   = [ssh, docker]   // outer-to-inner, matching YAML order
//	inner    = [drush, status]
//	step 1   = docker.Wrap(inner)             // [docker, exec, c, drush, status]
//	step 2   = ssh.Wrap(step1)                // [ssh, user@host, --, "docker exec c drush status"]
//	spawn    = step 2
//
// Each outer layer is responsible for shell-quoting its argument when
// its protocol needs it (ssh does; docker doesn't). That logic lives
// inside the individual Wrap implementations, not here.
//
// Capability resolution is delegated to the innermost layer for v0.4.0
// — if the inner handler implements DBImporter, the chain reports
// itself as a DBImporter via the InnerCapable accessor below.
type Chain struct {
	// layers ordered outer-to-inner. layers[0] is what we spawn locally
	// after all wrapping. layers[len-1] is closest to the drush argv.
	layers []Handler
}

// NewChain returns a chain of two-or-more handlers. Passing a single
// handler is a programming error — callers should use the handler
// directly. Empty chains are also rejected.
func NewChain(layers []Handler) *Chain {
	if len(layers) < 2 {
		panic("handler.NewChain requires at least 2 layers; use the inner handler directly for a single layer")
	}
	out := make([]Handler, len(layers))
	copy(out, layers)
	return &Chain{layers: out}
}

// Layers returns a copy of the chain's layers in outer-to-inner order.
// Useful for inspect output and tests.
func (c *Chain) Layers() []Handler {
	out := make([]Handler, len(c.layers))
	copy(out, c.layers)
	return out
}

// Inner returns the innermost layer — the one closest to the drush argv.
// Used for capability resolution (DBImporter, LoginCapable, …).
func (c *Chain) Inner() Handler {
	return c.layers[len(c.layers)-1]
}

// Outer returns the outermost layer — the one actually spawned locally.
func (c *Chain) Outer() Handler {
	return c.layers[0]
}

func (c *Chain) Name() string {
	names := make([]string, len(c.layers))
	for i, l := range c.layers {
		names[i] = l.Name()
	}
	return joinChainNames(names)
}

func (c *Chain) Available() error {
	for _, l := range c.layers {
		if err := l.Available(); err != nil {
			return fmt.Errorf("handler %q in chain: %w", l.Name(), err)
		}
	}
	return nil
}

// Wrap applies each layer's Wrap from inside out. The final result is
// the LOCAL argv to spawn.
func (c *Chain) Wrap(inner []string) []string {
	cur := inner
	for i := len(c.layers) - 1; i >= 0; i-- {
		cur = c.layers[i].Wrap(cur)
	}
	return cur
}

// Preview returns the fully-wrapped local argv for inspect-style output.
func (c *Chain) Preview(remoteCmd []string) []string {
	return c.Wrap(remoteCmd)
}

// Exec spawns the fully-wrapped argv locally. Chain doesn't delegate
// to layers[0].Exec — each layer's Exec/Pipe carries its own assumption
// about argv shape; the chain composes via Wrap and runs the result
// itself, which is simpler and unambiguous.
func (c *Chain) Exec(ctx context.Context, cmd []string, stdio Stdio) error {
	argv := c.Wrap(cmd)
	if len(argv) == 0 {
		return fmt.Errorf("chain produced empty argv for %v", cmd)
	}
	process := exec.CommandContext(ctx, argv[0], argv[1:]...)
	process.Stdin = stdio.In
	process.Stdout = stdio.Out
	process.Stderr = stdio.Err
	return process.Run()
}

// Pipe is Exec with explicit stdin/stdout. Mirrors today's transport.Pipe.
func (c *Chain) Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error {
	return c.Exec(ctx, cmd, Stdio{In: in, Out: out})
}

// Shell drops the user into an interactive shell at the INNERMOST
// layer of the chain — for ssh→docker that means inside the docker
// container, not on the ssh host. This is what users almost always
// want from `dconsole sh` (drush lives in the container, not on the
// box hosting docker).
//
// Composition: each layer that implements ShellWrapper provides a
// TTY-aware wrap (ssh adds -t, compose drops -T, docker/kubectl add
// -it, etc.). Layers that don't implement it fall back to Wrap —
// which works but may not get a TTY through, so the inner shell can
// exit immediately. In practice every TTY-relevant in-tree handler
// implements ShellWrapper.
//
// The innermost layer's "open a shell" command is `["bash", "-l"]`
// by default. workDir is dropped here — the outer layers can't know
// what the inner container considers a valid path. If you need cwd
// control, pass it via `dconsole sh @alias <path>` and the innermost
// layer's ShellWrapper can interpret it. (Innermost ShellWrapper
// implementations may add their own `cd workDir &&` prefix.)
func (c *Chain) Shell(ctx context.Context, workDir string) error {
	cur := previewShell(c.Inner(), workDir)
	for i := len(c.layers) - 2; i >= 0; i-- {
		if sw, ok := c.layers[i].(ShellWrapper); ok {
			cur = sw.WrapShell(cur)
		} else {
			cur = c.layers[i].Wrap(cur)
		}
	}
	if len(cur) == 0 {
		return fmt.Errorf("chain produced empty argv for shell")
	}
	cmd := exec.CommandContext(ctx, cur[0], cur[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// previewShell returns the argv that opens an interactive shell at
// `h`. Prefers ShellPreviewer (matches h.Shell() exactly), then
// ShellWrapper ([bash, -l] wrapped TTY-aware), then plain Wrap.
func previewShell(h Handler, workDir string) []string {
	if sp, ok := h.(ShellPreviewer); ok {
		return sp.ShellPreview(workDir)
	}
	if sw, ok := h.(ShellWrapper); ok {
		return sw.WrapShell([]string{"bash", "-l"})
	}
	return h.Wrap([]string{"bash", "-l"})
}

// Inner returns the innermost concrete handler — useful for capability
// type assertions. For a non-chain handler it's the identity. For a
// chain it's the layer closest to drush, where DBImporter / DBSyncer /
// etc. capabilities resolve. Convention:
//
//	if s, ok := handler.Inner(h).(handler.DBImporter); ok { … }
//
// Chains don't bubble capabilities to themselves because the outer
// layers (ssh, ssh-jump) don't know how to import a local file into
// the container — only the innermost layer does.
func Inner(h Handler) Handler {
	if c, ok := h.(*Chain); ok {
		return c.Inner()
	}
	return h
}

// ShellArgv returns the argv that `dconsole sh @alias` would spawn.
//
// For single-handler aliases this is the handler's own Shell()
// argv (via ShellPreviewer) — ddev returns `ddev ssh`, not the
// generic `ddev exec -- bash -l`. For chains it composes from
// inside-out: the innermost layer's ShellPreview (its
// shell-in-this-env argv) gets wrapped by each outer layer's
// ShellWrapper. End result matches Chain.Shell's spawn exactly.
func ShellArgv(h Handler, workDir string) []string {
	if c, ok := h.(*Chain); ok {
		cur := previewShell(c.Inner(), workDir)
		for i := len(c.layers) - 2; i >= 0; i-- {
			if sw, ok := c.layers[i].(ShellWrapper); ok {
				cur = sw.WrapShell(cur)
			} else {
				cur = c.layers[i].Wrap(cur)
			}
		}
		return cur
	}
	return previewShell(h, workDir)
}

// joinChainNames produces "ssh→docker" for inspect/error output.
func joinChainNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += "→" + n
	}
	return out
}
