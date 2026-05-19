package provider

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
)

// TestIronstarDumpFor uses a fake `iron` script that writes a gzipped
// SQL file into --save-path, mimicking the real CLI's behavior.
func TestIronstarDumpFor(t *testing.T) {
	dir := t.TempDir()
	fakeIron := filepath.Join(dir, "iron-fake")
	const dumpBody = "CREATE TABLE x (id INT);"
	mustScript(t, fakeIron, `#!/bin/sh
# Parse --save-path out of the args.
save=""
while [ $# -gt 0 ]; do
  case "$1" in
    --save-path) save="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -z "$save" ]; then
  echo "fake-iron: no --save-path given" >&2; exit 2
fi
mkdir -p "$save"
printf '`+dumpBody+`' | gzip > "$save/database-default.sql.gz"
echo "ok"
`)

	a := &alias.Alias{
		Site: "ex", Env: "prod",
		Provider: alias.Provider{
			Type: "ironstar",
			Ironstar: &alias.IronstarProvider{
				Subscription: "example-prod",
				Environment:  "production",
			},
		},
	}
	p := &ironstar{cfg: a.Provider.Ironstar, ironBin: fakeIron}

	path, cleanup, err := p.DumpFor(context.Background(), a)
	if err != nil {
		t.Fatalf("DumpFor: %v", err)
	}
	defer cleanup()

	if filepath.Ext(path) != ".gz" {
		t.Errorf("returned path should end in .gz, got %q", path)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(gz)
	if string(got) != dumpBody {
		t.Errorf("dump body = %q, want %q", string(got), dumpBody)
	}
}

func TestIronstarLoadForReturnsNotSupported(t *testing.T) {
	p := &ironstar{cfg: &alias.IronstarProvider{Subscription: "s", Environment: "e"}, ironBin: "iron"}
	err := p.LoadFor(context.Background(), &alias.Alias{}, "/tmp/dump.sql.gz")
	if err != ErrNotSupported {
		t.Errorf("LoadFor err = %v, want ErrNotSupported", err)
	}
}

func TestIronstarRegistryWiresFromAlias(t *testing.T) {
	a := &alias.Alias{
		Provider: alias.NewProvider("ironstar", alias.IronstarProvider{
			Subscription: "s",
			Environment:  "e",
		}),
	}
	p, err := For(a)
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("provider is nil")
	}
	if p.Name() != "ironstar" {
		t.Errorf("name = %q, want ironstar", p.Name())
	}
}

func mustScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
