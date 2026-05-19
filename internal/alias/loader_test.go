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
