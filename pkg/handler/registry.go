// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"fmt"
	"os/exec"
	"sort"
	"sync"

	"github.com/dconsole/dconsole/internal/alias"
)

// Factory builds a Handler from the alias's handler block. Factories
// should call the relevant Decode helper to pull their typed config
// out of the opaque YAML node.
type Factory func(a *alias.Alias) (Handler, error)

// Registration is the metadata + factory bundle dconsole stores per
// handler type. RequiredCLI is the binary on $PATH this handler
// depends on (or "" for none) — declared at registration so dconsole
// can answer "is the ssh handler usable?" without instantiating one.
type Registration struct {
	Build       Factory
	RequiredCLI string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Registration{}
)

// Register adds a handler type to the global registry. In-tree
// handlers call this from init(); the subprocess-plugin runner calls
// this lazily on first use of a plugin.
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

// Names returns registered handler names sorted alphabetically.
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

// SetUnknownTypeHandler installs a fallback Factory invoked when the
// requested handler type isn't in the in-tree registry. dconsole's
// binary wires this to the subprocess-plugin runner.
func SetUnknownTypeHandler(h Factory) {
	unknownHandlerMu.Lock()
	defer unknownHandlerMu.Unlock()
	unknownHandlerFn = h
}

// UnknownHandler returns the installed unknown-type fallback (or nil).
// Used by For-style helpers built on top of this registry.
func UnknownHandler() Factory {
	unknownHandlerMu.RLock()
	defer unknownHandlerMu.RUnlock()
	return unknownHandlerFn
}

var (
	unknownHandlerMu sync.RWMutex
	unknownHandlerFn Factory
)

// CLIAvailable returns nil iff `bin` is found on $PATH. Cached per
// process so repeated probes don't re-scan.
func CLIAvailable(bin string) error {
	return pathCache.lookup(bin)
}

type lookupCache struct {
	mu     sync.Mutex
	result map[string]error
}

func (c *lookupCache) lookup(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.result == nil {
		c.result = map[string]error{}
	}
	if err, ok := c.result[name]; ok {
		return err
	}
	_, err := exec.LookPath(name)
	if err != nil {
		err = fmt.Errorf("required CLI %q not found on PATH (%w)", name, err)
	}
	c.result[name] = err
	return err
}

var pathCache = &lookupCache{}

// ProbeAvailable reports whether a handler's underlying CLI is on PATH.
// Used by `dconsole handler:list` to summarise what's usable.
func ProbeAvailable(name string) error {
	r, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("unknown handler %q", name)
	}
	if r.RequiredCLI == "" {
		return nil
	}
	return CLIAvailable(r.RequiredCLI)
}

// RequiredCLI returns the CLI binary the named handler depends on
// (or "" for none). Returns ("", false) if the name isn't registered.
func RequiredCLI(name string) (string, bool) {
	r, ok := Lookup(name)
	if !ok {
		return "", false
	}
	return r.RequiredCLI, true
}
