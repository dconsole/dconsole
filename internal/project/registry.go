package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// Registry maps project names to the absolute path of their dconsole.yml.
// Persisted at ~/.dconsole/projects.yml so `dconsole @project.env` works
// from anywhere on the system.
type Registry struct {
	Projects map[string]string `yaml:"projects"`
}

// DefaultRegistryPath returns ~/.dconsole/projects.yml. Returns "" if the
// home directory can't be resolved.
func DefaultRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dconsole", "projects.yml")
}

var (
	registryMu     sync.Mutex
	cachedRegistry *Registry
)

// LoadRegistry reads the persisted registry. Returns an empty registry
// (never nil) if the file doesn't exist yet.
func LoadRegistry() (*Registry, error) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if cachedRegistry != nil {
		return cachedRegistry, nil
	}
	r := &Registry{Projects: map[string]string{}}
	path := DefaultRegistryPath()
	if path == "" {
		cachedRegistry = r
		return r, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cachedRegistry = r
			return r, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if r.Projects == nil {
		r.Projects = map[string]string{}
	}
	cachedRegistry = r
	return r, nil
}

// Save persists the registry to ~/.dconsole/projects.yml.
func (r *Registry) Save() error {
	path := DefaultRegistryPath()
	if path == "" {
		return fmt.Errorf("can't resolve home directory for registry")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Register adds or updates an entry. If the project name already maps to
// a different existing path, returns a conflict error so the user can
// rename one side.
func (r *Registry) Register(name, manifestPath string) error {
	if r.Projects == nil {
		r.Projects = map[string]string{}
	}
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return err
	}
	if existing, ok := r.Projects[name]; ok && existing != abs {
		if _, err := os.Stat(existing); err == nil {
			return fmt.Errorf("project %q is already registered at %s — rename one or `dconsole project:forget %s` first", name, existing, name)
		}
	}
	r.Projects[name] = abs
	return r.Save()
}

// Forget removes a project from the registry. No-op if not present.
func (r *Registry) Forget(name string) error {
	if _, ok := r.Projects[name]; !ok {
		return nil
	}
	delete(r.Projects, name)
	return r.Save()
}

// Lookup returns the manifest path for a project name, or "" if not
// registered.
func (r *Registry) Lookup(name string) string {
	return r.Projects[name]
}

// Names returns the registered project names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.Projects))
	for n := range r.Projects {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// AutoRegisterCwd scans up from the cwd for a dconsole.yml and, if it
// finds one whose project name is not already registered to a different
// existing path, adds it to the registry. Returns the project name (or
// "" if nothing was registered) and the manifest path it found.
func AutoRegisterCwd() (name, manifestPath string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	path, err := FindManifest(cwd)
	if err != nil || path == "" {
		return "", "", err
	}
	m, err := LoadManifest(path)
	if err != nil {
		return "", path, err
	}
	r, err := LoadRegistry()
	if err != nil {
		return "", path, err
	}
	// Skip auto-register if it would conflict with another live entry;
	// only register fresh or self-update entries.
	if existing, ok := r.Projects[m.Project]; ok && existing != path {
		if _, statErr := os.Stat(existing); statErr == nil {
			return "", path, nil
		}
	}
	if err := r.Register(m.Project, path); err != nil {
		return "", path, err
	}
	return m.Project, path, nil
}
