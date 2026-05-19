package remotebin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/heydon/dconsole/internal/alias"
	"gopkg.in/yaml.v3"
)

// Prober probes the remote side to decide whether to use `drupal` or
// `drush` when an alias has `bin.kind: auto`. Results are memoized in
// memory and persisted to ~/.cache/dconsole/probe-*.json.
type Prober interface {
	// Run executes a no-stdin command on the remote and returns its
	// stdout + an error if the process exited non-zero. Used by Probe to
	// invoke `<path> --version`.
	Run(ctx context.Context, cmd []string) (stdout []byte, err error)
}

// Probe resolves bin.kind: auto into a concrete Resolved by trying
// `<root>/vendor/bin/drupal --version`, then `<root>/vendor/bin/drush
// --version`, then `drush --version` on PATH. Returns the first that
// produces a clean exit.
func Probe(ctx context.Context, a *alias.Alias, p Prober) (*Resolved, error) {
	key := cacheKey(a)
	if r := memoCache.get(key); r != nil {
		return r, nil
	}
	if r := loadFromDisk(key); r != nil {
		memoCache.put(key, r)
		return r, nil
	}

	// Try each candidate path in order: sibling-vendor drupal, nested
	// drupal, sibling-vendor drush, nested drush, PATH lookups.
	var candidates []*Resolved
	for _, p := range CandidatePaths(a) {
		kind := KindDrush
		if strings.Contains(p, "drupal") || p == "drupal" {
			kind = KindDrupal
		}
		candidates = append(candidates, &Resolved{Kind: kind, Path: p})
	}
	var errs []string
	for _, c := range candidates {
		out, err := p.Run(ctx, []string{c.Path, "--version"})
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			memoCache.put(key, c)
			saveToDisk(key, c)
			return c, nil
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", c.Path, err))
		}
	}
	return nil, fmt.Errorf("bin.kind: auto could not find drupal or drush on @%s.%s: %v", a.Site, a.Env, errs)
}

// Forget removes a cached probe result, forcing a re-probe.
func Forget(a *alias.Alias) {
	key := cacheKey(a)
	memoCache.del(key)
	_ = os.Remove(cachePath(key))
}

// cacheKey is a stable hash of the alias coordinates and transport block
// so that re-probing happens after a transport change.
func cacheKey(a *alias.Alias) string {
	tBytes, _ := yaml.Marshal(a.Transport)
	h := sha256.Sum256([]byte(a.Site + "\x00" + a.Env + "\x00" + a.Root + "\x00" + string(tBytes)))
	return hex.EncodeToString(h[:])
}

func cachePath(key string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".cache", "dconsole")
	return filepath.Join(dir, "probe-"+key+".json")
}

func loadFromDisk(key string) *Resolved {
	p := cachePath(key)
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var r Resolved
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	if r.Kind == "" || r.Path == "" {
		return nil
	}
	return &r
}

func saveToDisk(key string, r *Resolved) {
	p := cachePath(key)
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o600)
}

type cache struct {
	mu  sync.Mutex
	m   map[string]*Resolved
}

func (c *cache) get(k string) *Resolved {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k]
}

func (c *cache) put(k string, r *Resolved) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*Resolved{}
	}
	c.m[k] = r
}

func (c *cache) del(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}

var memoCache = &cache{}

// Sentinel — the Forward command checks for this to give a nicer error.
var ErrAutoProbeFailed = errors.New("auto-probe failed")
