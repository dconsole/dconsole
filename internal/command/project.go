package command

import (
	"fmt"
	"io"
	"os"

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
		m, err := project.LoadManifest(path)
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
		m, err := project.LoadManifest(path)
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

// ProjectRegister registers the dconsole.yml found at-or-above cwd.
func ProjectRegister(out io.Writer) error {
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
	fmt.Fprintf(out, "registered project %q → %s\n", m.Project, path)
	return nil
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
		fmt.Fprintln(out, "(no projects registered — run `dconsole project:register` inside a project)")
	}
	for _, n := range names {
		path := reg.Lookup(n)
		marker := ""
		if _, err := os.Stat(path); err != nil {
			marker = "  (missing!)"
		}
		fmt.Fprintf(out, "  %-20s %s%s\n", n, path, marker)
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
