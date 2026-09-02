package command

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/project"
)

// WireProjectResolution attaches the project registry/manifest lookup to
// the loader so @project.env references work from anywhere on the system.
func WireProjectResolution(l *alias.Loader) {
	l.ProjectLookup = func(site, env string) (*alias.Alias, bool, error) {
		reg, err := project.LoadRegistry()
		if err != nil {
			return nil, false, err
		}
		path := reg.Lookup(site)
		if path == "" {
			return nil, false, nil
		}
		m, err := project.LoadManifestByName(site, path)
		if err != nil {
			return nil, false, fmt.Errorf("project %q: %w", site, err)
		}
		a, err := m.ResolveEnv(env)
		if err != nil {
			return nil, false, err
		}
		return a, true, nil
	}
	l.ProjectEnvsLookup = func(site string) ([]string, bool, error) {
		reg, err := project.LoadRegistry()
		if err != nil {
			return nil, false, err
		}
		path := reg.Lookup(site)
		if path == "" {
			return nil, false, nil
		}
		m, err := project.LoadManifestByName(site, path)
		if err != nil {
			return nil, false, err
		}
		return m.EnvNames(), true, nil
	}
	l.ProjectNames = func() []string {
		reg, err := project.LoadRegistry()
		if err != nil {
			return nil
		}
		return reg.Names()
	}
}

// ProjectRegister has two shapes:
//
//   - `dconsole project:register` (no args) — walks up from cwd looking
//     for a .dconsole.yml, and adds it to the registry as a SYMLINK
//     (~/.dconsole/projects/<name>.yml → the found manifest). Standard
//     "I'm in a project checkout, register it" flow.
//
//   - `dconsole project:register <path>` — treats <path> as a downloaded
//     or hand-authored manifest file, and COPIES it into the registry
//     as a standalone drop-in (~/.dconsole/projects/<name>.yml). Enables
//     the "run drush without local Drupal source" workflow: grab a
//     manifest out of a GitHub gist / email / share link, point
//     project:register at it, and `dconsole @name.env cr` works from
//     anywhere immediately — no clone, no override, no local Drupal
//     tree needed.
//
// The copy form requires the source to have an explicit `project:` key
// (the walk form is happy to infer one from the parent directory name,
// but a downloaded file's directory is meaningless — usually Downloads).
func ProjectRegister(out io.Writer, args []string) error {
	if len(args) > 0 {
		return projectRegisterFromPath(out, args[0])
	}
	return projectRegisterFromCwd(out)
}

func projectRegisterFromCwd(out io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	path, err := project.FindManifest(cwd)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("no .dconsole.yml found in %s or any parent (legacy dconsole.yml also accepted) — create one and try again", cwd)
	}
	m, err := project.LoadManifest(path)
	if err != nil {
		return err
	}
	reg, err := project.LoadRegistry()
	if err != nil {
		return err
	}
	if err := reg.Register(m.Project, path); err != nil {
		return err
	}
	fmt.Fprintf(out, "registered project %q → %s (symlink)\n", m.Project, path)
	return nil
}

func projectRegisterFromPath(out io.Writer, src string) error {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(absSrc)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory — pass the path to a .dconsole.yml file, or `cd` into it and run `dconsole project:register` with no args", src)
	}
	m, err := project.LoadManifest(absSrc)
	if err != nil {
		return fmt.Errorf("parse %s: %w", src, err)
	}
	if m.Project == "" {
		return fmt.Errorf("%s has no `project:` key — a downloaded manifest must name its project explicitly", src)
	}
	// Copy the file content verbatim into the registry (don't
	// re-marshal the parsed manifest — that would drop comments,
	// reformat, and hide fields we don't yet model).
	data, err := os.ReadFile(absSrc)
	if err != nil {
		return err
	}
	dir := project.DefaultRegistryDir()
	if dir == "" {
		return fmt.Errorf("can't resolve home directory for registry")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(dir, m.Project+".yml")
	// Conflict check: if the entry already exists, refuse rather than
	// silently overwriting. Users can `project:forget` first.
	if existing, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("project %q already registered at %s — `dconsole project:forget %s` first (existing type: %s)", m.Project, dest, m.Project, describeMode(existing.Mode()))
	}
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "registered project %q → %s (copied from %s)\n", m.Project, dest, absSrc)
	fmt.Fprintf(out, "hint: no local checkout needed — `dconsole @%s.<env> <cmd>` will work from anywhere.\n", m.Project)
	return nil
}

func describeMode(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "symlink"
	case m.IsRegular():
		return "regular file"
	default:
		return "unknown"
	}
}

// ProjectList prints registered projects + the manifest auto-discovered
// at-or-above cwd (if any).
func ProjectList(out io.Writer) error {
	reg, err := project.LoadRegistry()
	if err != nil {
		return err
	}
	names := reg.Names()
	if len(names) == 0 {
		fmt.Fprintln(out, "(no projects registered — run `dconsole project:register` inside a project,\n or `dconsole project:register <path>` to import a downloaded .dconsole.yml)")
	}
	regDir := project.DefaultRegistryDir()
	for _, n := range names {
		path := reg.Lookup(n)
		kind := "?"
		suffix := ""
		if regDir != "" {
			entry := filepath.Join(regDir, n+".yml")
			if info, err := os.Lstat(entry); err == nil {
				switch {
				case info.Mode()&os.ModeSymlink != 0:
					kind = "symlink"
				case info.Mode().IsRegular():
					kind = "drop-in"
				}
			} else {
				kind = "legacy"
			}
		}
		if _, err := os.Stat(path); err != nil {
			suffix = "  (target missing!)"
		}
		fmt.Fprintf(out, "  %-20s %-8s %s%s\n", n, kind, path, suffix)
	}
	if cwd, err := os.Getwd(); err == nil {
		if local, _ := project.FindManifest(cwd); local != "" {
			fmt.Fprintf(out, "\ncurrent directory manifest: %s\n", local)
		}
	}
	return nil
}

// ProjectForget removes a project from the registry.
func ProjectForget(name string, out io.Writer) error {
	reg, err := project.LoadRegistry()
	if err != nil {
		return err
	}
	if reg.Lookup(name) == "" {
		fmt.Fprintf(out, "project %q was not registered\n", name)
		return nil
	}
	if err := reg.Forget(name); err != nil {
		return err
	}
	fmt.Fprintf(out, "forgot project %q\n", name)
	return nil
}
