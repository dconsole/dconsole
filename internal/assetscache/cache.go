// Package assetscache caches provider-supplied asset bundles for
// `dconsole rsync` runs. Unlike sql:sync's dbcache (which caches every
// dump produced by any strategy), the assets cache holds ONLY
// provider.FilesDownload results — the diff and rsync modes work
// inherently fresh against live source state.
//
// On-disk layout per cache key:
//
//	<dir>/<key>.tar.gz   the gzipped asset bundle
//	<dir>/<key>.json     Metadata (timestamps, alias coords, pathspec)
//
// Atomic writes via .tmp.<rand> + os.Rename, matching dbcache.
package assetscache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/heydon/dconsole/internal/alias"
)

// DefaultTTL is the freshness window used when no TTL is supplied.
const DefaultTTL = 24 * time.Hour

// KeyInputs are the dimensions a cache entry is keyed by. Identical
// inputs produce the same key (and the same cached file); any
// difference produces a different key, so changing pathspec / provider
// invalidates automatically.
type KeyInputs struct {
	Site     string
	Env      string
	Strategy string // e.g. "provider:skpr"
	Pathspec string // "%files" or "%private"
}

// KeyFor produces the canonical sha256 hex digest for an alias + chosen
// strategy + pathspec.
func KeyFor(in KeyInputs) string {
	h := sha256.New()
	w := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	w(in.Site)
	w(in.Env)
	w(in.Strategy)
	ps := in.Pathspec
	if ps == "" {
		ps = "%files"
	}
	w(ps)
	return hex.EncodeToString(h.Sum(nil))
}

// KeyForAlias is a convenience wrapper.
func KeyForAlias(a *alias.Alias, strategy, pathspec string) string {
	return KeyFor(KeyInputs{Site: a.Site, Env: a.Env, Strategy: strategy, Pathspec: pathspec})
}

// Metadata is the JSON document written alongside each cached bundle.
// Acts as the freshness anchor: missing or malformed metadata makes
// the entry stale.
type Metadata struct {
	Site        string    `json:"site"`
	Env         string    `json:"env"`
	Strategy    string    `json:"strategy"`
	Pathspec    string    `json:"pathspec"`
	SourcePath  string    `json:"source_path,omitempty"`
	StoredAt    time.Time `json:"stored_at"`
	SizeBytes   int64     `json:"size_bytes"`
	DconsoleVer string    `json:"dconsole_version,omitempty"`
}

// Entry is one cached bundle + its metadata.
type Entry struct {
	Key  string
	Path string // absolute path to <key>.tar.gz
	Meta Metadata
}

// Age reports how long since the entry was stored. Returns 0 for unset
// StoredAt (treated as stale by IsFresh).
func (e Entry) Age() time.Duration {
	if e.Meta.StoredAt.IsZero() {
		return 0
	}
	return time.Since(e.Meta.StoredAt)
}

// IsFresh reports whether the entry is within ttl of now.
func (e Entry) IsFresh(ttl time.Duration) bool {
	if e.Meta.StoredAt.IsZero() {
		return false
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return time.Since(e.Meta.StoredAt) <= ttl
}

// Dir returns the cache directory, honouring $XDG_CACHE_HOME.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "dconsole", "assets"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".cache", "dconsole", "assets"), nil
}

// Get returns the cached bundle path if it exists and is within ttl.
func Get(key string, ttl time.Duration) (path string, ok bool, err error) {
	dir, err := Dir()
	if err != nil {
		return "", false, err
	}
	entry, ok, err := readEntry(dir, key)
	if err != nil || !ok {
		return "", false, err
	}
	if !entry.IsFresh(ttl) {
		return "", false, nil
	}
	return entry.Path, true, nil
}

// Put copies srcPath into the cache at <key>.tar.gz and writes the
// metadata JSON. Returns the cached file's path. Atomic against
// concurrent readers via per-call random .tmp suffix + rename.
func Put(key, srcPath string, meta Metadata) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	dst := entryPath(dir, key)
	tmp := dst + ".tmp." + randomSuffix()
	if err := copyFileAtomic(srcPath, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}

	if meta.StoredAt.IsZero() {
		meta.StoredAt = time.Now().UTC()
	}
	if meta.SizeBytes == 0 {
		if info, err := os.Stat(dst); err == nil {
			meta.SizeBytes = info.Size()
		}
	}
	if err := writeMeta(dir, key, meta); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// Invalidate removes both the bundle and its metadata. Missing entries
// return nil (idempotent).
func Invalidate(key string) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	for _, p := range []string{entryPath(dir, key), metaPath(dir, key)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// List returns every cache entry on disk (fresh and stale), newest first.
func List() ([]Entry, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	infos, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, fi := range infos {
		if fi.IsDir() {
			continue
		}
		name := fi.Name()
		if !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		key := strings.TrimSuffix(name, ".tar.gz")
		e, ok, err := readEntry(dir, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Meta.StoredAt.After(out[j].Meta.StoredAt)
	})
	return out, nil
}

// Clear removes every entry where filter returns true. Nil filter
// removes everything.
func Clear(filter func(Entry) bool) (int, error) {
	entries, err := List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if filter != nil && !filter(e) {
			continue
		}
		if err := Invalidate(e.Key); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// readEntry loads a (tar.gz, metadata) pair.
func readEntry(dir, key string) (Entry, bool, error) {
	bundle := entryPath(dir, key)
	if _, err := os.Stat(bundle); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	body, err := os.ReadFile(metaPath(dir, key))
	if err != nil {
		// Bundle exists but no metadata — treat as stale.
		return Entry{Key: key, Path: bundle}, true, nil
	}
	var meta Metadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return Entry{Key: key, Path: bundle}, true, nil
	}
	return Entry{Key: key, Path: bundle, Meta: meta}, true, nil
}

func writeMeta(dir, key string, meta Metadata) error {
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(dir, key), body, 0o600)
}

func entryPath(dir, key string) string { return filepath.Join(dir, key+".tar.gz") }
func metaPath(dir, key string) string  { return filepath.Join(dir, key+".json") }

// copyFileAtomic streams src → dst, fsync, close.
func copyFileAtomic(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dstPath)
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		os.Remove(dstPath)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(dstPath)
		return err
	}
	return nil
}

// ParseTTL parses the alias.assets.cache.ttl duration string.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultTTL, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("assetscache: invalid TTL %q: %w", s, err)
	}
	return d, nil
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
