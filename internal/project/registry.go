package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Registry maps project names to the absolute path of their manifest so
// `dconsole @project.env` works from anywhere on the system.
//
// Storage (v0.5.13+):
//
//	~/.dconsole/projects/<name>.yml     — either a symlink to the real
//	                                       .dconsole.yml inside a project
//	                                       checkout, OR a standalone
//	                                       regular file (no local source
//	                                       needed — one of dconsole's
//	                                       reasons to exist).
//	~/.dconsole/projects/<name>.override.yml
//	                                    — optional; used as a fallback
//	                                      override when the project has
//	                                      no sibling .dconsole.override.yml.
//
// Backward compat: entries in the legacy single-file
// ~/.dconsole/projects.yml are still read. Directory entries win on
// conflict. project:register writes to the new directory form; legacy
// entries stay put until the user forgets them.
type Registry struct {
	// Projects is the resolved name→abs-path map, unioned from both
	// storage forms with the directory taking precedence. Modifying
	// this map directly does NOT persist — use Register/Forget.
	Projects map[string]string `yaml:"projects"`

	// Directory-only view. Populated at load; used by Save().
	fromDir map[string]string `yaml:"-"`
	// Legacy-file-only view. Populated at load; used by Save() to
	// know what to write into the legacy file. Never gains new keys
	// after this release (all new registrations write to fromDir).
	fromLegacy map[string]string `yaml:"-"`
}

// DefaultRegistryDir returns ~/.dconsole/projects. Returns "" if the
// home directory can't be resolved.
func DefaultRegistryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dconsole", "projects")
}

// LegacyRegistryPath returns ~/.dconsole/projects.yml (the pre-v0.5.13
// single-file registry). Still read for backwards compat.
func LegacyRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dconsole", "projects.yml")
}

// DefaultRegistryPath — DEPRECATED. Kept for API compatibility with
// early v0.5.x callers; returns LegacyRegistryPath.
func DefaultRegistryPath() string { return LegacyRegistryPath() }

var (
	registryMu     sync.Mutex
	cachedRegistry *Registry
)

// LoadRegistry reads the persisted registry. Returns an empty registry
// (never nil) if neither storage form exists yet.
func LoadRegistry() (*Registry, error) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if cachedRegistry != nil {
		return cachedRegistry, nil
	}
	r := &Registry{
		Projects:   map[string]string{},
		fromDir:    map[string]string{},
		fromLegacy: map[string]string{},
	}
	// Legacy file first — directory entries will overwrite on conflict.
	if legacyPath := LegacyRegistryPath(); legacyPath != "" {
		if data, err := os.ReadFile(legacyPath); err == nil {
			var legacy struct {
				Projects map[string]string `yaml:"projects"`
			}
			if err := yaml.Unmarshal(data, &legacy); err != nil {
				return nil, fmt.Errorf("parse %s: %w", legacyPath, err)
			}
			for name, path := range legacy.Projects {
				r.fromLegacy[name] = path
				r.Projects[name] = path
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	// Directory entries second — win on conflict.
	if dir := DefaultRegistryDir(); dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			name, kind := parseRegistryEntry(e.Name())
			if kind != registryEntryManifest {
				continue
			}
			resolved, err := resolveRegistryEntry(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("registry entry %s: %w", e.Name(), err)
			}
			r.fromDir[name] = resolved
			r.Projects[name] = resolved
		}
	}
	cachedRegistry = r
	return r, nil
}

// registryEntryKind classifies a filename in ~/.dconsole/projects.
type registryEntryKind int

const (
	registryEntryUnknown registryEntryKind = iota
	registryEntryManifest
	registryEntryOverride
)

// parseRegistryEntry returns the project name and entry kind for a
// filename in ~/.dconsole/projects. Names starting with "." are
// ignored (macOS .DS_Store, editor turds).
func parseRegistryEntry(filename string) (name string, kind registryEntryKind) {
	if strings.HasPrefix(filename, ".") {
		return "", registryEntryUnknown
	}
	switch {
	case strings.HasSuffix(filename, ".override.yml"):
		return strings.TrimSuffix(filename, ".override.yml"), registryEntryOverride
	case strings.HasSuffix(filename, ".yml"):
		return strings.TrimSuffix(filename, ".yml"), registryEntryManifest
	}
	return "", registryEntryUnknown
}

// resolveRegistryEntry returns the absolute path a registry entry
// resolves to. For symlinks, follows one level (Readlink) and returns
// the absolute target. For regular files, returns the entry's own
// absolute path (drop-in workflow — the file IS the manifest).
func resolveRegistryEntry(entryPath string) (string, error) {
	info, err := os.Lstat(entryPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(entryPath)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			// Resolve relative to the entry's directory so relative
			// symlinks (rare, but valid) still point somewhere useful.
			target = filepath.Join(filepath.Dir(entryPath), target)
		}
		return filepath.Clean(target), nil
	}
	return filepath.Clean(entryPath), nil
}

