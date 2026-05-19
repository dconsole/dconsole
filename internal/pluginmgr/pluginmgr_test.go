package pluginmgr

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeTarball writes a tar.gz containing a single executable file named
// "dconsole-<name>" with the given content. Returns the tarball path
// and its sha256.
func makeTarball(t *testing.T, name, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	tgz := filepath.Join(dir, "dconsole-"+name+".tgz")
	f, err := os.Create(tgz)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\n" + content + "\n")
	hdr := &tar.Header{
		Name:     "dconsole-" + name,
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	// Compute sha256
	data, err := os.ReadFile(tgz)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return tgz, hex.EncodeToString(sum[:])
}

func TestInstall_FromPathPlacesBinary(t *testing.T) {
	tgz, _ := makeTarball(t, "demo", "echo from demo")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	bin, err := Install(InstallOpts{Path: tgz})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	dir, _ := PluginDir()
	want := filepath.Join(dir, "dconsole-demo")
	if bin != want {
		t.Errorf("install path = %q, want %q", bin, want)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("installed binary is not executable: mode %v", info.Mode())
	}
}

func TestInstall_FromPathWithSHA256(t *testing.T) {
	tgz, sum := makeTarball(t, "demo", "echo")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// Correct sha256 succeeds
	if _, err := Install(InstallOpts{Path: tgz, SHA256: sum}); err != nil {
		t.Fatalf("Install with correct sha256: %v", err)
	}

	// Reset target dir; wrong sha256 fails
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, err := Install(InstallOpts{Path: tgz, SHA256: "0000000000000000000000000000000000000000000000000000000000000000"})
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestInstall_OptsValidation(t *testing.T) {
	cases := []struct {
		name    string
		opts    InstallOpts
		wantErr string
	}{
		{"no source", InstallOpts{}, "exactly one"},
		{"two sources", InstallOpts{Name: "a", Path: "/tmp/x"}, "exactly one"},
		{"url without sha", InstallOpts{URL: "https://e.com/x.tgz"}, "requires --sha256"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Install(c.opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q missing %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestListInstalled_EmptyAndPopulated(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	list, err := ListInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %v", list)
	}

	// Drop two fake binaries
	dir, _ := PluginDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"dconsole-alpha", "dconsole-beta", "not-a-plugin"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	list, err = ListInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 plugins, got %v", list)
	}
	// list returned names without the "dconsole-" prefix
	for _, n := range list {
		if strings.HasPrefix(n, "dconsole-") {
			t.Errorf("name %q still has dconsole- prefix", n)
		}
	}
}

func TestRemove_DeletesBinary(t *testing.T) {
	tgz, _ := makeTarball(t, "demo", "echo")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := Install(InstallOpts{Path: tgz}); err != nil {
		t.Fatal(err)
	}
	if err := Remove("demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	dir, _ := PluginDir()
	if _, err := os.Stat(filepath.Join(dir, "dconsole-demo")); !os.IsNotExist(err) {
		t.Errorf("binary still exists after Remove")
	}
	// Removing again surfaces a clear error
	if err := Remove("demo"); err == nil {
		t.Error("expected error removing twice")
	}
}

func TestSelectPlatform_ReturnsHostKey(t *testing.T) {
	hostKey := runtime.GOOS + "-" + runtime.GOARCH
	v := &Version{
		Platforms: map[string]Platform{
			hostKey:           {URL: "https://e.com/host.tgz", SHA256: "x"},
			"unrelated-arch":  {URL: "https://e.com/other.tgz", SHA256: "y"},
		},
	}
	p, key, err := SelectPlatform(v)
	if err != nil {
		t.Fatal(err)
	}
	if key != hostKey || p.URL != "https://e.com/host.tgz" {
		t.Errorf("got %s, %v; want %s for host", key, p, hostKey)
	}
}

// streamHashOf is a smoke test for the sha256 helper to make sure
// computeSHA + verifySHA agree on the same byte sequence.
func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "blob")
	body := []byte("hello sha256 world\n")
	if err := os.WriteFile(f, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write(body)
	sum := hex.EncodeToString(h.Sum(nil))
	if err := verifySHA256(f, sum); err != nil {
		t.Errorf("verifySHA256 correct sum: %v", err)
	}
	if err := verifySHA256(f, "0"); err == nil {
		t.Error("expected mismatch error")
	}
}

// Stream-compose ensures the install path's gzip-tar reader handles a
// realistic minimal archive without panicking.
func TestExtractTarballTo_RoundTrip(t *testing.T) {
	tgz, _ := makeTarball(t, "roundtrip", "x")
	dst := t.TempDir()
	bin, err := extractTarballTo(tgz, dst)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(bin) != "dconsole-roundtrip" {
		t.Errorf("bin = %q, want dconsole-roundtrip", filepath.Base(bin))
	}
	// Ensure no traversal: file lives directly inside dst.
	if filepath.Dir(bin) != dst {
		t.Errorf("bin dir = %q, want %q", filepath.Dir(bin), dst)
	}
}

func TestExtractTarballTo_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	tgz := filepath.Join(dir, "evil.tgz")
	f, err := os.Create(tgz)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("malicious")
	if err := tw.WriteHeader(&tar.Header{Name: "../../escape", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Writer(tw).Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := extractTarballTo(tgz, t.TempDir()); err == nil {
		t.Fatal("expected error for path traversal entry")
	}
}
