package alias

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConvertDrushSSH(t *testing.T) {
	src := `
live:
  host: server.example.com
  user: www-admin
  root: /var/www/html
  uri: http://example.com
  ssh:
    options: -p 2222 -o StrictHostKeyChecking=no
  paths:
    drush-script: /var/www/html/vendor/bin/drush
`
	got := convertString(t, src)
	live, ok := got["live"]
	if !ok {
		t.Fatal("missing live env")
	}
	if live.Transport.Type != "ssh" || live.Transport.SSH == nil {
		t.Fatalf("expected ssh transport, got %+v", live.Transport)
	}
	if live.Transport.SSH.Host != "server.example.com" || live.Transport.SSH.User != "www-admin" {
		t.Errorf("ssh host/user mismapped: %+v", live.Transport.SSH)
	}
	wantOpts := []string{"-p", "2222", "-o", "StrictHostKeyChecking=no"}
	if !equalSlices(live.Transport.SSH.Options, wantOpts) {
		t.Errorf("ssh options = %q, want %q", live.Transport.SSH.Options, wantOpts)
	}
	if live.Bin.Kind != "drush" {
		t.Errorf("bin.kind = %q, want drush", live.Bin.Kind)
	}
	if live.Bin.Path != "/var/www/html/vendor/bin/drush" {
		t.Errorf("bin.path = %q", live.Bin.Path)
	}
}

func TestConvertDrushDockerToCompose(t *testing.T) {
	src := `
local:
  uri: http://localhost
  root: /app
  docker:
    service: drupal
    exec:
      options: --user USER
`
	got := convertString(t, src)
	local := got["local"]
	if local.Transport.Type != "compose" {
		t.Fatalf("expected compose transport, got %q", local.Transport.Type)
	}
	if local.Transport.Compose.Service != "drupal" {
		t.Errorf("service = %q", local.Transport.Compose.Service)
	}
	want := []string{"--user", "USER"}
	if !equalSlices(local.Transport.Compose.ExecOptions, want) {
		t.Errorf("exec_options = %q, want %q", local.Transport.Compose.ExecOptions, want)
	}
}

func TestConvertDrushKubectl(t *testing.T) {
	src := `
prod:
  uri: https://prod.example.com
  root: /app
  kubectl:
    namespace: drupal-prod
    resource: pods/drupal-foo
    container: php
    kubeconfig: /etc/kube.yml
`
	got := convertString(t, src)
	prod := got["prod"]
	if prod.Transport.Type != "kubectl" {
		t.Fatalf("expected kubectl, got %q", prod.Transport.Type)
	}
	k := prod.Transport.Kubectl
	if k.Namespace != "drupal-prod" || k.Resource != "pods/drupal-foo" || k.Container != "php" || k.Kubeconfig != "/etc/kube.yml" {
		t.Errorf("kubectl fields mismapped: %+v", k)
	}
}

func TestConvertDrushLocalToExec(t *testing.T) {
	src := `
local:
  uri: http://localhost
  root: /app/web
`
	got := convertString(t, src)
	local := got["local"]
	if local.Transport.Type != "exec" {
		t.Errorf("local site without host should map to exec; got %q", local.Transport.Type)
	}
}

func TestConvertDrushWarnsForUnmapped(t *testing.T) {
	src := `
live:
  host: x.example.com
  user: u
  command:
    sql:
      sync:
        options:
          no-cache: true
`
	dir := t.TempDir()
	path := filepath.Join(dir, "ex.site.yml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	warnings, err := ConvertDrushFile(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Error("expected at least one warning for `command:` block")
	}
	if !strings.Contains(strings.Join(warnings, " "), "command") {
		t.Errorf("warning should mention `command`; got %v", warnings)
	}
}

func convertString(t *testing.T, src string) AliasFile {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ex.site.yml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := ConvertDrushFile(path, &buf); err != nil {
		t.Fatal(err)
	}
	var got AliasFile
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("converted output isn't valid dconsole yaml: %v\noutput:\n%s", err, buf.String())
	}
	return got
}

func equalSlices(a, b []string) bool {
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
