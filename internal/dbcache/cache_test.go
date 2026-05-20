package dbcache

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// useTempDir points dbcache at a per-test cache directory by setting
// XDG_CACHE_HOME.
func useTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeBlob(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustPut(t *testing.T, key, body string, meta Metadata) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src.sql.gz")
	writeBlob(t, src, body)
	path, err := Put(key, src, meta)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPutGet_RoundTrip(t *testing.T) {
	useTempDir(t)
	const body = "FAKE-GZIP-DUMP"
	mustPut(t, "abc123", body, Metadata{Site: "demo", Env: "prod", Strategy: "drush"})

	path, ok, err := Get("abc123", DefaultTTL)
	if err != nil || !ok {
		t.Fatalf("Get(abc123, default) = (%q, %v, %v); want ok=true", path, ok, err)
	}
	read, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != body {
		t.Errorf("cached body = %q, want %q", string(read), body)
	}
}

func TestGet_StaleExpired(t *testing.T) {
	useTempDir(t)
	// Stored an hour ago; TTL of 1 minute → stale.
	mustPut(t, "stale", "x", Metadata{StoredAt: time.Now().Add(-1 * time.Hour)})
	_, ok, err := Get("stale", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected stale entry to return ok=false")
	}
}

func TestGet_FreshWithinTTL(t *testing.T) {
	useTempDir(t)
	mustPut(t, "fresh", "x", Metadata{StoredAt: time.Now().Add(-10 * time.Second)})
	if _, ok, err := Get("fresh", time.Minute); err != nil || !ok {
		t.Errorf("Get(fresh, 1min) = (_, %v, %v); want ok=true", ok, err)
	}
}

func TestGet_MissingReturnsFalse(t *testing.T) {
	useTempDir(t)
	if _, ok, err := Get("nope", DefaultTTL); err != nil || ok {
		t.Errorf("Get(nope) = (_, %v, %v); want ok=false, err=nil", ok, err)
	}
}

func TestPut_SizeBytesPopulated(t *testing.T) {
	useTempDir(t)
	path := mustPut(t, "sized", "1234567890", Metadata{Site: "demo"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 10 {
		t.Errorf("dump size = %d, want 10", info.Size())
	}
	// Reload metadata to verify SizeBytes was recorded.
	entry := loadEntryForTest(t, "sized")
	if entry.Meta.SizeBytes != 10 {
		t.Errorf("Metadata.SizeBytes = %d, want 10", entry.Meta.SizeBytes)
	}
}

func TestInvalidate_RemovesBothFiles(t *testing.T) {
	dir := useTempDir(t)
	mustPut(t, "drop-me", "x", Metadata{Site: "demo"})

	for _, p := range []string{
		filepath.Join(dir, "drop-me.sql.gz"),
		filepath.Join(dir, "drop-me.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}
	if err := Invalidate("drop-me"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(dir, "drop-me.sql.gz"),
		filepath.Join(dir, "drop-me.json"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s gone, got err=%v", p, err)
		}
	}
}

func TestInvalidate_MissingIsIdempotent(t *testing.T) {
	useTempDir(t)
	if err := Invalidate("never-existed"); err != nil {
		t.Errorf("Invalidate of missing key returned error: %v", err)
	}
}

func TestKeyFor_DifferentInputsProduceDifferentKeys(t *testing.T) {
	cases := []KeyInputs{
		{Site: "a", Env: "prod", Strategy: "drush"},
		{Site: "a", Env: "stage", Strategy: "drush"},                                          // different env
		{Site: "b", Env: "prod", Strategy: "drush"},                                          // different site
		{Site: "a", Env: "prod", Strategy: "file"},                                           // different strategy
		{Site: "a", Env: "prod", Strategy: "drush", SourceDatabase: "migrate"},               // different DB
		{Site: "a", Env: "prod", Strategy: "drush", StructureTables: []string{"cache"}},      // structure tables
		{Site: "a", Env: "prod", Strategy: "drush", StructureTables: []string{"sessions"}},   // different list
		{Site: "a", Env: "prod", Strategy: "drush", StructTablesKey: "common"},               // structure key
	}
	seen := map[string]int{}
	for i, in := range cases {
		k := KeyFor(in)
		if prev, dup := seen[k]; dup {
			t.Errorf("inputs %d and %d produced the same key %s", prev, i, k)
		}
		seen[k] = i
	}
}

func TestKeyFor_StableForIdenticalInputs(t *testing.T) {
	in := KeyInputs{
		Site: "demo", Env: "prod", Strategy: "drush",
		SourceDatabase:  "default",
		StructureTables: []string{"sessions", "cache"}, // unsorted
		StructTablesKey: "",
	}
	k1 := KeyFor(in)
	in.StructureTables = []string{"cache", "sessions"} // sorted differently
	k2 := KeyFor(in)
	if k1 != k2 {
		t.Errorf("KeyFor not stable across structure-tables ordering: %s vs %s", k1, k2)
	}
}

func TestList_ReturnsEntriesNewestFirst(t *testing.T) {
	useTempDir(t)
	mustPut(t, "old", "x", Metadata{StoredAt: time.Now().Add(-2 * time.Hour)})
	mustPut(t, "new", "y", Metadata{StoredAt: time.Now()})

	entries, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	if entries[0].Key != "new" || entries[1].Key != "old" {
		t.Errorf("entries not newest-first: %v", []string{entries[0].Key, entries[1].Key})
	}
}

func TestClear_FilterApplied(t *testing.T) {
	useTempDir(t)
	mustPut(t, "keep", "x", Metadata{Site: "keep-site"})
	mustPut(t, "drop1", "y", Metadata{Site: "drop-site"})
	mustPut(t, "drop2", "z", Metadata{Site: "drop-site"})

	removed, err := Clear(func(e Entry) bool { return e.Meta.Site == "drop-site" })
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("Clear returned removed=%d, want 2", removed)
	}
	rest, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].Key != "keep" {
		got := make([]string, len(rest))
		for i, r := range rest {
			got[i] = r.Key
		}
		sort.Strings(got)
		t.Errorf("after Clear, entries = %v, want [keep]", strings.Join(got, ","))
	}
}

func TestParseTTL(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", DefaultTTL, true},
		{"  ", DefaultTTL, true},
		{"6h", 6 * time.Hour, true},
		{"30m", 30 * time.Minute, true},
		{"24h", 24 * time.Hour, true},
		{"nope", 0, false},
	}
	for _, c := range cases {
		got, err := ParseTTL(c.in)
		if c.ok != (err == nil) {
			t.Errorf("ParseTTL(%q) err=%v, ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("ParseTTL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestPut_ConcurrentWritersDontTear ensures the atomic .tmp+rename
// approach prevents partial reads.
func TestPut_ConcurrentWritersDontTear(t *testing.T) {
	useTempDir(t)
	const key = "race"

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			src := filepath.Join(t.TempDir(), "src")
			body := strings.Repeat("ABCDEFGHIJ", 1000)
			if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
				t.Error(err)
				return
			}
			if _, err := Put(key, src, Metadata{Site: "demo"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	path, ok, err := Get(key, DefaultTTL)
	if err != nil || !ok {
		t.Fatal("expected the key to exist after concurrent Puts")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Every Put wrote the same body, so the result must equal it.
	want := strings.Repeat("ABCDEFGHIJ", 1000)
	if string(got) != want {
		t.Errorf("read back %d bytes, expected exact match (atomic rename failed?)", len(got))
	}
}

// loadEntryForTest is a thin wrapper around the unexported readEntry so
// tests can assert the metadata round-tripped correctly.
func loadEntryForTest(t *testing.T, key string) Entry {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	e, ok, err := readEntry(dir, key)
	if err != nil || !ok {
		t.Fatalf("readEntry(%q) = (_, %v, %v); want ok=true", key, ok, err)
	}
	return e
}
