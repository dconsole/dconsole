package command

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	_ "github.com/dconsole/dconsole/internal/transport"
)

func TestRsyncLocalToLocal(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// Lay down a small tree.
	mustWrite(t, filepath.Join(src, "a.txt"), "alpha")
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "beta")

	srcE := &Endpoint{Local: src}
	dstE := &Endpoint{Local: dst}

	if err := Rsync(context.Background(), srcE, dstE, io.Discard, nil, RsyncOpts{}); err != nil {
		t.Fatalf("Rsync: %v", err)
	}
	mustRead(t, filepath.Join(dst, "a.txt"), "alpha")
	mustRead(t, filepath.Join(dst, "sub", "b.txt"), "beta")
}

func TestRsyncAliasToLocal_FilesToken(t *testing.T) {
	dir := t.TempDir()
	// Lay down "remote" content under a directory tree the fake status
	// will point %files at.
	remoteRoot := filepath.Join(dir, "remote-root")
	remoteFiles := filepath.Join(remoteRoot, "sites", "default", "files")
	mustWrite(t, filepath.Join(remoteFiles, "logo.png"), "PNGDATA")
	mustWrite(t, filepath.Join(remoteFiles, "doc.pdf"), "PDFDATA")

	// Fake bin: `status --format=json` returns absolute paths for files.
	// The fake also handles invocation as the absolute path (the
	// remotebin layer passes the resolved absolute path).
	fakeBin := filepath.Join(dir, "fake-drush")
	// Strip leading --root= and --uri= args (sitepath now prepends them
	// so drush 8 can bootstrap). Then dispatch on the real subcommand.
	script := `#!/bin/sh
while [ "${1#--root=}" != "$1" ] || [ "${1#--uri=}" != "$1" ]; do shift; done
case "$1" in
  status)
    printf '{"root":"` + remoteRoot + `","files":"` + remoteFiles + `","site":"sites/default"}'
    ;;
  *)
    echo "unexpected drush invocation: $@" >&2; exit 1 ;;
esac
`
	mustWrite(t, fakeBin, script)
	if err := os.Chmod(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}

	a := &alias.Alias{
		Site: "ex", Env: "src",
		Root: remoteRoot,
		Bin: alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}

	// Use a fresh HOME so sitepath disk cache doesn't pollute the test.
	t.Setenv("HOME", t.TempDir())

	dst := t.TempDir()
	srcE := &Endpoint{Alias: a, PathSpec: "%files"}
	dstE := &Endpoint{Local: dst}

	var out bytes.Buffer
	if err := Rsync(context.Background(), srcE, dstE, &out, nil, RsyncOpts{Verbose: true}); err != nil {
		t.Fatalf("Rsync: %v\noutput:\n%s", err, out.String())
	}
	mustRead(t, filepath.Join(dst, "logo.png"), "PNGDATA")
	mustRead(t, filepath.Join(dst, "doc.pdf"), "PDFDATA")
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, string(got), want)
	}
}
