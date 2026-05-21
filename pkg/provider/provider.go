// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/dconsole/dconsole/internal/alias"
)

// ErrNotSupported is returned by provider methods that aren't
// implemented for that provider. Callers should fall through to the
// generic transport-based path.
var ErrNotSupported = errors.New("not supported by this provider")

// Provider integrates a hosting service with dconsole's sync
// orchestration. All methods may return ErrNotSupported; the
// orchestrator then falls back to generic transport-based paths.
type Provider interface {
	Name() string
	// SyncTo takes over the entire sql:sync end-to-end. Hosting
	// integrations whose preferred model isn't a SQL dump (e.g. Skpr,
	// which publishes a nightly Docker image you pull + rebuild
	// locally) implement this to short-circuit the dump+load chain.
	// Providers that just supply dumps return ErrNotSupported and
	// dconsole falls back to DumpFor / LoadFor.
	SyncTo(ctx context.Context, source, target *alias.Alias) error
	// SyncFilesTo is the rsync-equivalent of SyncTo: take over the
	// entire `dconsole rsync @src @dst` end-to-end. Providers whose
	// image already contains assets (Skpr) return nil; providers that
	// only ship asset bundles return ErrNotSupported (then dconsole
	// falls back to FilesDownload + LoadFilesFor).
	SyncFilesTo(ctx context.Context, source, target *alias.Alias) error
	// LoadFilesFor consumes a local asset bundle (tar.gz directory)
	// and loads it into `a`. Most providers won't implement this;
	// dconsole falls back to transport.FilesImporter (ddev import-files)
	// or a tar-stream unpack via the target transport.
	LoadFilesFor(ctx context.Context, a *alias.Alias, bundlePath string) error
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

// LoginCapable is an optional duck-typed capability. Providers (and
// transports) that need an auth step implement it; the auth command
// invokes Login on each.
type LoginCapable interface {
	Login(ctx context.Context, a *alias.Alias) error
}

// Factory builds a Provider from the alias's provider block.
type Factory func(a *alias.Alias) (Provider, error)

// Registration bundles factory + metadata for a provider type. Mirrors
// transport.Registration. RequiredCLI is the binary on $PATH this
// provider depends on (or "" for none) — declared at registration so
// dconsole can report missing prerequisites without instantiating.
type Registration struct {
	Build       Factory
	RequiredCLI string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Registration{}
)

// Register adds a provider type to the global registry.
func Register(name string, reg Registration) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = reg
}

// Lookup returns the registration for name (and whether it exists).
func Lookup(name string) (Registration, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	r, ok := registry[name]
	return r, ok
}

// Names returns registered provider names sorted alphabetically.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// For returns the Provider for an alias, or (nil, nil) if the alias has
// no provider attached. Unknown types fall through to the installed
// unknown-type handler (the subprocess-plugin runner).
func For(a *alias.Alias) (Provider, error) {
	t := a.Provider.Type
	if t == "" {
		return nil, nil
	}
	if r, ok := Lookup(t); ok {
		return r.Build(a)
	}
	if h := unknownHandler(); h != nil {
		return h(a)
	}
	return nil, fmt.Errorf("unknown provider type %q on @%s.%s", t, a.Site, a.Env)
}

// SetUnknownTypeHandler installs a fallback Factory invoked by For when
// the provider type isn't in the in-tree registry. dconsole's binary
// wires this to the subprocess-plugin runner.
func SetUnknownTypeHandler(h Factory) {
	unknownHandlerMu.Lock()
	defer unknownHandlerMu.Unlock()
	unknownHandlerFn = h
}

var (
	unknownHandlerMu sync.RWMutex
	unknownHandlerFn Factory
)

func unknownHandler() Factory {
	unknownHandlerMu.RLock()
	defer unknownHandlerMu.RUnlock()
	return unknownHandlerFn
}
