package transport

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
)

func TestDockerArgv(t *testing.T) {
	cases := []struct {
		name string
		cfg  alias.DockerTransport
		cmd  []string
		want []string
	}{
		{
			name: "minimal",
			cfg:  alias.DockerTransport{Container: "drupal"},
			cmd:  []string{"drush", "status"},
			want: []string{"exec", "-i", "drupal", "drush", "status"},
		},
		{
			name: "with user and extra options",
			cfg: alias.DockerTransport{
				Container:   "drupal",
				User:        "www-data",
				ExecOptions: []string{"-e", "DRUSH_OPTIONS_URI=https://x"},
			},
			cmd:  []string{"drush", "cr"},
			want: []string{"exec", "-i", "--user", "www-data", "-e", "DRUSH_OPTIONS_URI=https://x", "drupal", "drush", "cr"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &dockerTransport{cfg: &c.cfg}
			got := d.argv(c.cmd)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("argv\n  got  %q\n  want %q", got, c.want)
			}
		})
	}
}

func TestComposeArgv(t *testing.T) {
	c := &composeTransport{cfg: &alias.ComposeTransport{
		ProjectDir:  "/projects/x",
		Service:     "php",
		ExecOptions: []string{"--user", "www-data"},
	}}
	cmd := c.build(context.Background(), []string{"drush", "status"})
	want := []string{"docker", "compose", "--project-directory", "/projects/x", "exec", "-T", "--user", "www-data", "php", "drush", "status"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("compose argv\n  got  %q\n  want %q", cmd.Args, want)
	}
}

func TestKubectlArgv(t *testing.T) {
	cases := []struct {
		name string
		cfg  alias.KubectlTransport
		want []string
	}{
		{
			name: "minimal",
			cfg:  alias.KubectlTransport{Resource: "deployment/drupal"},
			want: []string{"kubectl", "exec", "deployment/drupal", "--", "drush", "status"},
		},
		{
			name: "namespace + container + kubeconfig",
			cfg: alias.KubectlTransport{
				Namespace:  "drupal-prod",
				Resource:   "deployment/drupal",
				Container:  "php",
				Kubeconfig: "/etc/kube.yml",
			},
			want: []string{"kubectl", "--kubeconfig", "/etc/kube.yml", "-n", "drupal-prod", "exec", "deployment/drupal", "-c", "php", "--", "drush", "status"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := &kubectlTransport{cfg: &c.cfg}
			cmd := k.build(context.Background(), []string{"drush", "status"}, false)
			if !reflect.DeepEqual(cmd.Args, c.want) {
				t.Errorf("kubectl argv\n  got  %q\n  want %q", cmd.Args, c.want)
			}
		})
	}
}

func TestDDEVArgv(t *testing.T) {
	d := &ddevTransport{
		cfg:     &alias.DDEVTransport{Project: "example", Service: "web"},
		approot: "/path/to/example",
	}
	cmd := d.build(context.Background(), []string{"drush", "cr"})
	want := []string{"ddev", "exec", "-s", "web", "--", "drush", "cr"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("ddev argv\n  got  %q\n  want %q", cmd.Args, want)
	}
	// ddev resolves the project from CWD; ensure Cmd.Dir is set so
	// `ddev exec` targets the right project regardless of where dconsole
	// was invoked.
	if cmd.Dir != "/path/to/example" {
		t.Errorf("ddev cmd.Dir = %q, want %q", cmd.Dir, "/path/to/example")
	}
}

func TestDDEVApprootDiscovery(t *testing.T) {
	dir := t.TempDir()
	approot := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(approot, ".ddev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(approot, ".ddev", "config.yaml"), []byte("name: example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pass the docroot (one level below approot), the way a typical
	// alias.Root points into the project.
	docroot := filepath.Join(approot, "web")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := findDDEVApproot(docroot)
	if err != nil {
		t.Fatal(err)
	}
	if got != approot {
		t.Errorf("findDDEVApproot = %q, want %q", got, approot)
	}

	if _, err := findDDEVApproot(t.TempDir()); err == nil {
		t.Error("expected error when no .ddev/config.yaml is found anywhere upstream")
	}
}

func TestLandoArgv(t *testing.T) {
	l := &landoTransport{cfg: &alias.LandoTransport{AppDir: "/home/u/proj", Service: "appserver"}}
	cmd := l.build(context.Background(), []string{"drush", "sset", "system.site.name", "hello world"})
	want := []string{"lando", "ssh", "-s", "appserver", "-c", `drush sset system.site.name 'hello world'`}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("lando argv\n  got  %q\n  want %q", cmd.Args, want)
	}
	if cmd.Dir != "/home/u/proj" {
		t.Errorf("lando Dir = %q, want /home/u/proj", cmd.Dir)
	}
}

func TestTransportRegistryHasAll(t *testing.T) {
	want := []string{"ssh", "docker", "compose", "kubectl", "ddev", "lando"}
	have := strings.Join(Names(), ",")
	for _, name := range want {
		if !strings.Contains(have, name) {
			t.Errorf("transport %q not registered (have %s)", name, have)
		}
	}
}
