package transport

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// PluginDir returns the user-scoped directory dconsole searches first
// for plugin binaries. Honours $XDG_DATA_HOME when set, otherwise
// ~/.dconsole/plugins/. Returns "" if HOME isn't set.
func PluginDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "dconsole", "plugins")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".dconsole", "plugins")
	}
	return ""
}

// ErrPluginNotFound is returned by FindPlugin when no binary named
// dconsole-<type> can be located in the plugin dir or on $PATH.
var ErrPluginNotFound = errors.New("plugin binary not found")

// FindPlugin returns the absolute path to the binary implementing the
// named plugin type, searching ~/.dconsole/plugins/ first then $PATH.
// Results are cached per process.
func FindPlugin(name string) (string, error) {
	pluginCache.mu.Lock()
	defer pluginCache.mu.Unlock()
	if pluginCache.m == nil {
		pluginCache.m = map[string]pluginCacheEntry{}
	}
	if e, ok := pluginCache.m[name]; ok {
		return e.path, e.err
	}
	path, err := findPluginUncached(name)
	pluginCache.m[name] = pluginCacheEntry{path: path, err: err}
	return path, err
}

func findPluginUncached(name string) (string, error) {
	bin := "dconsole-" + name
	if dir := PluginDir(); dir != "" {
		candidate := filepath.Join(dir, bin)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath(bin); err == nil {
		return p, nil
	}
	return "", ErrPluginNotFound
}

// ResetPluginCacheForTests clears the lookup cache. Tests call this
// after manipulating XDG_DATA_HOME / HOME / PATH.
func ResetPluginCacheForTests() {
	pluginCache.mu.Lock()
	defer pluginCache.mu.Unlock()
	pluginCache.m = nil
}

type pluginCacheEntry struct {
	path string
	err  error
}

var pluginCache struct {
	mu sync.Mutex
	m  map[string]pluginCacheEntry
}
