// Package project handles project-scoped configuration: the dconsole.yml
// manifest at a repo root, and a registry under ~/.dconsole/projects.yml
// that maps a project name to its manifest location so dconsole can be
// invoked from anywhere on the system.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/heydon/dconsole/internal/alias"
)

// ManifestName is the filename dconsole looks for at a repo root.
const ManifestName = "dconsole.yml"

// OverrideManifestName is the optional file that layers field-by-field
// changes on top of the base manifest. Useful when dconsole is deployed
// to a server or inside a container: a tiny override can change
// default_env and switch one env's transport to `exec` without forking
// the committed dconsole.yml.
const OverrideManifestName = "dconsole.override.yml"

// Manifest is the parsed dconsole.yml file. It declares a project name
// and the envs that belong to it. The YAML format is flat: `project:`
// names the project, `_defaults:` optionally provides per-env defaults,
// `default_env:` (optional) names the env used when the user runs
// `dconsole <cmd>` (no alias prefix) inside the project, and every other
// top-level key is an env name (dev, stage, prod, …).
type Manifest struct {
	Project    string
	DefaultEnv string
	Defaults   alias.Alias
	Envs       map[string]alias.Alias

	// AbsPath is the absolute path to the dconsole.yml the manifest was
	// loaded from. Populated by LoadManifest; not part of the YAML.
	AbsPath string
	// OverridePath is the absolute path of the dconsole.override.yml
	// that was layered on top, or "" if no override existed.
	OverridePath string
}

// LoadManifest reads and parses a dconsole.yml at `path`. If a
// dconsole.override.yml sits next to it, that file is layered on top
// field-by-field — useful for per-machine / per-deployment config that
// shouldn't be committed.
func LoadManifest(path string) (*Manifest, error) {
	m, err := loadManifestFile(path, manifestLoadOpts{requireEnvs: true, inferProject: true})
	if err != nil {
		return nil, err
	}
	overridePath := filepath.Join(filepath.Dir(m.AbsPath), OverrideManifestName)
	if _, statErr := os.Stat(overridePath); statErr == nil {
		ov, err := loadManifestFile(overridePath, manifestLoadOpts{requireEnvs: false, inferProject: false})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", overridePath, err)
		}
		mergeOverride(m, ov)
		m.OverridePath = ov.AbsPath
	}
	return m, nil
}

// manifestLoadOpts controls relaxed-mode parsing for override files
// (which legitimately omit project: and envs:).
type manifestLoadOpts struct {
	requireEnvs  bool
	inferProject bool
}

// loadManifestFile parses one YAML file into a Manifest. Used for both
// the base manifest (strict) and the override (relaxed).
func loadManifestFile(path string, opts manifestLoadOpts) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Two-pass: first unmarshal into a generic map to peel off the
	// special keys, then re-marshal the env subset for typed parsing.
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	m := &Manifest{Envs: map[string]alias.Alias{}}
	if node, ok := raw["project"]; ok {
		if err := node.Decode(&m.Project); err != nil {
			return nil, fmt.Errorf("%s: project: %w", path, err)
		}
	}
	if m.Project == "" && opts.inferProject {
		// Default to the manifest's parent directory name. Mirrors how
		// npm / composer / cargo default a project name when the file
		// lives in a directory named after the project — avoids a
		// confusing "missing required `project:` key" error when the
		// generated header has commented the line out.
		absForName, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		m.Project = filepath.Base(filepath.Dir(absForName))
		if m.Project == "" || m.Project == "." || m.Project == "/" {
			return nil, fmt.Errorf("%s: missing `project:` key and could not infer one from the directory", path)
		}
	}
	if node, ok := raw["_defaults"]; ok {
		if err := node.Decode(&m.Defaults); err != nil {
			return nil, fmt.Errorf("%s: _defaults: %w", path, err)
		}
	}
	if node, ok := raw["default_env"]; ok {
		if err := node.Decode(&m.DefaultEnv); err != nil {
			return nil, fmt.Errorf("%s: default_env: %w", path, err)
		}
	}
	for k, node := range raw {
		if k == "project" || k == "_defaults" || k == "default_env" {
			continue
		}
		var a alias.Alias
		if err := node.Decode(&a); err != nil {
			return nil, fmt.Errorf("%s: env %q: %w", path, k, err)
		}
		m.Envs[k] = a
	}
	if opts.requireEnvs && len(m.Envs) == 0 {
		return nil, fmt.Errorf("%s: no envs declared (every top-level key besides `project:` and `_defaults:` is treated as an env name)", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	m.AbsPath = abs
	return m, nil
}

// mergeOverride layers ov's non-empty fields on top of base in place:
//
//   - default_env wins from ov if set.
//   - _defaults merges field-by-field via alias.MergeDefaults.
//   - For each env that exists in both, ov's values override base's
//     per field (same MergeDefaults semantics).
//   - Envs only present in ov are added verbatim.
//
// project is intentionally NOT overridden — the base file owns the
// project's identity. If callers really need to rename the project at
// the override layer they can edit dconsole.yml directly.
func mergeOverride(base, ov *Manifest) {
	if ov.DefaultEnv != "" {
		base.DefaultEnv = ov.DefaultEnv
	}
	// alias.MergeDefaults(defaults, a) returns a with defaults filled in
	// where a was empty — i.e. a wins where it has a value. For the
	// override we want ov to win where set, with base as the fallback,
	// so we call MergeDefaults(base, ov).
	base.Defaults = alias.MergeDefaults(base.Defaults, ov.Defaults)
	for name, ovEnv := range ov.Envs {
		if existing, ok := base.Envs[name]; ok {
			base.Envs[name] = alias.MergeDefaults(existing, ovEnv)
		} else {
			base.Envs[name] = ovEnv
		}
	}
}

// FindManifest walks up from `start` looking for a dconsole.yml. Returns
// the path (or "" if none found). Stops at filesystem root.
func FindManifest(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, ManifestName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// ResolveEnv looks up an env in the manifest, applying the optional
// _defaults block.
func (m *Manifest) ResolveEnv(envName string) (*alias.Alias, error) {
	a, ok := m.Envs[envName]
	if !ok {
		envs := make([]string, 0, len(m.Envs))
		for k := range m.Envs {
			envs = append(envs, k)
		}
		alias.SortByLifecycle(envs)
		return nil, fmt.Errorf("env %q not found in %s (have: %s)", envName, m.AbsPath, strings.Join(envs, ", "))
	}
	a = alias.MergeDefaults(m.Defaults, a)
	a.Site = m.Project
	a.Env = envName
	return &a, nil
}

// EnvNames returns the env keys in lifecycle order.
func (m *Manifest) EnvNames() []string {
	envs := make([]string, 0, len(m.Envs))
	for k := range m.Envs {
		envs = append(envs, k)
	}
	alias.SortByLifecycle(envs)
	return envs
}
