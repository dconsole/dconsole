package command

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
	_ "github.com/heydon/dconsole/internal/provider"
	_ "github.com/heydon/dconsole/internal/transport"
)

// TestSqlSyncEndToEnd verifies that sql:sync orchestration:
//   1. invokes <bin> sql:dump --gzip on source, captures stdout to temp
//   2. pipes the gunzipped stream into <bin> sql:cli on target
// We don't actually run drush — instead we write tiny shell scripts that
// act as the source/target binaries and verify the round-trip.
func TestSqlSyncEndToEnd(t *testing.T) {
	dir := t.TempDir()
	const sqlBody = "CREATE TABLE x (id INT); INSERT INTO x VALUES (42);"

	// Source fake: emits gzipped sqlBody on stdout when invoked as
	// `<this> sql:dump --gzip`.
	srcScript := filepath.Join(dir, "fake-source")
	writeScript(t, srcScript, `#!/bin/sh
case "$1 $2" in
  "sql:dump --gzip")
    printf '%s' "`+sqlBody+`" | gzip
    ;;
  *)
    echo "unexpected source invocation: $@" >&2
    exit 1 ;;
esac
`)

	// Target fake: on `sql:cli`, copies stdin to a known file.
	targetOut := filepath.Join(dir, "imported.sql")
	tgtScript := filepath.Join(dir, "fake-target")
	writeScript(t, tgtScript, `#!/bin/sh
case "$1" in
  "sql:cli")
    cat > "`+targetOut+`"
    ;;
  *)
    echo "unexpected target invocation: $@" >&2
    exit 1 ;;
esac
`)

	source := &alias.Alias{
		Site: "fake", Env: "src",
		Bin: alias.RemoteBin{Kind: "drush", Path: srcScript},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}
	target := &alias.Alias{
		Site: "fake", Env: "dst",
		Bin: alias.RemoteBin{Kind: "drush", Path: tgtScript},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}

	var out bytes.Buffer
	if err := SqlSync(context.Background(), source, target, &out, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v\noutput:\n%s", err, out.String())
	}

	got, err := os.ReadFile(targetOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sqlBody {
		t.Errorf("round-trip mismatch\n  got  %q\n  want %q", string(got), sqlBody)
	}
}

// TestSqlSyncRejectsNonGzip ensures we fail loudly if the source isn't
// emitting a gzipped dump (operator misconfigured --gzip).
func TestSqlSyncRejectsNonGzip(t *testing.T) {
	dir := t.TempDir()
	srcScript := filepath.Join(dir, "fake-source")
	writeScript(t, srcScript, "#!/bin/sh\necho 'plain text, not gzip'\n")
	tgtScript := filepath.Join(dir, "fake-target")
	writeScript(t, tgtScript, "#!/bin/sh\ncat > /dev/null\n")

	source := &alias.Alias{
		Site: "fake", Env: "src",
		Bin: alias.RemoteBin{Kind: "drush", Path: srcScript},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}
	target := &alias.Alias{
		Site: "fake", Env: "dst",
		Bin: alias.RemoteBin{Kind: "drush", Path: tgtScript},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}
	err := SqlSync(context.Background(), source, target, io.Discard, SqlSyncOpts{})
	if err == nil {
		t.Fatal("expected error for non-gzip dump, got nil")
	}
}

// TestSqlSyncFromProvider verifies the headline workflow: source has a
// provider that pre-fetches a backup, target uses the regular
// transport+drush path. Uses the test-only `fake` provider that just
// reports a pre-baked dump path — exactly what a real provider plugin
// would do — so we don't need any third-party hosting CLI to run this.
func TestSqlSyncFromProvider(t *testing.T) {
	dir := t.TempDir()
	const dumpBody = "CREATE TABLE t(id INT); INSERT INTO t VALUES (42);"

	// Pre-create a gzipped SQL dump at a known path. The fake provider's
	// DumpFor just returns this path; sql:sync's loader gunzips and
	// pipes into the target's drush sql:cli.
	dumpPath := filepath.Join(dir, "dump.sql.gz")
	writeGzipped(t, dumpPath, dumpBody)

	// Fake target drush that writes incoming stdin to a known file
	// under sql:cli, so we can assert the dump made it across.
	importedFile := filepath.Join(dir, "imported.sql")
	fakeDrush := filepath.Join(dir, "fake-drush")
	writeScript(t, fakeDrush, `#!/bin/sh
case "$1" in
  "sql:cli") cat > "`+importedFile+`" ;;
  *) echo "unexpected: $@" >&2; exit 1 ;;
esac
`)

	source := &alias.Alias{
		Site: "ex", Env: "prod",
		Provider: alias.NewProvider("fake", fakeProviderConfig{
			DumpPath: dumpPath,
		}),
		// Transport is still required structurally but won't be used
		// because the provider supplies the dump.
		Transport: alias.NewTransport("exec", nil),
	}
	target := &alias.Alias{
		Site: "ex", Env: "local",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeDrush},
		Transport: alias.NewTransport("exec", nil),
	}

	var out bytes.Buffer
	if err := SqlSync(context.Background(), source, target, &out, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v\noutput:\n%s", err, out.String())
	}

	got, err := os.ReadFile(importedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != dumpBody {
		t.Errorf("imported body = %q, want %q", string(got), dumpBody)
	}
	if !bytes.Contains(out.Bytes(), []byte("via provider fake")) {
		t.Errorf("output should mention provider; got:\n%s", out.String())
	}
}

// TestSqlSyncFromFileStrategy uses sql.source.type=file: source has a
// pre-made gzipped dump at a known path; dconsole `cat`s it via transport.
func TestSqlSyncFromFileStrategy(t *testing.T) {
	dir := t.TempDir()
	const sqlBody = "INSERT INTO t VALUES (7);"
	dumpFile := filepath.Join(dir, "backup.sql.gz")
	writeGzipped(t, dumpFile, sqlBody)

	importedFile := filepath.Join(dir, "imported.sql")
	fakeDrush := filepath.Join(dir, "fake-drush")
	writeScript(t, fakeDrush, `#!/bin/sh
case "$1" in
  "sql:cli") cat > "`+importedFile+`" ;;
  *) echo "unexpected: $@" >&2; exit 1 ;;
esac
`)

	source := &alias.Alias{
		Site: "ex", Env: "prod",
		// Transport is exec so dconsole runs `cat <dumpFile>` locally.
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
		SQL: alias.SQL{
			Source: alias.SQLSource{Type: "file", Path: dumpFile},
		},
	}
	target := &alias.Alias{
		Site: "ex", Env: "local",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeDrush},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}

	var out bytes.Buffer
	if err := SqlSync(context.Background(), source, target, &out, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v\noutput:\n%s", err, out.String())
	}
	got, err := os.ReadFile(importedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sqlBody {
		t.Errorf("imported = %q, want %q", string(got), sqlBody)
	}
}

// TestSqlSyncFromFileStrategy_Plain verifies that a plain (non-gzipped)
// remote dump is gzipped locally before being handed to the importer.
func TestSqlSyncFromFileStrategy_Plain(t *testing.T) {
	dir := t.TempDir()
	const sqlBody = "INSERT INTO t VALUES (99);"
	dumpFile := filepath.Join(dir, "backup.sql")
	if err := os.WriteFile(dumpFile, []byte(sqlBody), 0o644); err != nil {
		t.Fatal(err)
	}

	importedFile := filepath.Join(dir, "imported.sql")
	fakeDrush := filepath.Join(dir, "fake-drush")
	writeScript(t, fakeDrush, `#!/bin/sh
case "$1" in
  "sql:cli") cat > "`+importedFile+`" ;;
  *) exit 1 ;;
esac
`)

	gz := false
	source := &alias.Alias{
		Site: "ex", Env: "prod",
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
		SQL: alias.SQL{
			Source: alias.SQLSource{Type: "file", Path: dumpFile, Gzipped: &gz},
		},
	}
	target := &alias.Alias{
		Site: "ex", Env: "local",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeDrush},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}

	if err := SqlSync(context.Background(), source, target, io.Discard, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v", err)
	}
	got, err := os.ReadFile(importedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sqlBody {
		t.Errorf("imported = %q, want %q", string(got), sqlBody)
	}
}

// TestSqlSyncFromDockerCp swaps out `docker` on PATH for a fake script
// that copies a known dump file. Exercises the docker_cp strategy without
// requiring a real docker daemon.
func TestSqlSyncFromDockerCp(t *testing.T) {
	dir := t.TempDir()
	const sqlBody = "CREATE TABLE t(id INT);"
	srcDump := filepath.Join(dir, "src-dump.sql.gz")
	writeGzipped(t, srcDump, sqlBody)

	// Fake docker: parses `docker cp <container>:<path> <local>` and
	// copies the file from <path> to <local>. Container name is ignored.
	fakeDocker := filepath.Join(dir, "docker")
	writeScript(t, fakeDocker, `#!/bin/sh
[ "$1" = "cp" ] || { echo "unexpected docker cmd: $@" >&2; exit 1; }
src="$2"
dst="$3"
path="${src#*:}"
cp "$path" "$dst"
`)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+":"+origPath)

	importedFile := filepath.Join(dir, "imported.sql")
	fakeDrush := filepath.Join(dir, "fake-drush")
	writeScript(t, fakeDrush, `#!/bin/sh
case "$1" in
  "sql:cli") cat > "`+importedFile+`" ;;
  *) exit 1 ;;
esac
`)

	source := &alias.Alias{
		Site: "ex", Env: "prod",
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
		SQL: alias.SQL{Source: alias.SQLSource{
			Type:      "docker_cp",
			Container: "backup-shipper",
			Path:      srcDump,
		}},
	}
	target := &alias.Alias{
		Site: "ex", Env: "local",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeDrush},
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}

	var out bytes.Buffer
	if err := SqlSync(context.Background(), source, target, &out, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v\noutput:\n%s", err, out.String())
	}
	got, err := os.ReadFile(importedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sqlBody {
		t.Errorf("imported = %q, want %q", string(got), sqlBody)
	}
	if !bytes.Contains(out.Bytes(), []byte("via docker cp")) {
		t.Errorf("expected output to mention docker cp; got:\n%s", out.String())
	}
}

func writeGzipped(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := gzip.NewWriter(f)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

