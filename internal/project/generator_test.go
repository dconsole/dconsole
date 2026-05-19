package project

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect_NothingHere(t *testing.T) {
	d, err := Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Errorf("expected nil for empty dir, got %+v", d)
	}
}

func TestDetect_DDevOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".ddev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ddev", "config.yaml"), []byte("name: example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("expected detection")
	}
	if d.ProjectName != "example" {
		t.Errorf("project name = %q, want example", d.ProjectName)
	}
	if _, ok := d.Envs["local"]; !ok {
		t.Errorf("expected `local` env from ddev; got %v", d.Envs)
	}
	if d.DefaultEnv != "local" {
		t.Errorf("default_env = %q, want local", d.DefaultEnv)
	}
}

func TestDetect_DrushAliases(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "drush", "sites"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drush", "sites", "self.site.yml"), []byte(`
dev:
  host: dev.x.com
  user: deploy
  root: /var/www/html
prod:
  host: prod.x.com
  user: deploy
  root: /var/www/html
`), 0o600); err != nil {
		t.Fatal(err)
	}
	// composer is needed for the indicator that says "this is a Drupal
	// project," because drush/sites alone is enough to walk into here.
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"name":"acme/x","require":{"drupal/core":"^11"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("expected detection")
	}
	if _, ok := d.Envs["dev"]; !ok {
		t.Errorf("expected dev env; got %v", d.Envs)
	}
	if _, ok := d.Envs["prod"]; !ok {
		t.Errorf("expected prod env; got %v", d.Envs)
	}
	if d.ProjectName != "x" {
		t.Errorf("project name from composer = %q, want x", d.ProjectName)
	}
}

func TestWrite_Deterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"name":"a/b","require":{"drupal/core":"^11"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ddev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ddev", "config.yaml"), []byte("name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("nil detection")
	}
	var a, b bytes.Buffer
	if err := Write(d, &a); err != nil {
		t.Fatal(err)
	}
	if err := Write(d, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("output not deterministic between runs")
	}
	if !strings.Contains(a.String(), "project: b") {
		t.Errorf("expected `project: b`; got:\n%s", a.String())
	}
}

func TestGenerate_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"name":"a/b","require":{"drupal/core":"^11"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ddev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ddev", "config.yaml"), []byte("name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "dconsole.yml")
	if err := Generate(d, target, false); err != nil {
		t.Fatal(err)
	}
	// Second write without --force should be refused.
	if err := Generate(d, target, false); err == nil {
		t.Error("expected refusal when file exists")
	}
	// With overwrite=true it should succeed.
	if err := Generate(d, target, true); err != nil {
		t.Errorf("overwrite=true should succeed: %v", err)
	}
}
