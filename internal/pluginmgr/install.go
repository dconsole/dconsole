package pluginmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// InstallOpts controls Install behavior. Exactly one of Name/URL/Path
// must be set.
type InstallOpts struct {
	// Name fetches plugins/<name>.yml from the index.
	Name string
	// Version pins a specific version block from the YAML (optional).
	Version string
	// URL bypasses the index and downloads directly from a tarball URL.
	URL string
	// SHA256 is required when URL is set; verified against the download.
	SHA256 string
	// Path installs from a local tarball.
	Path string
}

// Install resolves the source, downloads/copies the tarball, verifies
// the sha256, extracts the contained binary (or single file) into
// ~/.dconsole/plugins/, and chmods it executable. Returns the path to
// the installed binary.
func Install(opts InstallOpts) (string, error) {
	if err := validateInstallOpts(opts); err != nil {
		return "", err
	}

	var (
		tarballPath string
		expectedSHA string
		cleanupDL   func()
	)
	switch {
	case opts.Path != "":
		tarballPath = opts.Path
		expectedSHA = opts.SHA256 // optional verification for --path
	case opts.URL != "":
		expectedSHA = opts.SHA256
		dl, cleanup, err := downloadToTemp(opts.URL)
		if err != nil {
			return "", err
		}
		tarballPath = dl
		cleanupDL = cleanup
	case opts.Name != "":
		doc, err := FetchPluginDoc(opts.Name)
		if err != nil {
			return "", err
		}
		ver, err := SelectVersion(doc, opts.Version)
		if err != nil {
			return "", err
		}
		plat, key, err := SelectPlatform(ver)
		if err != nil {
			return "", err
		}
		_ = key
		expectedSHA = plat.SHA256
		dl, cleanup, err := downloadToTemp(plat.URL)
		if err != nil {
			return "", err
		}
		tarballPath = dl
		cleanupDL = cleanup
	}
	if cleanupDL != nil {
		defer cleanupDL()
	}

	if expectedSHA != "" {
		if err := verifySHA256(tarballPath, expectedSHA); err != nil {
			return "", err
		}
	}

	dir, err := PluginDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return extractTarballTo(tarballPath, dir)
}

// Remove deletes a plugin binary by name (e.g. "skpr" removes
// dconsole-skpr from PluginDir).
func Remove(name string) error {
	dir, err := PluginDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "dconsole-"+name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("not installed (no %s)", path)
	}
	return os.Remove(path)
}

// ListInstalled walks PluginDir and returns the names of installed
// plugins (without the "dconsole-" prefix).
func ListInstalled() ([]string, error) {
	dir, err := PluginDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, "dconsole-") {
			continue
		}
		out = append(out, strings.TrimPrefix(n, "dconsole-"))
	}
	return out, nil
}

func validateInstallOpts(opts InstallOpts) error {
	set := 0
	if opts.Name != "" {
		set++
	}
	if opts.URL != "" {
		set++
	}
	if opts.Path != "" {
		set++
	}
	if set != 1 {
		return fmt.Errorf("install requires exactly one of --name, --url, or --path")
	}
	if opts.URL != "" && opts.SHA256 == "" {
		return fmt.Errorf("--url requires --sha256 for integrity verification")
	}
	return nil
}

func downloadToTemp(url string) (string, func(), error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "dconsole-plugin-manager")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.CreateTemp("", "dconsole-plugin-dl-*.tgz")
	if err != nil {
		return "", nil, err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, expected)
	}
	return nil
}