// LookupOverride returns the absolute path of the registry-side
// override for name (~/.dconsole/projects/<name>.override.yml) if it
// exists, or "" otherwise. Used by LoadManifest as a fallback when
// no project-side .dconsole.override.yml sits next to the manifest.
func LookupOverride(name string) string {
	dir := DefaultRegistryDir()
	if dir == "" {
		return ""
	}
	p := filepath.Join(dir, name+".override.yml")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// Save persists the registry. Directory entries are created/updated
// as symlinks; the legacy file is REWRITTEN with only its untouched
// entries (never gains new ones — those go into the directory).
func (r *Registry) Save() error {
	dir := DefaultRegistryDir()
	if dir == "" {
		return fmt.Errorf("can't resolve home directory for registry")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Directory writes are handled by Register/Forget directly (they
	// mutate one entry per call). Save() only exists to update the
	// legacy file after a legacy entry is Forget()'d — new
	// registrations never touch the legacy file.
	if len(r.fromLegacy) == 0 {
		if legacyPath := LegacyRegistryPath(); legacyPath != "" {
			if _, err := os.Stat(legacyPath); err == nil {
				return os.Remove(legacyPath)
			}
		}
		return nil
	}
	if legacyPath := LegacyRegistryPath(); legacyPath != "" {
		if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
			return err
		}
		data, err := yaml.Marshal(struct {
			Projects map[string]string `yaml:"projects"`
		}{Projects: r.fromLegacy})
		if err != nil {
			return err
		}
		return os.WriteFile(legacyPath, data, 0o600)
	}
	return nil
}

// Register adds or updates an entry as a symlink at
// ~/.dconsole/projects/<name>.yml → manifestPath.
//
// If an existing entry already points to a DIFFERENT existing manifest,
// refuse — the user should Forget one side first or rename. Existing
// entries pointing to nowhere (dangling symlinks, deleted checkouts)
// are silently replaced.
func (r *Registry) Register(name, manifestPath string) error {
	if r.Projects == nil {
		r.Projects = map[string]string{}
	}
	if r.fromDir == nil {
		r.fromDir = map[string]string{}
	}
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return err
	}
	// Guard against pointing at ourselves — a symlink inside
	// ~/.dconsole/projects/ pointing back into the same directory
	// would confuse the loader.
	dir := DefaultRegistryDir()
	if dir == "" {
		return fmt.Errorf("can't resolve home directory for registry")
	}
	if filepath.Dir(abs) == dir {
		return fmt.Errorf("refusing to register a manifest that already lives inside %s (nothing to symlink to)", dir)
	}
	// Conflict check against BOTH storage forms.
	if existing, ok := r.Projects[name]; ok && existing != abs {
		if _, err := os.Stat(existing); err == nil {
			return fmt.Errorf("project %q is already registered at %s — rename one or `dconsole project:forget %s` first", name, existing, name)
		}
		// Existing entry is dangling — safe to replace.
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entryPath := filepath.Join(dir, name+".yml")
	// Remove any existing entry (symlink or regular file) before
	// creating the new symlink. os.Symlink refuses to overwrite.
	if _, err := os.Lstat(entryPath); err == nil {
		if err := os.Remove(entryPath); err != nil {
			return err
		}
	}
	if err := os.Symlink(abs, entryPath); err != nil {
		return err
	}
	r.fromDir[name] = abs
	r.Projects[name] = abs
	// If the legacy file also carried this name, drop it there — the
	// directory entry is the source of truth now.
	if _, hadLegacy := r.fromLegacy[name]; hadLegacy {
		delete(r.fromLegacy, name)
		if err := r.Save(); err != nil {
			return err
		}
	}
	return nil
}

// Forget removes a project from the registry. No-op if not present.
// Removes both the directory entry (if any) and the legacy-file entry
// (if any). The optional <name>.override.yml is LEFT in place — the
// user might want to keep it for a later re-register.
func (r *Registry) Forget(name string) error {
	if _, inDir := r.fromDir[name]; inDir {
		dir := DefaultRegistryDir()
		if dir != "" {
			entryPath := filepath.Join(dir, name+".yml")
			if err := os.Remove(entryPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		delete(r.fromDir, name)
	}
	_, inLegacy := r.fromLegacy[name]
	if inLegacy {
		delete(r.fromLegacy, name)
	}
	delete(r.Projects, name)
	if inLegacy {
		return r.Save()
	}
	return nil
}

// Lookup returns the manifest path for a project name, or "" if not
// registered. Directory entries win over legacy-file entries.
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

// AutoRegisterCwd scans up from the cwd for a .dconsole.yml (or the
// legacy dconsole.yml) and, if it finds one whose project name is not
// already registered to a different existing path, adds it to the
// registry. Returns the project name (or "" if nothing was registered)
// and the manifest path it found.
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
