package assetscache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	writeBlob(t, src, body)
	path, err := Put(key, src, meta)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPutGet_RoundTrip(t *testing.T) {
	useTempDir(t)
	const body = "FAKE-TARBALL"
	mustPut(t, "abc", body, Metadata{Site: "demo", Env: "prod", Strategy: "provider:skpr", Pathspec: "%files"})
	path, ok, err := Get("abc", DefaultTTL)
	if err != nil || !ok {
		t.Fatalf("Get(abc) = (%q, %v, %v); want ok", path, ok, err)
	}
	read, _ := os.ReadFile(path)
	if string(read) != body {
		t.Errorf("body mismatch: got %q want %q", string(read), body)
	}
}

func TestGet_StaleExpired(t *testing.T) {
	useTempDir(t)
	mustPut(t, "stale", "x", Metadata{StoredAt: time.Now().Add(-1 * time.Hour)})
	if _, ok, _ := Get("stale", time.Minute); ok {
		t.Error("expected stale to return ok=false")
	}
}

func TestKeyFor_DifferentInputsProduceDifferentKeys(t *testing.T) {
	keys := []KeyInputs{
		{Site: "a", Env: "prod", Strategy: "provider:skpr", Pathspec: "%files"},
		{Site: "a", Env: "stage", Strategy: "provider:skpr", Pathspec: "%files"},
		{Site: "b", Env: "prod", Strategy: "provider:skpr", Pathspec: "%files"},
		{Site: "a", Env: "prod", Strategy: "provider:ironstar", Pathspec: "%files"},
		{Site: "a", Env: "prod", Strategy: "provider:skpr", Pathspec: "%private"},
	}
	seen := map[string]int{}
	for i, k := range keys {
		hash := KeyFor(k)
		if prev, dup := seen[hash]; dup {
			t.Errorf("inputs %d and %d collide on key %s", prev, i, hash)
		}
		seen[hash] = i
	}
}

func TestKeyFor_DefaultsToFiles(t *testing.T) {
	withDefault := KeyFor(KeyInputs{Site: "a", Env: "prod", Strategy: "x", Pathspec: ""})
	explicit := KeyFor(KeyInputs{Site: "a", Env: "prod", Strategy: "x", Pathspec: "%files"})
	if withDefault != explicit {
		t.Errorf("empty pathspec should default to %%files; got %s vs %s", withDefault, explicit)
	}
}

func TestInvalidate_RemovesBothFiles(t *testing.T) {
	dir := useTempDir(t)
	mustPut(t, "drop", "x", Metadata{Site: "demo"})
	for _, p := range []string{
		filepath.Join(dir, "drop.tar.gz"),
		filepath.Join(dir, "drop.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}
	if err := Invalidate("drop"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(dir, "drop.tar.gz"),
		filepath.Join(dir, "drop.json"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s gone, got err=%v", p, err)
		}
	}
}

func TestList_NewestFirst(t *testing.T) {
	useTempDir(t)
	mustPut(t, "old", "x", Metadata{StoredAt: time.Now().Add(-2 * time.Hour)})
	mustPut(t, "new", "y", Metadata{StoredAt: time.Now()})
	entries, _ := List()
	if len(entries) != 2 || entries[0].Key != "new" || entries[1].Key != "old" {
		got := []string{}
		for _, e := range entries {
			got = append(got, e.Key)
		}
		t.Errorf("got %v, want [new, old]", got)
	}
}

func TestClear_Filter(t *testing.T) {
	useTempDir(t)
	mustPut(t, "keep", "x", Metadata{Site: "keep-site"})
	mustPut(t, "drop1", "y", Metadata{Site: "drop-site"})
	mustPut(t, "drop2", "z", Metadata{Site: "drop-site"})
	removed, err := Clear(func(e Entry) bool { return e.Meta.Site == "drop-site" })
	if err != nil || removed != 2 {
		t.Errorf("Clear: removed=%d err=%v; want 2,nil", removed, err)
	}
	rest, _ := List()
	if len(rest) != 1 || rest[0].Key != "keep" {
		t.Errorf("after Clear got %d entries; want 1 ('keep')", len(rest))
	}
}

func TestParseTTL(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", DefaultTTL, true},
		{"6h", 6 * time.Hour, true},
		{"30m", 30 * time.Minute, true},
		{"bogus", 0, false},
	}
	for _, c := range cases {
		got, err := ParseTTL(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ParseTTL(%q): err=%v, ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("ParseTTL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPut_SuffixSafeForConcurrency(t *testing.T) {
	// Smoke test: each Put uses a unique .tmp suffix so back-to-back
	// puts at the same key don't collide on the .tmp filename.
	useTempDir(t)
	src1 := filepath.Join(t.TempDir(), "s1")
	src2 := filepath.Join(t.TempDir(), "s2")
	body := strings.Repeat("A", 4096)
	os.WriteFile(src1, []byte(body), 0o644)
	os.WriteFile(src2, []byte(body), 0o644)
	if _, err := Put("k", src1, Metadata{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Put("k", src2, Metadata{}); err != nil {
		t.Fatalf("second Put on same key should overwrite atomically: %v", err)
	}
}
