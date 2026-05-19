package command

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/heydon/dconsole/internal/project"
)

// InitOpts controls dconsole project:init.
type InitOpts struct {
	Force   bool // overwrite an existing dconsole.yml
	YesAll  bool // skip interactive prompts (assume yes)
	DryRun  bool // print what would be written; don't write
}

// ProjectInit detects a project at-or-above cwd and writes a dconsole.yml.
// When stdin is interactive and YesAll is false, it shows the detection
// and asks for confirmation before writing.
func ProjectInit(ctx context.Context, out io.Writer, in *os.File, opts InitOpts) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	d, err := project.Detect(cwd)
	if err != nil {
		return err
	}
	if d == nil {
		fmt.Fprintln(out, "no project sources detected (looked for composer.json with drupal/core, .ddev/config.yaml, drush/sites/*.site.yml in cwd and its parents)")
		return nil
	}
	target := filepath.Join(d.Root, "dconsole.yml")

	fmt.Fprintln(out, "─── detection ─────────────────────────────────────────")
	project.PrintDetection(d, out)
	fmt.Fprintf(out, "\ntarget: %s\n", target)
	if _, err := os.Stat(target); err == nil && !opts.Force {
		fmt.Fprintln(out, "(target exists — pass --force to overwrite)")
		return nil
	}

	if opts.DryRun {
		fmt.Fprintln(out, "\n--- generated yaml (dry-run) ---")
		return project.Write(d, out)
	}

	if !opts.YesAll && isInteractive(in) {
		fmt.Fprintf(out, "\nwrite this dconsole.yml? [Y/n] ")
		ans, err := readLine(in)
		if err != nil {
			return err
		}
		if !confirmed(ans) {
			fmt.Fprintln(out, "aborted (nothing written)")
			return nil
		}
	}

	if err := project.Generate(d, target, opts.Force); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s\n", target)
	fmt.Fprintln(out, "next: run `dconsole project:register` to make @"+d.ProjectName+".env reachable from anywhere.")
	return nil
}

func confirmed(s string) bool {
	switch s {
	case "", "y", "Y", "yes", "Yes":
		return true
	}
	return false
}

func readLine(in *os.File) (string, error) {
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	// strip trailing newline
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	return line, nil
}

func isInteractive(in *os.File) bool {
	if in == nil {
		return false
	}
	info, err := in.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// OfferGenerateIfMissing runs the project detector and, if it finds
// sources, either asks the user whether to write dconsole.yml
// (interactive) or skips with a one-line hint (non-interactive). Returns
// the manifest path written (empty if nothing was generated).
//
// Environment switches:
//   DCONSOLE_NO_INIT_PROMPT=1 → never prompt, never auto-write
//   DCONSOLE_AUTOYES=1        → write without asking (CI / scripting)
//
// Called from ResolveContextual when a user invokes a command in a
// project that hasn't been initialized yet — converts the previous
// silent stderr hint into an interactive offer.
func OfferGenerateIfMissing(in, out *os.File) (string, error) {
	if os.Getenv("DCONSOLE_NO_INIT_PROMPT") != "" {
		return "", nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	d, err := project.Detect(cwd)
	if err != nil || d == nil {
		return "", err
	}
	target := filepath.Join(d.Root, "dconsole.yml")
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}
	autoYes := os.Getenv("DCONSOLE_AUTOYES") != ""

	if !autoYes && !isInteractive(in) {
		fmt.Fprintf(out, "hint: no dconsole.yml at %s — `dconsole project:init` can generate one (detected %d env(s)); set DCONSOLE_AUTOYES=1 to auto-generate\n", d.Root, len(d.Envs))
		return "", nil
	}

	if !autoYes {
		fmt.Fprintln(out, "─── no dconsole.yml here ──────────────────────────────")
		project.PrintDetection(d, out)
		fmt.Fprintf(out, "\ngenerate %s now? [Y/n] ", target)
		ans, err := readLine(in)
		if err != nil {
			return "", err
		}
		if !confirmed(ans) {
			fmt.Fprintln(out, "skipped (run `dconsole project:init` later to do this)")
			return "", nil
		}
	} else {
		fmt.Fprintf(out, "DCONSOLE_AUTOYES=1: generating %s\n", target)
	}

	if err := project.Generate(d, target, false); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "wrote %s\n\n", target)
	return target, nil
}
