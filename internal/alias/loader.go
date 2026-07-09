package alias

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader resolves @site.env references into Alias values by reading
// dconsole YAML alias files from a search path plus, optionally, project
// manifests registered via ~/.dconsole/projects.yml.
type Loader struct {
	// SearchPaths in priority order. First match wins.
	SearchPaths []string
	// ProjectLookup, if set, is consulted when no *.site.yml file matches
	// a site name. Returns an alias and ok=true if the project registry
	// has an entry for `site` whose env `env` resolves. Wired in main.go
	// to break the import cycle between alias and project.
	ProjectLookup func(site, env string) (*Alias, bool, error)
	// ProjectEnvsLookup returns the envs declared by a registered project.
	ProjectEnvsLookup func(site string) ([]string, bool, error)
	// ProjectNames returns site names that exist as registered projects.
	ProjectNames func() []string
}

// NewLoader returns a Loader populated with the default search path:
//   1. $PWD/dconsole/sites/
//   2. $XDG_CONFIG_HOME/dconsole/sites/  (or ~/.config/dconsole/sites/)
//   3. ~/.dconsole/sites/
func NewLoader() *Loader {
	var paths []string
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, "dconsole", "sites"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "dconsole", "sites"))
	} else if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "dconsole", "sites"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".dconsole", "sites"))
	}
	return &Loader{SearchPaths: paths}
}

// ParseRef splits "@site.env" or "@self.env" into (site, env, true) or
// returns (_, _, false) if the token isn't an alias reference.
func ParseRef(token string) (site, env string, ok bool) {
	if !strings.HasPrefix(token, "@") {
		return "", "", false
	}
	parts := strings.SplitN(token[1:], ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ParseShortRef returns (env, true) if token is "@<env>" with no dot,
// i.e. the user said `@prod` instead of `@site.prod`. The caller is
// expected to fill in the site from cwd's project manifest.
func ParseShortRef(token string) (env string, ok bool) {
	if !strings.HasPrefix(token, "@") {
		return "", false
	}
	rest := token[1:]
	if rest == "" || strings.Contains(rest, ".") {
		return "", false
	}
	return rest, true
}

// Resolve loads "@site.env" from the first matching site file, falling
// through to ProjectLookup if no file matched.
func (l *Loader) Resolve(site, env string) (*Alias, error) {
	filename := site + ".site.yml"
	for _, dir := range l.SearchPaths {
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var file AliasFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		defaults, hasDefaults := file["_defaults"]
		a, ok := file[env]
		if !ok {
			available := availableEnvs(file)
			return nil, fmt.Errorf("env %q not found in %s (have: %s)", env, path, strings.Join(available, ", "))
		}
		if hasDefaults {
			a = mergeDefaults(defaults, a)
		}
		a.Site = site
		a.Env = env
		return &a, nil
	}
	if l.ProjectLookup != nil {
		a, ok, err := l.ProjectLookup(site, env)
		if err != nil {
			return nil, err
		}
		if ok {
			return a, nil
		}
	}
	return nil, fmt.Errorf("no alias file for site %q in %s (and not registered as a project)", site, strings.Join(l.SearchPaths, ":"))
}

// ResolveRef is the convenience wrapper for "@site.env".
func (l *Loader) ResolveRef(ref string) (*Alias, error) {
	site, env, ok := ParseRef(ref)
	if !ok {
		return nil, fmt.Errorf("invalid alias reference %q (expected @site.env)", ref)
	}
	return l.Resolve(site, env)
}

// ListEnvs returns the env keys (excluding _defaults) defined in the
// first site file that matches `site` on the search path. If
// ProjectEnvsLookup is set, it's used as a fallback for project-only
// sites.
func (l *Loader) ListEnvs(site string) ([]string, error) {
	filename := site + ".site.yml"
	for _, dir := range l.SearchPaths {
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var file AliasFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, err
		}
		return availableEnvs(file), nil
	}
	if l.ProjectEnvsLookup != nil {
		envs, ok, err := l.ProjectEnvsLookup(site)
		if err != nil {
			return nil, err
		}
		if ok {
			return envs, nil
		}
	}
	return nil, fmt.Errorf("no alias file for site %q", site)
}

// ListSites returns the site names visible in the search path plus any
// registered projects.
func (l *Loader) ListSites() ([]string, error) {
	seen := map[string]struct{}{}
	var names []string
	for _, dir := range l.SearchPaths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".site.yml") {
				continue
			}
			site := strings.TrimSuffix(n, ".site.yml")
			if _, dup := seen[site]; dup {
				continue
			}
			seen[site] = struct{}{}
			names = append(names, site)
		}
	}
	if l.ProjectNames != nil {
		for _, p := range l.ProjectNames() {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			names = append(names, p)
		}
	}
	return names, nil
}

func availableEnvs(f AliasFile) []string {
	out := make([]string, 0, len(f))
	for k := range f {
		if k == "_defaults" {
			continue
		}
		out = append(out, k)
	}
	SortByLifecycle(out)
	return out
}

// MergeDefaults applies defaults beneath a, only filling fields a left
// empty. Pointers/slices/maps in a take precedence whenever non-nil and
// non-empty. Exported so the project package can reuse it for manifest
// _defaults blocks.
func MergeDefaults(defaults, a Alias) Alias {
	if a.URI == "" {
		a.URI = defaults.URI
	}
	if a.Root == "" {
		a.Root = defaults.Root
	}
	if a.Bin.Kind == "" {
		a.Bin.Kind = defaults.Bin.Kind
	}
	if a.Bin.Path == "" {
		a.Bin.Path = defaults.Bin.Path
	}
	// The new Handler/Handlers schema inherits from defaults the same
	// way the legacy Transport did — but only when the env leaves BOTH
	// forms empty. If the env explicitly chose one shape, we don't
	// smuggle the other in from defaults (would surface later as a
	// "mixes handler: and handlers:" error from LegacyChain).
	if a.Handler.Type == "" && len(a.Handlers) == 0 {
		if defaults.Handler.Type != "" {
			a.Handler = defaults.Handler
		} else if len(defaults.Handlers) > 0 {
			a.Handlers = defaults.Handlers
		}
	}
	if a.Transport.Type == "" {
		a.Transport = defaults.Transport
	}
	if a.Provider.Type == "" {
		a.Provider = defaults.Provider
	}
	if a.SQL.Source.Type == "" {
		a.SQL.Source = defaults.SQL.Source
	}
	if a.SQL.Target.Type == "" {
		a.SQL.Target = defaults.SQL.Target
	}
	if len(a.EnvVars) == 0 {
		a.EnvVars = defaults.EnvVars
	}
	return a
}

func mergeDefaults(defaults, a Alias) Alias { return MergeDefaults(defaults, a) }
