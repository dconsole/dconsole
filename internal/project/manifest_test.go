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

// TestLoadManifest_OverrideChangesDefaultEnv covers the deploy-time
// pattern: server has dconsole.yml describing the project's envs and
// a tiny dconsole.override.yml that flips default_env to whatever this
// server represents. No alias prefix needed when you're on the box.
func TestLoadManifest_OverrideChangesDefaultEnv(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dconsole.yml")
	override := filepath.Join(dir, "dconsole.override.yml")
	mustWrite(t, base, `
project: demo
default_env: dev
dev:
  uri: https://dev.example.com
  transport: { type: ssh, ssh: { host: dev } }
prod:
  uri: https://www.example.com
  transport: { type: ssh, ssh: { host: prod } }
`)
	mustWrite(t, override, "default_env: prod\n")

	m, err := LoadManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	if m.DefaultEnv != "prod" {
		t.Errorf("DefaultEnv = %q, want %q (override should win)", m.DefaultEnv, "prod")
	}
	if m.Project != "demo" {
		t.Errorf("Project = %q, want demo (base owns the name)", m.Project)
	}
	if m.OverridePath == "" {
		t.Errorf("OverridePath should be set when override file is loaded")
	}
}

// TestLoadManifest_OverrideSwitchesEnvTransport covers the
// "deployed inside a container" pattern: the same env that's normally
// reached via ssh becomes a local exec transport.
func TestLoadManifest_OverrideSwitchesEnvTransport(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dconsole.yml")
	override := filepath.Join(dir, "dconsole.override.yml")
	mustWrite(t, base, `
project: demo
prod:
  uri: https://www.example.com
  root: /var/www/myapp/web
  transport:
    type: ssh
    ssh:
      host: prod.example.com
      user: deploy
`)
	mustWrite(t, override, `
prod:
  transport:
    type: exec
    exec:
      dir: /var/www/myapp/web
`)

	m, err := LoadManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	prod, err := m.ResolveEnv("prod")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Transport.Type != "exec" {
		t.Errorf("Transport.Type = %q, want exec (override should replace ssh)", prod.Transport.Type)
	}
	if prod.URI != "https://www.example.com" {
		t.Errorf("URI lost: %q (base value should survive)", prod.URI)
	}
	if prod.Root != "/var/www/myapp/web" {
		t.Errorf("Root lost: %q", prod.Root)
	}
}

// TestLoadManifest_OverrideAddsNewEnv lets ops introduce a new env via
// override (e.g. a container-local "self" alias) without touching the
// shared dconsole.yml.
func TestLoadManifest_OverrideAddsNewEnv(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dconsole.yml")
	override := filepath.Join(dir, "dconsole.override.yml")
	mustWrite(t, base, `
project: demo
prod:
  uri: https://www.example.com
  transport: { type: ssh, ssh: { host: prod } }
`)
	mustWrite(t, override, `
self:
  uri: https://internal.example.com
  root: /var/www/myapp/web
  transport:
    type: exec
    exec:
      dir: /var/www/myapp/web
`)

	m, err := LoadManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Envs["self"]; !ok {
		t.Errorf("override env 'self' not added; have: %v", m.EnvNames())
	}
	if _, ok := m.Envs["prod"]; !ok {
		t.Errorf("base env 'prod' lost after override merge")
	}
}

