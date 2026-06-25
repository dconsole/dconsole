//go:build darwin

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

// TestContainerComposeArgv — argv is `container exec -i <project>-<service> …`
// where the container name follows container-compose's documented
// naming convention.
func TestContainerComposeArgv(t *testing.T) {
	cases := []struct {
		name string
		cfg  alias.ContainerComposeTransport
		cmd  []string
		want []string
	}{
		{
			"project_name + service",
			alias.ContainerComposeTransport{ProjectName: "hc", Service: "php"},
			[]string{"drush", "status"},
			[]string{"exec", "-i", "hc-php", "drush", "status"},
		},
		{
			"project_dir basename used when project_name is empty",
			alias.ContainerComposeTransport{ProjectDir: "/Users/me/projects/heydon", Service: "php"},
			[]string{"drush", "cr"},
			[]string{"exec", "-i", "heydon-php", "drush", "cr"},
		},
		{
			"project_name overrides project_dir basename",
			alias.ContainerComposeTransport{ProjectDir: "/Users/me/some-dir", ProjectName: "real-name", Service: "web"},
			[]string{"ls"},
			[]string{"exec", "-i", "real-name-web", "ls"},
		},
		{
			"user + exec_options",
			alias.ContainerComposeTransport{
				ProjectName: "hc",
				Service:     "php",
				User:        "www-data",
				ExecOptions: []string{"--env", "DEBUG=1"},
			},
			[]string{"drush", "cr"},
			[]string{"exec", "-i", "--user", "www-data", "--env", "DEBUG=1", "hc-php", "drush", "cr"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &containerComposeTransport{cfg: &c.cfg}
			got := tr.argv(c.cmd)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("argv:\n  got  %v\n  want %v", got, c.want)
			}
		})
	}
}

// TestContainerComposeShellPreview — interactive shell argv inserts -t
// and the optional -w workDir.
func TestContainerComposeShellPreview(t *testing.T) {
	tr := &containerComposeTransport{cfg: &alias.ContainerComposeTransport{
		ProjectName: "hc",
		Service:     "php",
		User:        "www-data",
	}}
	got := tr.ShellPreview("/var/www/html")
	want := []string{
		"container", "exec", "-it", "--user", "www-data",
		"-w", "/var/www/html", "hc-php",
		"sh", "-c", `exec "${SHELL:-/bin/sh}"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ShellPreview:\n  got  %v\n  want %v", got, want)
	}
}

// TestContainerComposeYAML — type: container-compose roundtrips through
// the YAML loader, satisfies handler.Handler, and emits the expected
// Preview argv. Mirrors the container handler's roundtrip test.
func TestContainerComposeYAML(t *testing.T) {
	yml := `
type: container-compose
container_compose:
  project_name: hc
  service: php
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
			Type:             h.Type,
			ContainerCompose: h.ContainerCompose,
			Raw:              h.Raw,
		},
	}
	tr, err := pkgtransport.For(a)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Preview([]string{"drush", "status"})
	want := []string{"container", "exec", "-i", "--user", "www-data", "hc-php", "drush", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Preview:\n  got  %v\n  want %v", got, want)
	}
	if sp, ok := tr.(handler.ShellPreviewer); ok {
		flat := strings.Join(sp.ShellPreview(""), " ")
		if !strings.Contains(flat, "container exec -it") {
			t.Errorf("ShellPreview should TTY-allocate via -it:\n  %s", flat)
		}
	} else {
		t.Error("containerComposeTransport must implement handler.ShellPreviewer")
	}
}

// TestContainerComposeMissingFields — service is mandatory; either
// project_name OR project_dir must be set.
func TestContainerComposeMissingFields(t *testing.T) {
	for _, yml := range []string{
		// no service
		`
type: container-compose
container_compose:
  project_name: hc
`,
		// neither project_name nor project_dir
		`
type: container-compose
container_compose:
  service: php
`,
	} {
		var h alias.Handler
		if err := yaml.Unmarshal([]byte(yml), &h); err != nil {
			t.Fatal(err)
		}
		a := &alias.Alias{
			Site: "x", Env: "y",
			Handler:   h,
			Transport: alias.Transport{Type: h.Type, ContainerCompose: h.ContainerCompose, Raw: h.Raw},
		}
		if _, err := pkgtransport.For(a); err == nil {
			t.Errorf("expected error for invalid config:\n%s", yml)
		}
	}
}
