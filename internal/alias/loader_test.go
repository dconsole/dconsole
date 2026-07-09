package alias

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRef(t *testing.T) {
	cases := []struct {
		in       string
		wantSite string
		wantEnv  string
		wantOK   bool
	}{
		{"@example.dev", "example", "dev", true},
		{"@self.live", "self", "live", true},
		{"@x.y", "x", "y", true},
		{"@example", "", "", false},
		{"example.dev", "", "", false},
		{"@.dev", "", "", false},
		{"@example.", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		site, env, ok := ParseRef(c.in)
		if site != c.wantSite || env != c.wantEnv || ok != c.wantOK {
			t.Errorf("ParseRef(%q) = (%q,%q,%v); want (%q,%q,%v)", c.in, site, env, ok, c.wantSite, c.wantEnv, c.wantOK)
		}
	}
}

func TestResolveDefaultsMerge(t *testing.T) {
	dir := t.TempDir()
	yaml := `
_defaults:
  root: /var/www/html
  bin: { kind: drush }
  transport:
    type: ssh
    ssh: { host: shared.example.com, user: deploy }

dev:
  uri: https://dev.example.com

stage:
  uri: https://stage.example.com
  bin: { kind: drupal, path: /app/vendor/bin/drupal }
  transport:
    type: ssh
    ssh: { host: stage.example.com, user: deploy }
`
	path := filepath.Join(dir, "example.site.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Loader{SearchPaths: []string{dir}}

	dev, err := l.Resolve("example", "dev")
	if err != nil {
		t.Fatalf("resolve dev: %v", err)
	}
	if dev.Root != "/var/www/html" {
		t.Errorf("dev.Root inherited badly: %q", dev.Root)
	}
	if dev.Bin.Kind != "drush" {
		t.Errorf("dev.Bin.Kind inherited badly: %q", dev.Bin.Kind)
	}
	if dev.Transport.Type != "ssh" || dev.Transport.SSH == nil || dev.Transport.SSH.Host != "shared.example.com" {
		t.Errorf("dev.Transport inherited badly: %+v", dev.Transport)
	}
	if dev.URI != "https://dev.example.com" {
		t.Errorf("dev.URI overridden badly: %q", dev.URI)
	}

	stage, err := l.Resolve("example", "stage")
	if err != nil {
		t.Fatalf("resolve stage: %v", err)
	}
	if stage.Bin.Kind != "drupal" {
		t.Errorf("stage.Bin.Kind should win over defaults: %q", stage.Bin.Kind)
	}
	if stage.Bin.Path != "/app/vendor/bin/drupal" {
		t.Errorf("stage.Bin.Path: %q", stage.Bin.Path)
	}
	if stage.Transport.SSH == nil || stage.Transport.SSH.Host != "stage.example.com" {
		t.Errorf("stage transport should override defaults: %+v", stage.Transport.SSH)
	}
	if stage.Root != "/var/www/html" {
		t.Errorf("stage.Root should still inherit: %q", stage.Root)
	}
}

// TestMergeDefaults_HandlerInherits — the new handler: schema inherits
// from _defaults the same way legacy transport: does. Regression guard
// for the pre-v0.5.12 gap where _defaults.handler was silently dropped,
// which caused CI-only failures (local dev was masked by an override
// file that gave each env its own handler).
func TestMergeDefaults_HandlerInherits(t *testing.T) {
	defaults := Alias{
		Root: "/var/www/html",
		Handler: Handler{
			Type: "ssh",
			SSH:  &SSHTransport{Host: "default.example.com", User: "deploy"},
		},
	}
	envEmpty := Alias{}
	merged := MergeDefaults(defaults, envEmpty)
	if merged.Handler.Type != "ssh" {
		t.Fatalf("empty env should inherit defaults.Handler; got %+v", merged.Handler)
	}
	if merged.Handler.SSH == nil || merged.Handler.SSH.Host != "default.example.com" {
		t.Errorf("SSH block not inherited: %+v", merged.Handler.SSH)
	}
	if merged.Root != "/var/www/html" {
		t.Errorf("Root not inherited: %q", merged.Root)
	}
}

// TestMergeDefaults_HandlerExplicitWins — an env that declares its own
// handler must not be overwritten by defaults.
func TestMergeDefaults_HandlerExplicitWins(t *testing.T) {
	defaults := Alias{
		Handler: Handler{Type: "ssh", SSH: &SSHTransport{Host: "default.example.com", User: "deploy"}},
	}
	env := Alias{
		Handler: Handler{Type: "ssh", SSH: &SSHTransport{Host: "prod.example.com", User: "root"}},
	}
	merged := MergeDefaults(defaults, env)
	if merged.Handler.SSH.Host != "prod.example.com" || merged.Handler.SSH.User != "root" {
		t.Errorf("env-declared handler must win over defaults; got %+v", merged.Handler.SSH)
	}
}

// TestMergeDefaults_HandlersListInherits — the explicit-list form
// (handlers: [...]) inherits the same way handler: does.
func TestMergeDefaults_HandlersListInherits(t *testing.T) {
	defaults := Alias{
		Handlers: []Handler{
			{Type: "ssh", SSH: &SSHTransport{Host: "jump.example.com", User: "deploy"}},
			{Type: "docker", Docker: &DockerTransport{Container: "app"}},
		},
	}
	env := Alias{}
	merged := MergeDefaults(defaults, env)
	if len(merged.Handlers) != 2 {
		t.Fatalf("empty env should inherit defaults.Handlers; got len=%d", len(merged.Handlers))
	}
	if merged.Handlers[0].SSH.Host != "jump.example.com" {
		t.Errorf("first handler wrong: %+v", merged.Handlers[0])
	}
}

// TestMergeDefaults_HandlerDoesNotMixSchemas — if the env explicitly
// chose handlers: (list form), defaults' handler: (single form) must
// NOT be smuggled in — that would produce a "mixes handler: and
// handlers:" error at LegacyChain time.
func TestMergeDefaults_HandlerDoesNotMixSchemas(t *testing.T) {
	defaults := Alias{
		Handler: Handler{Type: "ssh", SSH: &SSHTransport{Host: "default.example.com"}},
	}
	env := Alias{
		Handlers: []Handler{{Type: "exec"}},
	}
	merged := MergeDefaults(defaults, env)
	if merged.Handler.Type != "" {
		t.Errorf("env used handlers: — defaults.Handler must not be added on top; got Handler=%+v", merged.Handler)
	}
	if _, err := (&merged).LegacyChain(); err != nil {
		t.Errorf("LegacyChain must not error after merge; got: %v", err)
	}
}

func TestResolveBogusEnv(t *testing.T) {
	dir := t.TempDir()
	yaml := "dev:\n  uri: https://dev.example.com\n"
	if err := os.WriteFile(filepath.Join(dir, "example.site.yml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Loader{SearchPaths: []string{dir}}
	if _, err := l.Resolve("example", "ghost"); err == nil {
		t.Error("expected error for missing env, got nil")
	}
}
