package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	_ "github.com/dconsole/dconsole/internal/transport"
)

func TestInspect_ForwardWithFullRef(t *testing.T) {
	dir := withInspectSite(t, `
dev:
  uri: https://dev.ex.com
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: ssh, ssh: { host: dev.ex.com, user: deploy, port: 2222 } }
`)
	loader := &alias.Loader{SearchPaths: []string{dir}}
	WireProjectResolution(loader)

	var out bytes.Buffer
	if err := Inspect(context.Background(), loader, []string{"@ex.dev", "cache:rebuild"}, &out); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"ref:       @ex.dev",
		"transport: ssh",
		"would run:",
		"ssh",
		"-p 2222",
		"deploy@dev.ex.com",
		"/var/www/html/vendor/bin/drush cache:rebuild",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestInspect_SqlSync_PolicyAllowed(t *testing.T) {
	dir := withInspectSite(t, `
prod:
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: exec, exec: {} }
  policy: { allow_sync_to: [dev] }

dev:
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: exec, exec: {} }
`)
	loader := &alias.Loader{SearchPaths: []string{dir}}
	WireProjectResolution(loader)

	var out bytes.Buffer
	if err := Inspect(context.Background(), loader, []string{"sql:sync", "@ex.prod", "@ex.dev"}, &out); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "✓ allowed") {
		t.Errorf("expected allowed marker, got:\n%s", got)
	}
}

func TestInspect_SqlSync_PolicyRefused(t *testing.T) {
	dir := withInspectSite(t, `
prod:
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: exec, exec: {} }
  policy: { sync_policy: protected }

dev:
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: exec, exec: {} }
`)
	loader := &alias.Loader{SearchPaths: []string{dir}}
	WireProjectResolution(loader)

	var out bytes.Buffer
	if err := Inspect(context.Background(), loader, []string{"sql:sync", "@ex.dev", "@ex.prod"}, &out); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "REFUSED") {
		t.Errorf("expected refusal marker, got:\n%s", got)
	}
	if !strings.Contains(got, "pass --force to bypass") {
		t.Errorf("expected --force hint, got:\n%s", got)
	}
}

func TestInspect_SqlSync_DockerCpStrategy(t *testing.T) {
	dir := withInspectSite(t, `
prod:
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: exec, exec: {} }
  sql:
    source:
      type: docker_cp
      container: backup-shipper
      path: /backups/latest.sql.gz

dev:
  root: /var/www/html
  bin: { kind: drush, path: /var/www/html/vendor/bin/drush }
  transport: { type: exec, exec: {} }
`)
	loader := &alias.Loader{SearchPaths: []string{dir}}
	WireProjectResolution(loader)

	var out bytes.Buffer
	if err := Inspect(context.Background(), loader, []string{"sql:sync", "@ex.prod", "@ex.dev"}, &out); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"docker_cp",
		"backup-shipper",
		"/backups/latest.sql.gz",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect missing %q\noutput:\n%s", want, got)
		}
	}
}

func withInspectSite(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ex.site.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
