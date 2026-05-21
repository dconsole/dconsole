// Package dbcache caches sql:sync source dumps under
// $XDG_CACHE_HOME/dconsole/sql/ keyed by alias + source-strategy fingerprint.
// Subsequent sql:sync runs against the same source within the TTL skip
// the (expensive) dump step entirely.
//
// On-disk layout per cache key:
//
//	<dir>/<key>.sql.gz     the gzipped dump bytes
//	<dir>/<key>.json       Metadata (timestamps, coordinates, sizes)
//
// Writes are atomic: the dump streams into <key>.sql.gz.tmp, fsync, then
// os.Rename. Concurrent readers always see either the previous file or
// the new one — never a torn write.
package dbcache

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

	"github.com/dconsole/dconsole/internal/alias"
)

// DefaultTTL is the freshness window used when no TTL is supplied.
const DefaultTTL = 24 * time.Hour

// KeyInputs are the dimensions a cache entry is keyed by. Two runs with
// identical inputs produce the same key (and so the same cached file);
// any difference produces a different key, so a config change naturally
// invalidates the prior cache.
type KeyInputs struct {
	Site             string
	Env              string
	Strategy         string   // "drush" | "file" | "docker_cp" | provider name
	SourceDatabase   string   // "default" if unset
	StructureTables  []string // sorted, comma-joined into the fingerprint
	StructTablesKey  string   // named structure-tables key
}

// KeyFor produces the canonical sha256 hex digest for a source alias and
// the chosen source strategy. Stable across runs: same inputs → same key.
func KeyFor(in KeyInputs) string {
	h := sha256.New()
	w := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	w(in.Site)
	w(in.Env)
	w(in.Strategy)
	db := in.SourceDatabase
	if db == "" {
		db = "default"
	}
	w(db)
	st := append([]string(nil), in.StructureTables...)
	sort.Strings(st)
	w(strings.Join(st, ","))
	w(in.StructTablesKey)
	return hex.EncodeToString(h.Sum(nil))
}

// KeyForAlias is a convenience wrapper that derives KeyInputs from an
// alias + explicit strategy tag. The strategy tag mirrors what the
// sql:sync orchestrator uses to dispatch (provider name when a provider
// supplied the dump; otherwise alias.SQL.Source.Type or "drush").
func KeyForAlias(a *alias.Alias, strategy string) string {
	return KeyFor(KeyInputs{
		Site:            a.Site,
		Env:             a.Env,
		Strategy:        strategy,
		SourceDatabase:  a.SQL.Source.Database,
		StructureTables: a.SQL.Source.StructureTables,
		StructTablesKey: a.SQL.Source.StructureTablesKey,
	})
}

// Metadata is the JSON document written alongside each cached dump.
// Acts as the freshness anchor: missing or malformed metadata makes the
// entry stale (so partial writes aren't honoured as fresh).
type Metadata struct {
	Site         string    `json:"site"`
	Env          string    `json:"env"`
	Strategy     string    `json:"strategy"`
	SourceDB     string    `json:"source_database,omitempty"`
	StructTables []string  `json:"structure_tables,omitempty"`
	StructKey    string    `json:"structure_tables_key,omitempty"`
	StoredAt     time.Time `json:"stored_at"`
	SizeBytes    int64     `json:"size_bytes"`
	DconsoleVer  string    `json:"dconsole_version,omitempty"`
}

// Entry is one cached dump + its metadata.
type Entry struct {
	Key  string
	Path string // absolute path to <key>.sql.gz
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

// Dir returns the cache directory dconsole will use, honouring
// $XDG_CACHE_HOME, falling back to ~/.cache/dconsole/sql/.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "dconsole", "sql"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".cache", "dconsole", "sql"), nil
}

// Get returns the cached dump path for key if it exists and is within
// ttl. ok=false means "missing or stale, caller must (re)populate".
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

// Put copies srcPath into the cache at <key>.sql.gz and writes metadata
// JSON. Returns the cached file's path. The source path is left untouched
// — callers either move or delete it themselves.
//
// The write is atomic against concurrent readers: bytes land at
// <key>.sql.gz.tmp first, fsync, then os.Rename into place.
func Put(key, srcPath string, meta Metadata) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	dst := entryPath(dir, key)
	// Per-call random suffix so concurrent Put callers don't fight over
	// the same .tmp file. Each goroutine writes its own tmp and renames
	// last-writer-wins.
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
		// Don't leave dump without metadata — better to remove both.
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// Invalidate removes both the dump and its metadata. Missing entries
// return nil (idempotent).
func Invalidate(key string) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range []string{entryPath(dir, key), metaPath(dir, key)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// List returns every cache entry on disk (both fresh and stale).
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
		if !strings.HasSuffix(name, ".sql.gz") {
			continue
		}
		key := strings.TrimSuffix(name, ".sql.gz")
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

// Clear removes every entry for which filter returns true. Returns the
// number of entries removed. A nil filter removes everything.
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

// readEntry loads a (sql.gz, metadata) pair into an Entry.
func readEntry(dir, key string) (Entry, bool, error) {
	dump := entryPath(dir, key)
	if _, err := os.Stat(dump); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	body, err := os.ReadFile(metaPath(dir, key))
	if err != nil {
		// Dump file exists but no metadata — treat as stale (likely a
		// partial write or pre-format file). Caller should re-populate.
		return Entry{Key: key, Path: dump}, true, nil
	}
	var meta Metadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return Entry{Key: key, Path: dump}, true, nil
	}
	return Entry{Key: key, Path: dump, Meta: meta}, true, nil
}

func writeMeta(dir, key string, meta Metadata) error {
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(dir, key), body, 0o600)
}

func entryPath(dir, key string) string { return filepath.Join(dir, key+".sql.gz") }
func metaPath(dir, key string) string  { return filepath.Join(dir, key+".json") }

// copyFileAtomic streams srcPath → dstPath, fsync, close. Caller is
// expected to os.Rename(dstPath, finalPath) afterwards.
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

// ParseTTL parses the duration string from alias.sql.cache.ttl, returning
// (DefaultTTL, nil) for empty input. Useful so callers can write a single
// "effective TTL" expression.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultTTL, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("dbcache: invalid TTL %q: %w", s, err)
	}
	return d, nil
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Vanishingly unlikely; in the absolute worst case the suffix
		// collides and one of the two writers retries — not a
		// correctness issue, just a hiccup.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
