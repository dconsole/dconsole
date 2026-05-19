// Package sitepath resolves Drush/Drupal `%files`, `%root`, `%private`
// path tokens for an alias by asking the remote CLI via its transport.
package sitepath

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/remotebin"
)

// Runner is the minimal capability we need to ask the remote: run a
// command and read stdout. Implemented by anything that wraps a Transport.
type Runner interface {
	Run(ctx context.Context, cmd []string) (stdout []byte, err error)
}

// Paths holds the per-alias path tokens. Empty strings mean the remote
// didn't report that token.
type Paths struct {
	Root    string `json:"root"`
	Files   string `json:"files"`
	Private string `json:"private"`
	Site    string `json:"site"`
}

// Resolve asks the alias's remote CLI for its status and extracts the
// path tokens. The result is cached in memory per process AND on disk at
// ~/.cache/dconsole/sitepath-*.json keyed by alias coordinates + transport.
func Resolve(ctx context.Context, a *alias.Alias, bin *remotebin.Resolved, r Runner) (*Paths, error) {
	key := cacheKey(a)
	if p := memo.get(key); p != nil {
		return p, nil
	}
	if p := loadDisk(key); p != nil {
		memo.put(key, p)
		return p, nil
	}
	out, err := r.Run(ctx, bin.Argv([]string{"status", "--format=json"}))
	if err != nil {
		return nil, fmt.Errorf("`%s status --format=json` failed: %w", bin.Kind, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &raw); err != nil {
		return nil, fmt.Errorf("parse status JSON: %w", err)
	}
	p := &Paths{
		Root:    stringField(raw, "root"),
		Files:   stringField(raw, "files"),
		Private: stringField(raw, "private"),
		Site:    stringField(raw, "site"),
	}
	// drush reports files/private relative to root; absolute paths are
	// preserved. Normalize to absolute.
	p.Files = absolutize(p.Root, p.Files)
	p.Private = absolutize(p.Root, p.Private)

	if p.Root == "" {
		return nil, fmt.Errorf("remote status did not include a root path")
	}
	memo.put(key, p)
	saveDisk(key, p)
	return p, nil
}

// Expand replaces "%root", "%files", "%private" in `pathSpec` with the
// corresponding absolute path on the remote. If `pathSpec` has no token,
// it is returned unchanged. If `pathSpec` is empty, it defaults to %root.
func (p *Paths) Expand(pathSpec string) (string, error) {
	if pathSpec == "" || pathSpec == "%root" {
		return p.Root, nil
	}
	if pathSpec == "%files" {
		if p.Files == "" {
			return "", fmt.Errorf("remote did not report %%files path")
		}
		return p.Files, nil
	}
	if pathSpec == "%private" {
		if p.Private == "" {
			return "", fmt.Errorf("remote did not report %%private path")
		}
		return p.Private, nil
	}
	// Allow nested forms like "%files/subdir".
	if strings.HasPrefix(pathSpec, "%root/") {
		return path.Join(p.Root, strings.TrimPrefix(pathSpec, "%root/")), nil
	}
	if strings.HasPrefix(pathSpec, "%files/") && p.Files != "" {
		return path.Join(p.Files, strings.TrimPrefix(pathSpec, "%files/")), nil
	}
	if strings.HasPrefix(pathSpec, "%private/") && p.Private != "" {
		return path.Join(p.Private, strings.TrimPrefix(pathSpec, "%private/")), nil
	}
	return pathSpec, nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func absolutize(root, p string) string {
	if p == "" || root == "" {
		return p
	}
	if path.IsAbs(p) {
		return p
	}
	return path.Join(root, p)
}

func cacheKey(a *alias.Alias) string {
	h := sha256.Sum256([]byte(a.Site + "\x00" + a.Env + "\x00" + a.Root))
	return hex.EncodeToString(h[:])
}

func cachePath(key string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "dconsole", "sitepath-"+key+".json")
}

func loadDisk(key string) *Paths {
	p := cachePath(key)
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out Paths
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return &out
}

func saveDisk(key string, p *Paths) {
	cp := cachePath(key)
	if cp == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cp), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = os.WriteFile(cp, data, 0o600)
}

type cache struct {
	mu sync.Mutex
	m  map[string]*Paths
}

func (c *cache) get(k string) *Paths {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k]
}

func (c *cache) put(k string, p *Paths) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*Paths{}
	}
	c.m[k] = p
}

var memo = &cache{}
