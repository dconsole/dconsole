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

// TestContainerArgv — the basic `container exec -i <name> …` shape.
func TestContainerArgv(t *testing.T) {
	cases := []struct {
		name string
		cfg  alias.ContainerTransport
		cmd  []string
		want []string
	}{
		{
			"minimal",
			alias.ContainerTransport{Container: "drupal-app"},
			[]string{"drush", "status"},
			[]string{"exec", "-i", "drupal-app", "drush", "status"},
		},
		{
			"with user and extra exec options",
			alias.ContainerTransport{
				Container:   "drupal-app",
				User:        "www-data",
				ExecOptions: []string{"--env", "FOO=bar"},
			},
			[]string{"drush", "cr"},
			[]string{"exec", "-i", "--user", "www-data", "--env", "FOO=bar", "drupal-app", "drush", "cr"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &containerTransport{cfg: &c.cfg}
			got := tr.argv(c.cmd)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("argv:\n  got  %v\n  want %v", got, c.want)
			}
		})
	}
}

// TestContainerShellPreview — `container exec -it [--user] [-w] <c> sh -c …`.
// Inspect uses this to render the actual interactive-shell argv.
func TestContainerShellPreview(t *testing.T) {
	tr := &containerTransport{cfg: &alias.ContainerTransport{
		Container: "drupal-app",
		User:      "www-data",
	}}
	got := tr.ShellPreview("/var/www/html")
	want := []string{
		"container", "exec", "-it", "--user", "www-data",
		"-w", "/var/www/html", "drupal-app",
		"sh", "-c", `exec "${SHELL:-/bin/sh}"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ShellPreview:\n  got  %v\n  want %v", got, want)
	}
}

// TestContainerURI — @container://my-app routes through pkg/handler.For,
// satisfies handler.Handler, and produces the right Preview argv.
func TestContainerURI(t *testing.T) {
	yml := `
type: container
container:
  container: my-app
  user: www-data
`
	var h alias.Handler
	if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{
		Site:    "x",
		Env:     "y",
		Handler: h,
		Transport: alias.Transport{
			Type:      h.Type,
			Container: h.Container,
			Raw:       h.Raw,
		},
	}

	tr, err := pkgtransport.For(a)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Preview([]string{"drush", "status"})
	want := []string{"container", "exec", "-i", "--user", "www-data", "my-app", "drush", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Preview:\n  got  %v\n  want %v", got, want)
	}

	// ShellPreview surfaces -t for TTY allocation.
	if sp, ok := tr.(handler.ShellPreviewer); ok {
		flat := strings.Join(sp.ShellPreview(""), " ")
		if !strings.Contains(flat, "container exec -it") {
			t.Errorf("ShellPreview should start with `container exec -it` for TTY allocation:\n  %s", flat)
		}
	} else {
		t.Error("containerTransport doesn't implement handler.ShellPreviewer")
	}
}

// TestContainerMissingContainerName — empty name must be rejected at
// build time with a clear error.
func TestContainerMissingContainerName(t *testing.T) {
	yml := `
type: container
container:
  user: www-data
`
	var h alias.Handler
	if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{
		Site: "x", Env: "y",
		Handler:   h,
		Transport: alias.Transport{Type: h.Type, Container: h.Container, Raw: h.Raw},
	}
	if _, err := pkgtransport.For(a); err == nil {
		t.Fatal("expected error for missing container name")
	}
}
