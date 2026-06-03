package transport

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/pkg/handler"
	pkgtransport "github.com/dconsole/dconsole/pkg/transport"
	"gopkg.in/yaml.v3"
)

// TestDockerHost — `host:` injects `-H <value>` before the subcommand.
func TestDockerHost(t *testing.T) {
	yml := `
type: docker
docker:
  container: my-app
  host: ssh://gordon@hosting.example.com
`
	var h alias.Handler
	if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{Site: "x", Env: "y", Handler: h, Transport: alias.Transport{Type: h.Type, Docker: h.Docker, Raw: h.Raw}}

	tr, err := pkgtransport.For(a)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Preview([]string{"drush", "status"})
	want := []string{"docker", "-H", "ssh://gordon@hosting.example.com", "exec", "-i", "my-app", "drush", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Preview with host:\n  got  %v\n  want %v", got, want)
	}

	// Also verify ShellPreview includes -H.
	hh := tr.(handler.Handler)
	if sp, ok := hh.(handler.ShellPreviewer); ok {
		shell := sp.ShellPreview("")
		flat := strings.Join(shell, " ")
		if !strings.Contains(flat, "-H ssh://gordon@hosting.example.com") {
			t.Errorf("ShellPreview missing -H flag:\n  %s", flat)
		}
	} else {
		t.Error("docker transport doesn't implement ShellPreviewer")
	}
}

// TestDockerContext — `context:` injects `--context <name>`.
func TestDockerContext(t *testing.T) {
	yml := `
type: docker
docker:
  container: my-app
  context: hc-prod
`
	var h alias.Handler
	if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{Site: "x", Env: "y", Handler: h, Transport: alias.Transport{Type: h.Type, Docker: h.Docker, Raw: h.Raw}}

	tr, err := pkgtransport.For(a)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Preview([]string{"drush", "status"})
	want := []string{"docker", "--context", "hc-prod", "exec", "-i", "my-app", "drush", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Preview with context:\n  got  %v\n  want %v", got, want)
	}
}

// TestDockerHostAndContextMutuallyExclusive — both at once should fail
// at build time with a clear error.
func TestDockerHostAndContextMutuallyExclusive(t *testing.T) {
	yml := `
type: docker
docker:
  container: my-app
  host: ssh://x
  context: y
`
	var h alias.Handler
	if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{Site: "x", Env: "y", Handler: h, Transport: alias.Transport{Type: h.Type, Docker: h.Docker, Raw: h.Raw}}
	_, err := pkgtransport.For(a)
	if err == nil {
		t.Fatal("expected error when both host: and context: are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error doesn't explain the conflict: %v", err)
	}
}

// TestComposeHost — same pattern for compose.
func TestComposeHost(t *testing.T) {
	yml := `
type: compose
compose:
  project_dir: /home/gordon/docker/heydon
  service: php
  host: ssh://gordon@hosting.example.com
`
	var h alias.Handler
	if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{Site: "x", Env: "y", Handler: h, Transport: alias.Transport{Type: h.Type, Compose: h.Compose, Raw: h.Raw}}

	tr, err := pkgtransport.For(a)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Preview([]string{"drush", "status"})
	want := []string{
		"docker", "-H", "ssh://gordon@hosting.example.com",
		"compose", "--project-directory", "/home/gordon/docker/heydon",
		"exec", "-T", "php", "drush", "status",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("compose Preview with host:\n  got  %v\n  want %v", got, want)
	}

	// ShellPreview too.
	hh := tr.(handler.Handler)
	if sp, ok := hh.(handler.ShellPreviewer); ok {
		flat := strings.Join(sp.ShellPreview(""), " ")
		if !strings.Contains(flat, "-H ssh://gordon@hosting.example.com") {
			t.Errorf("compose ShellPreview missing -H:\n  %s", flat)
		}
		if !strings.Contains(flat, "compose") || !strings.Contains(flat, "exec") {
			t.Errorf("compose ShellPreview missing compose exec:\n  %s", flat)
		}
		// Must NOT contain "exec -T" — that suppresses tty.
		if strings.Contains(flat, " -T ") {
			t.Errorf("ShellPreview contains -T (no-tty) which kills interactive shells:\n  %s", flat)
		}
	}
}
