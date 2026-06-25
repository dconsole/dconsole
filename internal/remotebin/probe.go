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

	"github.com/dconsole/dconsole/internal/alias"
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
	var tried []attempt
	seen := map[string]bool{}
	for _, c := range candidates {
		out, err := p.Run(ctx, []string{c.Path, "--version"})
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			memoCache.put(key, c)
			saveToDisk(key, c)
			return c, nil
		}
		if seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		tried = append(tried, attempt{c.Path, classifyProbeErr(c.Path, err)})
	}
	return nil, formatProbeFailure(a, tried)
}

// classifyProbeErr collapses raw fork/exec errors into a short human
// phrase. Auto-probe tries six-plus candidates and most fail with the
// same "no such file or directory" — repeating that verbatim makes
// the error a wall of noise. Unfamiliar errors (permission denied,
// drush exits non-zero) get passed through so the diagnostic info
// isn't lost.
func classifyProbeErr(p string, err error) string {
	if err == nil {
		return "no output"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "no such file or directory"):
		return "no such file"
	case strings.Contains(s, "executable file not found in $PATH"):
		return "not on $PATH"
	case strings.Contains(s, "permission denied"):
		return "permission denied"
	default:
		return s
	}
}

// formatProbeFailure renders the probe failure as a multi-line message.
// When the alias is the synthetic local default (@self.local — i.e.
// the user ran dconsole from a directory with no project config), we
// also append a short hint pointing at the three common fixes. Remote
// aliases skip the hint because "cd into a Drupal directory" doesn't
// apply.
func formatProbeFailure(a *alias.Alias, tried []attempt) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no Drupal CLI (drush or drupal) found for @%s.%s.\n\nTried:\n", a.Site, a.Env)
	for _, t := range tried {
		fmt.Fprintf(&b, "  %-44s (%s)\n", t.path, t.reason)
	}
	if a.Site == "self" && a.Env == "local" {
		b.WriteString("\nFixes:\n")
		b.WriteString("  - cd into a Drupal project (with vendor/bin/drush)\n")
		b.WriteString("  - target a remote alias: dconsole @site.env <cmd>\n")
		b.WriteString("  - install drush globally: composer global require drush/drush\n")
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

type attempt struct{ path, reason string }

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
