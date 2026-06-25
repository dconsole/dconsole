package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/project"
)

// Resolution is the outcome of ResolveContextual: an alias plus the args
// left over after consuming any leading @ref token.
type Resolution struct {
	Alias *alias.Alias
	// Rest is the args slice with any leading alias token stripped off.
	Rest []string
	// Source explains where the alias came from (for user-facing messages).
	Source string
}

// ResolveContextual picks the right alias for an invocation, in priority:
//
//  1. `@site.env` — explicit full reference (current behavior)
//  2. `@env`      — short reference, site inferred from cwd's dconsole.yml
//  3. otherwise   — no alias prefix, use the project's default_env (if a
//                   dconsole.yml exists in cwd or any parent) or fall back
//                   to a synthetic "local" alias rooted at cwd (exec
//                   transport + local drush)
//
// Returns an error if a project manifest is found but doesn't declare
// a default_env, since silently picking one would be surprising.
func ResolveContextual(loader *alias.Loader, args []string) (*Resolution, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no arguments")
	}

	// Form 0: inline URI (@<scheme>://…). MUST be checked before
	// ParseRef because a host like "epp.heydon.io" contains a dot
	// and would otherwise be misclassified as "site.env".
	if alias.IsInlineURIRef(args[0]) {
		a, err := alias.ParseInline(args[0])
		if err != nil {
			return nil, fmt.Errorf("parse inline URI: %w", err)
		}
		return &Resolution{Alias: a, Rest: args[1:], Source: args[0]}, nil
	}

	// Form 1: @site.env. We try the local project manifest first so an
	// unregistered local project resolves without `project:register`.
	if site, env, ok := alias.ParseRef(args[0]); ok {
		if m, err := findProjectManifest(); err == nil && m != nil && m.Project == site {
			a, err := m.ResolveEnv(env)
			if err != nil {
				return nil, err
			}
			return &Resolution{Alias: a, Rest: args[1:], Source: "@" + site + "." + env}, nil
		}
		a, err := loader.Resolve(site, env)
		if err != nil {
			return nil, err
		}
		return &Resolution{Alias: a, Rest: args[1:], Source: "@" + site + "." + env}, nil
	}

	// Form 2: @env (site inferred from cwd's project manifest)
	if env, ok := alias.ParseShortRef(args[0]); ok {
		m, err := findProjectManifest()
		if err != nil {
			return nil, err
		}
		justGenerated := false
		if m == nil {
			// No manifest — offer to generate one if we can.
			if written, gerr := OfferGenerateIfMissing(os.Stdin, os.Stderr); gerr != nil {
				return nil, gerr
			} else if written != "" {
				m2, err := project.LoadManifest(written)
				if err != nil {
					return nil, err
				}
				m = m2
				justGenerated = true
			}
		}
		if m == nil {
			return nil, fmt.Errorf("@%s used without a site, but no dconsole.yml found in cwd or any parent — write `@<site>.%s` or run from a project root", env, env)
		}
		a, err := m.ResolveEnv(env)
		if err == nil {
			return &Resolution{Alias: a, Rest: args[1:], Source: fmt.Sprintf("@%s.%s (site inferred from %s)", m.Project, env, m.AbsPath)}, nil
		}
		// The requested env doesn't exist. If we JUST generated the
		// manifest (the user was prompted mid-command), it's nicer to
		// continue the command against default_env than to bail out.
		if justGenerated && m.DefaultEnv != "" {
			fb, fErr := m.ResolveEnv(m.DefaultEnv)
			if fErr == nil {
				fmt.Fprintf(os.Stderr, "note: env %q not in generated manifest (have: %s) — running against default_env @%s.%s instead\n",
					env, strings.Join(m.EnvNames(), ", "), m.Project, m.DefaultEnv)
				return &Resolution{Alias: fb, Rest: args[1:], Source: fmt.Sprintf("@%s.%s (fell back to default_env — requested @%s wasn't in the just-generated manifest)", m.Project, m.DefaultEnv, env)}, nil
			}
		}
		return nil, err
	}

	// Form 3: no alias prefix — default. Project default if available,
	// otherwise a synthetic local alias.
	m, err := findProjectManifest()
	if err != nil {
		return nil, err
	}
	if m != nil {
		if m.DefaultEnv == "" {
			return nil, fmt.Errorf("dconsole.yml at %s has no `default_env:` set; either add one or pass @env explicitly", m.AbsPath)
		}
		a, err := m.ResolveEnv(m.DefaultEnv)
		if err != nil {
			return nil, err
		}
		return &Resolution{Alias: a, Rest: args, Source: fmt.Sprintf("@%s.%s (project default from %s)", m.Project, m.DefaultEnv, m.AbsPath)}, nil
	}
	// No manifest. Offer to generate one (interactive) or hint
	// (non-interactive) before falling back to the synthetic local alias.
	if written, _ := OfferGenerateIfMissing(os.Stdin, os.Stderr); written != "" {
		m2, err := project.LoadManifest(written)
		if err != nil {
			return nil, err
		}
		if m2.DefaultEnv == "" {
			return nil, fmt.Errorf("generated dconsole.yml at %s has no default_env; edit it and re-run", written)
		}
		a, err := m2.ResolveEnv(m2.DefaultEnv)
		if err != nil {
			return nil, err
		}
		return &Resolution{Alias: a, Rest: args, Source: fmt.Sprintf("@%s.%s (project default from %s)", m2.Project, m2.DefaultEnv, written)}, nil
	}
	return &Resolution{Alias: localAlias(), Rest: args, Source: "local (cwd)"}, nil
}

func summariseSources(s []string) string {
	if len(s) == 0 {
		return "(none)"
	}
	if len(s) == 1 {
		return s[0]
	}
	return s[0] + " (+" + itoa(len(s)-1) + " more)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// findProjectManifest walks up from cwd looking for a dconsole.yml.
// Returns (nil, nil) if no manifest was found.
func findProjectManifest() (*project.Manifest, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	path, err := project.FindManifest(cwd)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	return project.LoadManifest(path)
}

// localAlias returns a synthetic alias representing "run drush locally in
// the current working directory." Used when the user is outside any
// dconsole-aware project and didn't specify an alias.
func localAlias() *alias.Alias {
	cwd, _ := os.Getwd()
	return &alias.Alias{
		Site:    "self",
		Env:     "local",
		Root:    cwd,
		Bin:     alias.RemoteBin{Kind: "auto", Path: filepath.Join(cwd, "vendor", "bin", "drush")},
		Handler: alias.NewHandler("exec", alias.ExecTransport{Dir: cwd}),
	}
}
