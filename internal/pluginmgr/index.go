// Package pluginmgr implements dconsole's first-party plugin manager:
// fetching the curated index, downloading & verifying release artifacts,
// extracting them into ~/.dconsole/plugins/, and removing them.
//
// The curated index lives at github.com/heydon-consulting/dconsole-plugin-index
// (separate repo). Each plugin gets a single YAML file under plugins/
// describing its release artifacts per platform; dconsole fetches
// individual YAMLs over raw.githubusercontent.com (no API auth, no rate
// limit) and verifies a sha256 before installing.
package pluginmgr

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// IndexBaseURL is the location of the per-plugin YAML files.
// Overridable for testing.
var IndexBaseURL = "https://raw.githubusercontent.com/heydon-consulting/dconsole-plugin-index/main"

// HTTPClient is the client used for index + artifact fetches. Tests
// substitute a stub.
var HTTPClient = &http.Client{Timeout: 60 * time.Second}

// PluginDoc is the schema for plugins/<name>.yml in the index.
type PluginDoc struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description,omitempty"`
	Homepage    string    `yaml:"homepage,omitempty"`
	License     string    `yaml:"license,omitempty"`
	Transports  []string  `yaml:"transports,omitempty"`
	Providers   []string  `yaml:"providers,omitempty"`
	Versions    []Version `yaml:"versions"`
}

// Version is one release of a plugin.
type Version struct {
	Version          string              `yaml:"version"`
	RequiresDconsole string              `yaml:"requires_dconsole,omitempty"`
	Platforms        map[string]Platform `yaml:"platforms"`
}

// Platform is the per-OS+arch artifact reference. Key in Version.Platforms
// is "<goos>-<goarch>", e.g. "darwin-arm64".
type Platform struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
}

// IndexEntry is one row of the auto-generated _generated/index.json
// (the index repo's CI builds this from all plugins/*.yml).
type IndexEntry struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
}

// Index is the auto-generated _generated/index.json document.
type Index struct {
	IndexVersion int          `json:"index_version"`
	Plugins      []IndexEntry `json:"plugins"`
}

// FetchPluginDoc downloads and parses the YAML for one plugin from the
// index. Returns a clear error if the plugin isn't listed.
func FetchPluginDoc(name string) (*PluginDoc, error) {
	url := IndexBaseURL + "/plugins/" + name + ".yml"
	body, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	var doc PluginDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", url, err)
	}
	if doc.Name == "" {
		doc.Name = name
	}
	if len(doc.Versions) == 0 {
		return nil, fmt.Errorf("plugin %q has no versions in the index", name)
	}
	return &doc, nil
}

// FetchIndex downloads the auto-generated index summary (name +
// description + latest version per plugin), used by `plugin search`.
func FetchIndex() (*Index, error) {
	url := IndexBaseURL + "/_generated/index.json"
	body, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	var idx Index
	if err := yaml.Unmarshal(body, &idx); err != nil {
		// _generated/index.json is JSON but YAML supersedes JSON in yaml.v3
		// — so the same unmarshal call works either way.
		return nil, fmt.Errorf("parse %s: %w", url, err)
	}
	return &idx, nil
}

// SelectVersion picks the right Version block for a name + optional
// version pin. If pin is empty, the LAST entry of Versions wins
// (convention: append-only, newest at the bottom).
func SelectVersion(doc *PluginDoc, pin string) (*Version, error) {
	if pin == "" {
		v := doc.Versions[len(doc.Versions)-1]
		return &v, nil
	}
	for i := range doc.Versions {
		if doc.Versions[i].Version == pin {
			return &doc.Versions[i], nil
		}
	}
	return nil, fmt.Errorf("plugin %s has no version %q (available: %s)", doc.Name, pin, joinVersions(doc.Versions))
}

// SelectPlatform picks the Platform block matching the host. Returns a
// clear error listing the available platforms when there's no match.
func SelectPlatform(v *Version) (*Platform, string, error) {
	key := runtime.GOOS + "-" + runtime.GOARCH
	if p, ok := v.Platforms[key]; ok {
		return &p, key, nil
	}
	available := make([]string, 0, len(v.Platforms))
	for k := range v.Platforms {
		available = append(available, k)
	}
	return nil, "", fmt.Errorf("no artifact for host %s in version %s (available: %s)", key, v.Version, strings.Join(available, ", "))
}

// PluginDir returns the user-scoped install directory:
// $XDG_DATA_HOME/dconsole/plugins or ~/.dconsole/plugins.
func PluginDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "dconsole", "plugins"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".dconsole", "plugins"), nil
}

func joinVersions(v []Version) string {
	out := make([]string, len(v))
	for i, ver := range v {
		out[i] = ver.Version
	}
	return strings.Join(out, ", ")
}

func httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dconsole-plugin-manager")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
