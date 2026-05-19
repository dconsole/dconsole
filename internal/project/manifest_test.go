package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest_FlatFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dconsole.yml")
	body := `
project: demo
_defaults:
  root: /var/www/html
  bin: { kind: drush }

dev:
  uri: https://dev.example.com
  transport: { type: ssh, ssh: { host: dev.example.com, user: deploy } }

prod:
  uri: https://www.example.com
  transport: { type: ssh, ssh: { host: prod.example.com, user: deploy } }
  policy:
    sync_policy: protected
    allow_sync_to: [dev]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Project != "demo" {
		t.Errorf("project = %q", m.Project)
	}
	if len(m.Envs) != 2 {
		t.Errorf("envs = %v", m.Envs)
	}
	if m.Defaults.Root != "/var/www/html" {
		t.Errorf("defaults.Root = %q", m.Defaults.Root)
	}
	// _defaults should NOT be treated as an env.
	if _, present := m.Envs["_defaults"]; present {
		t.Error("_defaults leaked into Envs")
	}
}

func TestLoadManifest_ResolveEnvAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dconsole.yml")
	body := `
project: demo
_defaults:
  root: /var/www/html
  bin: { kind: drush }
  transport: { type: ssh, ssh: { host: shared.example.com, user: deploy } }

dev:
  uri: https://dev.example.com
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.ResolveEnv("dev")
	if err != nil {
		t.Fatal(err)
	}
	if a.Root != "/var/www/html" || a.Bin.Kind != "drush" {
		t.Errorf("defaults not applied: %+v", a)
	}
	if a.Transport.Type != "ssh" || a.Transport.SSH == nil || a.Transport.SSH.Host != "shared.example.com" {
		t.Errorf("transport defaults not applied: %+v", a.Transport)
	}
	if a.Site != "demo" || a.Env != "dev" {
		t.Errorf("site/env not set: %q %q", a.Site, a.Env)
	}
}

func TestLoadManifest_MissingProjectInfersFromDir(t *testing.T) {
	// When project: is missing, dconsole defaults to the parent
	// directory's basename — same convention as npm/composer/cargo.
	root := t.TempDir()
	dir := filepath.Join(root, "my-cool-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dconsole.yml")
	if err := os.WriteFile(path, []byte("dev:\n  uri: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Project != "my-cool-project" {
		t.Errorf("Project = %q, want %q (basename of containing dir)", m.Project, "my-cool-project")
	}
}

func TestLoadManifest_ExplicitProjectWins(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir-name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dconsole.yml")
	if err := os.WriteFile(path, []byte("project: explicit-name\ndev:\n  uri: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Project != "explicit-name" {
		t.Errorf("Project = %q, want %q (explicit YAML value should win over dir basename)", m.Project, "explicit-name")
	}
}

func TestFindManifest_WalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dconsole.yml"), []byte("project: x\ndev: { uri: y }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := FindManifest(deep)
	if err != nil {
		t.Fatal(err)
	}
	if found != filepath.Join(root, "dconsole.yml") {
		t.Errorf("found %q, want %q", found, filepath.Join(root, "dconsole.yml"))
	}
}

func TestRegistryRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Reset cache since other tests may have populated it.
	registryMu.Lock()
	cachedRegistry = nil
	registryMu.Unlock()

	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register("demo", filepath.Join(tmp, "demo", "dconsole.yml")); err != nil {
		t.Fatal(err)
	}
	// Drop the cache, reload from disk.
	registryMu.Lock()
	cachedRegistry = nil
	registryMu.Unlock()
	r2, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.Lookup("demo"); got == "" {
		t.Errorf("demo not persisted; got %q", got)
	}
	if err := r2.Forget("demo"); err != nil {
		t.Fatal(err)
	}
	if r2.Lookup("demo") != "" {
		t.Error("forget didn't remove the entry")
	}
}
