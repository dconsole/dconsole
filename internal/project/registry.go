package project

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// pointerKey is the YAML key a pointer file uses to redirect to the
// real manifest. The pointer file is the Windows-safe equivalent of a
// symlink: Windows requires admin rights or dev-mode to create one,
// but any user can write a plain file. A pointer file is a valid YAML
// document with just this key — it works equally well on every
// platform, so Register uses it unconditionally on Windows and users
// on Unix see it occasionally too (e.g. restoring a cross-platform
// backup).
const pointerKey = "_dconsole_pointer"

// pointerFile is the on-disk shape of a pointer entry. The whole file
// is one line of YAML. Content kept minimal — the registry carries
// just the redirect, no project metadata.
type pointerFile struct {
	Pointer string `yaml:"_dconsole_pointer"`
}

// resolveRegistryEntry returns the absolute path a registry entry
// resolves to. Three shapes are supported:
//
//   - **Symlink** — follow via Readlink; return the absolute target.
//     Unix default. On Windows, created by users with dev-mode on.
//   - **Pointer file** — a regular YAML file whose only key is
//     `_dconsole_pointer: /abs/path`. Return that path. Windows
//     default (where symlinks need admin).
//   - **Drop-in file** — any other regular file. Return the entry's
//     own path (the file IS the manifest; no local checkout exists).
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
	// Regular file: could be a pointer or a drop-in. Pointer-ness
	// is cheap to detect — read the file (registry entries are tiny)
	// and look for the sentinel key.
	if target := readPointerTarget(entryPath); target != "" {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(entryPath), target)
		}
		return filepath.Clean(target), nil
	}
	return filepath.Clean(entryPath), nil
}

// IsPointerEntry reports whether a registry entry at entryPath is a
// pointer file (as opposed to a true drop-in manifest). Used by
// project:list to label the entry kind. Returns false for anything
// that isn't a regular file whose contents parse as a pointer.
func IsPointerEntry(entryPath string) bool {
	return readPointerTarget(entryPath) != ""
}

// readPointerTarget returns the pointer target if entryPath is a
// pointer file, or "" if it's a drop-in or unreadable. Read errors
// are swallowed — caller falls back to treating the file as a drop-in,
// which will surface a parse error at manifest-load time with better
// context.
func readPointerTarget(entryPath string) string {
	data, err := os.ReadFile(entryPath)
	if err != nil {
		return ""
	}
	// Quick substring guard — avoids a YAML parse on every registry
	// entry when the pointer key isn't present.
	if !bytes.Contains(data, []byte(pointerKey)) {
		return ""
	}
	var p pointerFile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return ""
	}
	return p.Pointer
}

// writePointerFile creates a pointer file at entryPath redirecting to
// target. Used as the Windows fallback when os.Symlink fails (and
// unconditionally on Windows to avoid the privilege-required attempt).
func writePointerFile(entryPath, target string) error {
	data, err := yaml.Marshal(pointerFile{Pointer: target})
	if err != nil {
		return err
	}
	// Prepend a short comment so humans peeking at the file see what
	// it is without having to recognise the key name.
	content := []byte("# dconsole project registry pointer (Windows-safe symlink equivalent)\n")
	content = append(content, data...)
	return os.WriteFile(entryPath, content, 0o600)
}

// writeRegistryEntry creates the on-disk entry at entryPath pointing
// at target. On Unix it tries os.Symlink first (the native, zero-cost
// form); on failure it falls back to a pointer file. On Windows it
// writes a pointer file unconditionally — symlinks there need admin
// rights or developer mode, and quietly degrading to a pointer file
// avoids a confusing "operation not permitted" that most users can't
// resolve. forcePointerForTest is a test knob so pointer-write code
// paths get exercised on Unix CI.
func writeRegistryEntry(entryPath, target string) error {
	if runtime.GOOS == "windows" || forcePointerForTest {
		return writePointerFile(entryPath, target)
	}
	if err := os.Symlink(target, entryPath); err == nil {
		return nil
	}
	// Last-ditch fallback — some mounted filesystems forbid symlinks
	// even on Unix (SMB, certain Docker-mounted volumes).
	return writePointerFile(entryPath, target)
}

// forcePointerForTest, when true, makes writeRegistryEntry skip the
// symlink attempt and always write a pointer file. Used by tests on
// Unix to cover the Windows code path.
var forcePointerForTest bool

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

// Register adds or updates an entry at ~/.dconsole/projects/<name>.yml
// pointing at manifestPath. The on-disk form depends on the platform:
//
//   - Unix: a symlink. If os.Symlink fails for some unexpected reason,
//     falls back to a pointer file.
//   - Windows: a pointer file. os.Symlink there needs admin rights or
//     dev-mode, so we skip the attempt and write a pointer file
//     unconditionally — users don't want to re-run as admin for a
//     local-only bookkeeping call.
//
// Either way the LOGICAL behaviour is identical: Lookup returns the
// manifest's absolute path and dconsole proceeds as if the manifest
// lived there directly. The pointer file is simply the Windows-safe
// equivalent of a symlink.
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
	// Remove any existing entry (symlink, pointer file, or drop-in)
	// before writing the new one. os.Symlink refuses to overwrite, and
	// a stale pointer could silently redirect.
	if _, err := os.Lstat(entryPath); err == nil {
		if err := os.Remove(entryPath); err != nil {
			return err
		}
	}
	if err := writeRegistryEntry(entryPath, abs); err != nil {
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
