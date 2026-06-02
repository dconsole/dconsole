// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"

	"github.com/dconsole/dconsole/internal/alias"
)

// DBImporter is an optional capability a Transport may implement when
// it has a native, faster database-import path than feeding bytes to
// drush sql:cli. The sql:sync orchestrator type-asserts each target
// transport against this interface and uses it in preference to the
// generic pipe-to-drush fallback. ddev implements it via
// `ddev import-db`; other transports can opt in later.
//
// Deprecated (v0.4.0): use pkg/handler.DBImporter. The signature is
// identical and existing implementations satisfy both. After call
// sites migrate to handler.For, this declaration is removed.
type DBImporter interface {
	ImportDB(ctx context.Context, a *alias.Alias, dumpPath string) error
}

// FilesImporter is the asset-bundle sibling of DBImporter.
//
// Deprecated (v0.4.0): use pkg/handler.FilesImporter.
type FilesImporter interface {
	ImportFiles(ctx context.Context, a *alias.Alias, bundlePath string) error
}

// RsyncSSH is an optional capability a Transport implements when it
// represents an ssh-reachable endpoint suitable for the rsync binary.
//
// Deprecated (v0.4.0): use pkg/handler.RsyncSSH.
type RsyncSSH interface {
	RsyncRemote() (remote string, sshOpts []string)
}

// Transport reaches a remote environment described by an Alias.
//
// Deprecated (v0.4.0): use pkg/handler.Handler, which is a superset
// (mandatory Wrap() for chain composition + all of pkg/provider's
// platform-op methods as duck-typed capabilities). Every in-tree
// transport already satisfies handler.Handler. New code should call
// handler.For(a); call sites are being migrated. See pkg/handler/doc.go.
type Transport interface {
	Name() string
	Available() error
	Exec(ctx context.Context, cmd []string, stdio Stdio) error
	Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error
	Shell(ctx context.Context, workDir string) error
	Preview(remoteCmd []string) []string
}

// Factory builds a Transport from the alias's transport block. Factories
// should call alias.Transport.Decode(&cfg) to pull their typed config out
// of the opaque YAML node.
type Factory func(a *alias.Alias) (Transport, error)

// Registration is the metadata + factory bundle dconsole stores per
// transport type. RequiredCLI is the binary on $PATH this transport
// depends on (or "" for none) — declared at registration so dconsole
// can answer "is the ssh transport usable?" without needing an alias.
type Registration struct {
	Build       Factory
	RequiredCLI string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Registration{}
)

// Register adds a transport type to the global registry. In-tree
// transports call this from init(); the subprocess-plugin runner calls
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

// Names returns registered transport names sorted alphabetically.
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

// For returns the Transport implementation that an alias has configured.
// If the type isn't in the registry, the installed unknown-type handler
// (if any) is consulted — that's where the subprocess-plugin runner
// hooks in.
func For(a *alias.Alias) (Transport, error) {
	t := a.Transport.Type
	if t == "" {
		return nil, fmt.Errorf("alias @%s.%s has no transport configured", a.Site, a.Env)
	}
	if r, ok := Lookup(t); ok {
		return r.Build(a)
	}
	if h := unknownHandler(); h != nil {
		return h(a)
	}
	return nil, fmt.Errorf("unknown transport type %q on @%s.%s", t, a.Site, a.Env)
}

// SetUnknownTypeHandler installs a fallback Factory invoked by For when
// the transport type isn't in the in-tree registry. dconsole's binary
// wires this to the subprocess-plugin runner; SDK consumers don't need it.
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

// CLIAvailable returns nil iff `bin` is found on $PATH. Result is cached
// per process so repeated probes inside the same dconsole invocation
// don't re-scan the filesystem.
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

// ProbeAvailable reports whether a transport's underlying CLI is on
// PATH, looked up by transport name (no alias required). Used by
// `dconsole transport:list` to summarise what's usable on this machine.
func ProbeAvailable(name string) error {
	r, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("unknown transport %q", name)
	}
	if r.RequiredCLI == "" {
		return nil
	}
	return CLIAvailable(r.RequiredCLI)
}

// RequiredCLI returns the CLI binary the named transport depends on
// (or "" for none). Returns ("", false) if the name isn't registered.
func RequiredCLI(name string) (string, bool) {
	r, ok := Lookup(name)
	if !ok {
		return "", false
	}
	return r.RequiredCLI, true
}