// TestLoadManifest_OverrideAbsentNoError confirms an absent override
// file is silently OK (no error, OverridePath stays empty).
func TestLoadManifest_OverrideAbsentNoError(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dconsole.yml")
	mustWrite(t, base, "project: demo\nprod: { uri: x }\n")
	m, err := LoadManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	if m.OverridePath != "" {
		t.Errorf("OverridePath = %q, want empty", m.OverridePath)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
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

// TestFindManifest_PrefersDotfile — when both .dconsole.yml and
// dconsole.yml exist in the same directory, the walker picks the
// dotfile. Regression guard so future refactors of ManifestNames
// keep the modern-default ordering.
func TestFindManifest_PrefersDotfile(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".dconsole.yml")
	plain := filepath.Join(root, "dconsole.yml")
	if err := os.WriteFile(dot, []byte("project: dot\ndev: { uri: y }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("project: plain\ndev: { uri: y }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := FindManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if found != dot {
		t.Errorf("found %q, want dotfile %q", found, dot)
	}
}

// TestFindManifest_LegacyOnly — when only the plain dconsole.yml
// exists (no dotfile), the walker still finds it. Confirms the
// backwards-compat path is unchanged.
func TestFindManifest_LegacyOnly(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "dconsole.yml")
	if err := os.WriteFile(plain, []byte("project: x\ndev: { uri: y }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := FindManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if found != plain {
		t.Errorf("found %q, want %q", found, plain)
	}
}

// TestLoadManifest_OverrideDotfilePreferred — when both the dotfile
// override (.dconsole.override.yml) and the legacy override exist
// next to the manifest, the dotfile wins.
func TestLoadManifest_OverrideDotfilePreferred(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".dconsole.yml")
	if err := os.WriteFile(base, []byte("project: x\ndev: { uri: y }\nstage: { uri: y }\ndefault_env: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Dotfile override → default_env: stage
	dotOv := filepath.Join(dir, ".dconsole.override.yml")
	if err := os.WriteFile(dotOv, []byte("default_env: stage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Legacy override → default_env: dev (should be ignored)
	plainOv := filepath.Join(dir, "dconsole.override.yml")
	if err := os.WriteFile(plainOv, []byte("default_env: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	if m.DefaultEnv != "stage" {
		t.Errorf("DefaultEnv = %q, want %q (dotfile override should win)", m.DefaultEnv, "stage")
	}
	if m.OverridePath != dotOv {
		t.Errorf("OverridePath = %q, want dotfile override %q", m.OverridePath, dotOv)
	}
}

// resetRegistryCache drops the module-level cache so a test that
// changes HOME sees fresh disk state.
func resetRegistryCache() {
	registryMu.Lock()
	cachedRegistry = nil
	registryMu.Unlock()
}

func TestRegistryRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetRegistryCache()

	// Manifest that Register() will point at. Register does an existence
	// check on the pre-existing entry to detect conflicts, but the
	// target itself just needs to be reachable via filepath.Abs.
	target := filepath.Join(tmp, "demo", ".dconsole.yml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("project: demo\ndev: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register("demo", target); err != nil {
		t.Fatal(err)
	}

	// The entry is a symlink at ~/.dconsole/projects/demo.yml → target.
	entry := filepath.Join(tmp, ".dconsole", "projects", "demo.yml")
	info, err := os.Lstat(entry)
	if err != nil {
		t.Fatalf("Register didn't create %s: %v", entry, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink at %s; got mode %v", entry, info.Mode())
	}

	// Drop the cache, reload from disk — Lookup should still find it.
	resetRegistryCache()
	r2, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.Lookup("demo"); got != target {
		t.Errorf("Lookup(demo) = %q, want %q", got, target)
	}

	if err := r2.Forget("demo"); err != nil {
		t.Fatal(err)
	}
	if r2.Lookup("demo") != "" {
		t.Error("forget didn't remove the entry")
	}
	if _, err := os.Lstat(entry); err == nil {
		t.Errorf("Forget didn't remove %s", entry)
	}
}

// TestRegistryDropInFile — a real regular file at
// ~/.dconsole/projects/<name>.yml is treated as a standalone manifest
// (no local checkout needed). The lookup returns the file's own path.
// This is the "run drush without local Drupal source" workflow.
func TestRegistryDropInFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetRegistryCache()

	dir := filepath.Join(tmp, ".dconsole", "projects")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dropIn := filepath.Join(dir, "shared.yml")
	if err := os.WriteFile(dropIn, []byte(
		"project: shared\ndefault_env: prod\nprod:\n  handler: { type: ssh, ssh: { host: shared.example.com, user: deploy } }\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Lookup("shared"); got != dropIn {
		t.Errorf("drop-in lookup: got %q, want %q (the file itself)", got, dropIn)
	}
	// End-to-end: LoadManifestByName should read it and find the ssh handler.
	m, err := LoadManifestByName("shared", r.Lookup("shared"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.ResolveEnv("prod")
	if err != nil {
		t.Fatal(err)
	}
	if a.Handler.Type != "ssh" || a.Handler.SSH == nil || a.Handler.SSH.Host != "shared.example.com" {
		t.Errorf("drop-in handler did not resolve: %+v", a.Handler)
	}
}

// TestRegistryLegacyFileFallback — pre-v0.5.13 projects.yml entries
// are still readable. Directory entries win on conflict.
func TestRegistryLegacyFileFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetRegistryCache()

	// Legacy file only.
	legacyPath := filepath.Join(tmp, ".dconsole", "projects.yml")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(
		"projects:\n  oldproj: /nonexistent/old/dconsole.yml\n  clash: /nonexistent/legacy-wins.yml\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	// Directory entry that clashes with the legacy "clash" name.
	regDir := filepath.Join(tmp, ".dconsole", "projects")
	if err := os.MkdirAll(regDir, 0o700); err != nil {
		t.Fatal(err)
	}
	newerPath := filepath.Join(tmp, "clash", ".dconsole.yml")
	if err := os.MkdirAll(filepath.Dir(newerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newerPath, []byte("project: clash\ndev: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newerPath, filepath.Join(regDir, "clash.yml")); err != nil {
		t.Fatal(err)
	}

	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Lookup("oldproj"); got != "/nonexistent/old/dconsole.yml" {
		t.Errorf("legacy-only entry lost: %q", got)
	}
	if got := r.Lookup("clash"); got != newerPath {
		t.Errorf("directory entry should win over legacy for %q: got %q, want %q", "clash", got, newerPath)
	}
}

// TestRegistryOverridePrecedence — project-side .dconsole.override.yml
// wins over registry-side ~/.dconsole/projects/<name>.override.yml.
// The registry-side override is only applied when the project has none
// (including drop-in entries with no project checkout at all).
func TestRegistryOverridePrecedence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetRegistryCache()

	// Project checkout with its own .dconsole.override.yml.
	checkout := filepath.Join(tmp, "checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(checkout, ".dconsole.yml")
	if err := os.WriteFile(manifest, []byte(
		"project: withproj\ndefault_env: dev\ndev: {}\nstage: {}\nprod: {}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	projectOv := filepath.Join(checkout, ".dconsole.override.yml")
	if err := os.WriteFile(projectOv, []byte("default_env: stage\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Registry entry + a REGISTRY-side override that would flip to prod.
	regDir := filepath.Join(tmp, ".dconsole", "projects")
	if err := os.MkdirAll(regDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(manifest, filepath.Join(regDir, "withproj.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(regDir, "withproj.override.yml"), []byte("default_env: prod\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Project-side override wins → default_env should be "stage".
	m, err := LoadManifestByName("withproj", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.DefaultEnv != "stage" {
		t.Errorf("project-side override should win: DefaultEnv=%q, want %q", m.DefaultEnv, "stage")
	}

	// Drop-in: no project-side override → registry-side applies.
	// Create a second project with NO project-side override.
	checkout2 := filepath.Join(tmp, "checkout2")
	if err := os.MkdirAll(checkout2, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest2 := filepath.Join(checkout2, ".dconsole.yml")
	if err := os.WriteFile(manifest2, []byte(
		"project: dropin\ndefault_env: dev\ndev: {}\nstage: {}\nprod: {}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(manifest2, filepath.Join(regDir, "dropin.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(regDir, "dropin.override.yml"), []byte("default_env: prod\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m2, err := LoadManifestByName("dropin", manifest2)
	if err != nil {
		t.Fatal(err)
	}
	if m2.DefaultEnv != "prod" {
		t.Errorf("registry-side override should apply when project has none: DefaultEnv=%q, want %q", m2.DefaultEnv, "prod")
	}
}

// TestRegistryConflictRefused — Register refuses to overwrite a live
// entry pointing at a different path (user must Forget one side first).
// Dangling entries CAN be silently replaced.
func TestRegistryConflictRefused(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetRegistryCache()

	// Register one path first.
	target1 := filepath.Join(tmp, "one", ".dconsole.yml")
	if err := os.MkdirAll(filepath.Dir(target1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target1, []byte("project: proj\ndev: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register("proj", target1); err != nil {
		t.Fatal(err)
	}

	// Different existing path → refuse.
	target2 := filepath.Join(tmp, "two", ".dconsole.yml")
	if err := os.MkdirAll(filepath.Dir(target2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target2, []byte("project: proj\ndev: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("proj", target2); err == nil {
		t.Error("expected conflict error when re-registering same name at a different live path")
	}

	// Delete target1 → the entry is now dangling → Register should replace.
	if err := os.RemoveAll(filepath.Dir(target1)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("proj", target2); err != nil {
		t.Errorf("expected dangling entry to be replaceable, got: %v", err)
	}
	if got := r.Lookup("proj"); got != target2 {
		t.Errorf("Lookup after replace: got %q, want %q", got, target2)
	}
}
