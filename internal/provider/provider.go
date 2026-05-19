// Package provider abstracts hosting-provider integrations (Ironstar,
// Pantheon, Acquia, Platform.sh, …) whose preferred data-movement story
// differs from the generic "drush sql:dump on source" approach — typically
// they offer pre-computed backups via their own CLI or API.
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/heydon/dconsole/internal/alias"
)

// ErrNotSupported is returned by provider methods that aren't implemented
// for that provider. Callers should fall through to the generic path.
var ErrNotSupported = errors.New("not supported by this provider")

// Provider integrates a hosting service with dconsole's sync orchestration.
// All methods may return ErrNotSupported; the orchestrator then falls
// back to generic transport-based paths.
type Provider interface {
	Name() string
	// DumpFor returns a local path to a database dump for `a`. The
	// returned cleanup function is called by the orchestrator when the
	// dump is no longer needed.
	DumpFor(ctx context.Context, a *alias.Alias) (dumpPath string, cleanup func(), err error)
	// LoadFor consumes a local dump and loads it into `a`. Most providers
	// won't implement this; dconsole falls back to the regular transport.
	LoadFor(ctx context.Context, a *alias.Alias, dumpPath string) error
	// FilesDownload pulls `a`'s files directory into `targetDir`.
	FilesDownload(ctx context.Context, a *alias.Alias, targetDir string) error
}

// Factory builds a Provider from the alias's provider block.
type Factory func(a *alias.Alias) (Provider, error)

var registry = map[string]Factory{}

// Register adds a Factory under a provider type name.
func Register(name string, f Factory) {
	registry[name] = f
}

// For returns the Provider for an alias, or (nil, nil) if the alias has
// no provider attached.
func For(a *alias.Alias) (Provider, error) {
	t := a.Provider.Type
	if t == "" {
		return nil, nil
	}
	f, ok := registry[t]
	if !ok {
		return nil, fmt.Errorf("unknown provider type %q on @%s.%s", t, a.Site, a.Env)
	}
	return f(a)
}
