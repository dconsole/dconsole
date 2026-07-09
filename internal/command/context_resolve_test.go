package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
)

func TestResolveContextual_FullRef(t *testing.T) {
	dir := withProjectManifest(t, `
project: ex
default_env: dev
dev: { uri: https://dev.ex.com, transport: { type: exec, exec: {} } }
prod: { uri: https://www.ex.com, transport: { type: exec, exec: {} } }
`)
	t.Chdir(dir)
	loader := &alias.Loader{}
	WireProjectResolution(loader)

	res, err := ResolveContextual(loader, []string{"@ex.prod", "cr"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Alias.Site != "ex" || res.Alias.Env != "prod" {
		t.Errorf("alias = @%s.%s, want @ex.prod", res.Alias.Site, res.Alias.Env)
	}
	if !equalStrings(res.Rest, []string{"cr"}) {
		t.Errorf("rest = %v, want [cr]", res.Rest)
	}
}

func TestResolveContextual_ShortRef_InfersSite(t *testing.T) {
	dir := withProjectManifest(t, `
project: ex
default_env: dev
dev: { uri: https://dev.ex.com, transport: { type: exec, exec: {} } }
prod: { uri: https://www.ex.com, transport: { type: exec, exec: {} } }
`)
	t.Chdir(dir)
	loader := &alias.Loader{}
	WireProjectResolution(loader)

	res, err := ResolveContextual(loader, []string{"@prod", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Alias.Site != "ex" || res.Alias.Env != "prod" {
		t.Errorf("inferred site/env wrong: @%s.%s", res.Alias.Site, res.Alias.Env)
	}
	if !equalStrings(res.Rest, []string{"status"}) {
		t.Errorf("rest = %v", res.Rest)
	}
}

func TestResolveContextual_ShortRef_NoProjectFails(t *testing.T) {
	t.Chdir(t.TempDir()) // no manifest at-or-above
	loader := &alias.Loader{}
	WireProjectResolution(loader)
	_, err := ResolveContextual(loader, []string{"@prod", "status"})
	if err == nil {
		t.Fatal("expected error when @env used without a project")
	}
	if !strings.Contains(err.Error(), ".dconsole.yml") {
		t.Errorf("error should mention missing manifest; got %v", err)
	}
}

func TestResolveContextual_NoPrefix_UsesProjectDefault(t *testing.T) {
	dir := withProjectManifest(t, `
project: ex
default_env: dev
dev: { uri: https://dev.ex.com, transport: { type: exec, exec: {} } }
prod: { uri: https://www.ex.com, transport: { type: exec, exec: {} } }
`)
	t.Chdir(dir)
	loader := &alias.Loader{}
	WireProjectResolution(loader)
	res, err := ResolveContextual(loader, []string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Alias.Site != "ex" || res.Alias.Env != "dev" {
		t.Errorf("default resolution wrong: @%s.%s", res.Alias.Site, res.Alias.Env)
	}
	if !equalStrings(res.Rest, []string{"status"}) {
		t.Errorf("rest = %v (should keep all args since none was an alias)", res.Rest)
	}
}

func TestResolveContextual_NoPrefix_NoDefaultEnvFails(t *testing.T) {
	dir := withProjectManifest(t, `
project: ex
dev: { uri: https://dev.ex.com, transport: { type: exec, exec: {} } }
`)
	t.Chdir(dir)
	loader := &alias.Loader{}
	WireProjectResolution(loader)
	_, err := ResolveContextual(loader, []string{"status"})
	if err == nil {
		t.Fatal("expected error when no default_env set")
	}
}

func TestResolveContextual_NoPrefix_NoProject_LocalAlias(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	loader := &alias.Loader{}
	WireProjectResolution(loader)
	res, err := ResolveContextual(loader, []string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Alias.Site != "self" || res.Alias.Env != "local" {
		t.Errorf("expected synthetic @self.local; got @%s.%s", res.Alias.Site, res.Alias.Env)
	}
	if res.Alias.Handler.Type != "exec" {
		t.Errorf("local alias should use exec handler; got %q", res.Alias.Handler.Type)
	}
	if res.Alias.Root != dir {
		t.Errorf("local alias root should be cwd; got %q", res.Alias.Root)
	}
}

func withProjectManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dconsole.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
