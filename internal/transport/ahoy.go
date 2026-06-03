package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dconsole/dconsole/internal/alias"
)

// ahoyTransport runs remote commands through the `ahoy` CLI, which
// reads tasks from a .ahoy.yml at or above the working directory.
// The transport rewrites a command like `[drush sql:dump]` into
// `ahoy drush sql:dump` and sets Cmd.Dir to the project root so ahoy
// finds its config regardless of where dconsole was invoked from.
type ahoyTransport struct {
	cfg     alias.AhoyTransport
	workDir string // resolved dir that contains (or has an ancestor with) .ahoy.yml
}

func init() {
	Register("ahoy", Registration{
		RequiredCLI: "ahoy",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				Ahoy alias.AhoyTransport `yaml:"ahoy"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type ahoy: %w", err)
			}
			start := w.Ahoy.Dir
			if start == "" {
				start = a.Root
			}
			dir, err := findAhoyDir(start)
			if err != nil {
				return nil, fmt.Errorf("ahoy transport: %w", err)
			}
			return &ahoyTransport{cfg: w.Ahoy, workDir: dir}, nil
		},
	})
}

func (h *ahoyTransport) Name() string     { return "ahoy" }
func (h *ahoyTransport) Available() error { return CLIAvailable("ahoy") }

func (h *ahoyTransport) Exec(ctx context.Context, remoteCmd []string, stdio Stdio) error {
	cmd := h.build(ctx, remoteCmd)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (h *ahoyTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := h.build(ctx, remoteCmd)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (h *ahoyTransport) Preview(remoteCmd []string) []string {
	return h.build(context.Background(), remoteCmd).Args
}

// Wrap returns the LOCAL argv that ahoy will spawn to dispatch to the
// configured task (typically `ahoy drush ...`). Required by
// pkg/handler.Handler.
func (h *ahoyTransport) Wrap(inner []string) []string { return h.Preview(inner) }

// ShellPreview returns the argv ahoy.Shell() would spawn — `ahoy shell`,
// which expects users to have a `shell:` task defined in their .ahoy.yml.
// workDir is ignored (ahoy resolves cwd from the task definition).
func (h *ahoyTransport) ShellPreview(workDir string) []string {
	return []string{"ahoy", "shell"}
}

func (h *ahoyTransport) Shell(ctx context.Context, workDir string) error {
	// `ahoy` itself has no shell verb; users typically define a `shell`
	// task in .ahoy.yml. If they have, this invokes it.
	task := "shell"
	cmd := exec.CommandContext(ctx, "ahoy", task)
	if workDir != "" {
		cmd.Dir = workDir
	} else {
		cmd.Dir = h.workDir
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// build constructs `ahoy <task> <args...>`. Task comes from cfg.Task
// or, when unset, the basename of remoteCmd[0] (which dconsole's bin
// resolution typically sets to "drush" / "drupal"). Cmd.Dir is the
// resolved .ahoy.yml-containing directory.
func (h *ahoyTransport) build(ctx context.Context, remoteCmd []string) *exec.Cmd {
	task := h.cfg.Task
	args := remoteCmd
	if len(remoteCmd) > 0 {
		if task == "" {
			task = filepath.Base(remoteCmd[0])
		}
		args = remoteCmd[1:]
	}
	if task == "" {
		// Defensive: empty remoteCmd is a no-op; ahoy itself prints help.
		task = ""
	}
	argv := []string{task}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "ahoy", argv...)
	cmd.Dir = h.workDir
	return cmd
}

// findAhoyDir walks up from start looking for .ahoy.yml. Returns the
// directory containing it. If start is empty, walks up from cwd.
func findAhoyDir(start string) (string, error) {
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = cwd
	}
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".ahoy.yml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .ahoy.yml found walking up from %s", start)
		}
		dir = parent
	}
}
