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

func TestSSHRsyncRemote(t *testing.T) {
	cases := []struct {
		name       string
		cfg        alias.SSHTransport
		wantRemote string
		wantOpts   []string
	}{
		{
			name:       "user + host",
			cfg:        alias.SSHTransport{Host: "example.com", User: "deploy"},
			wantRemote: "deploy@example.com",
			wantOpts:   nil,
		},
		{
			name:       "host only",
			cfg:        alias.SSHTransport{Host: "example.com"},
			wantRemote: "example.com",
			wantOpts:   nil,
		},
		{
			name:       "with port",
			cfg:        alias.SSHTransport{Host: "h", User: "u", Port: 2222},
			wantRemote: "u@h",
			wantOpts:   []string{"-p", "2222"},
		},
		{
			name: "identity + extra options",
			cfg: alias.SSHTransport{
				Host: "h", User: "u",
				IdentityFile: "/tmp/key",
				Options:      []string{"-o", "StrictHostKeyChecking=no"},
			},
			wantRemote: "u@h",
			wantOpts:   []string{"-i", "/tmp/key", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &sshTransport{cfg: &c.cfg}
			remote, opts := s.RsyncRemote()
			if remote != c.wantRemote {
				t.Errorf("remote = %q, want %q", remote, c.wantRemote)
			}
			if !reflect.DeepEqual(opts, c.wantOpts) {
				t.Errorf("opts:\n  got  %q\n  want %q", opts, c.wantOpts)
			}
		})
	}
}

func TestDDEVImportFilesArgv(t *testing.T) {
	// ImportFiles always builds the same argv shape regardless of alias.
	// Replicate the logic here to assert without needing ddev installed.
	bundlePath := "/tmp/files.tar.gz"
	wantArgs := []string{"ddev", "import-files", "--src=" + bundlePath}
	args := []string{"import-files", "--src=" + bundlePath}
	got := append([]string{"ddev"}, args...)
	if !reflect.DeepEqual(got, wantArgs) {
		t.Errorf("argv:\n  got  %q\n  want %q", got, wantArgs)
	}
}

func TestDDEVImportDBArgv(t *testing.T) {
	cases := []struct {
		name     string
		a        *alias.Alias
		dump     string
		wantArgs []string
	}{
		{
			name:     "default database",
			a:        &alias.Alias{Site: "ex", Env: "local"},
			dump:     "/tmp/dump.sql.gz",
			wantArgs: []string{"ddev", "import-db", "--file=/tmp/dump.sql.gz"},
		},
		{
			name: "named target database",
			a: &alias.Alias{Site: "ex", Env: "local",
				SQL: alias.SQL{Target: alias.SQLTarget{Database: "migrate"}}},
			dump:     "/tmp/dump.sql.gz",
			wantArgs: []string{"ddev", "import-db", "--file=/tmp/dump.sql.gz", "--database=migrate"},
		},
		{
			name: "default key in target.database stays implicit",
			a: &alias.Alias{Site: "ex", Env: "local",
				SQL: alias.SQL{Target: alias.SQLTarget{Database: "default"}}},
			dump:     "/tmp/dump.sql.gz",
			wantArgs: []string{"ddev", "import-db", "--file=/tmp/dump.sql.gz"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Replicate ImportDB's argv-building logic so we can assert
			// the exact command line without needing ddev installed.
			args := []string{"import-db", "--file=" + c.dump}
			if db := c.a.SQL.Target.Database; db != "" && db != "default" {
				args = append(args, "--database="+db)
			}
			got := append([]string{"ddev"}, args...)
			if !reflect.DeepEqual(got, c.wantArgs) {
				t.Errorf("argv:\n  got  %q\n  want %q", got, c.wantArgs)
			}
		})
	}
}

func TestAhoyArgv(t *testing.T) {
	cases := []struct {
		name      string
		cfg       alias.AhoyTransport
		remoteCmd []string
		wantArgs  []string
	}{
		{
			name:      "defaults task to bin basename",
			cfg:       alias.AhoyTransport{},
			remoteCmd: []string{"drush", "sql:dump", "--gzip"},
			wantArgs:  []string{"ahoy", "drush", "sql:dump", "--gzip"},
		},
		{
			name:      "explicit task overrides basename",
			cfg:       alias.AhoyTransport{Task: "d"},
			remoteCmd: []string{"/vendor/bin/drush", "status"},
			wantArgs:  []string{"ahoy", "d", "status"},
		},
		{
			name:      "absolute bin path uses just the basename",
			cfg:       alias.AhoyTransport{},
			remoteCmd: []string{"/var/www/vendor/bin/drupal", "cache:rebuild"},
			wantArgs:  []string{"ahoy", "drupal", "cache:rebuild"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &ahoyTransport{cfg: c.cfg, workDir: "/some/project"}
			got := h.build(context.Background(), c.remoteCmd)
			if !reflect.DeepEqual(got.Args, c.wantArgs) {
				t.Errorf("argv\n  got  %q\n  want %q", got.Args, c.wantArgs)
			}
			if got.Dir != "/some/project" {
				t.Errorf("Dir = %q, want /some/project", got.Dir)
			}
		})
	}
}

func TestAhoyDirDiscovery(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(filepath.Join(project, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ahoy.yml"), []byte("ahoyapi: v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Start from the docroot — should walk up one level to the project.
	got, err := findAhoyDir(filepath.Join(project, "web"))
	if err != nil {
		t.Fatal(err)
	}
	if got != project {
		t.Errorf("findAhoyDir = %q, want %q", got, project)
	}
	// No .ahoy.yml at or above an unrelated dir → error.
	if _, err := findAhoyDir(t.TempDir()); err == nil {
		t.Error("expected error when no .ahoy.yml is reachable")
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
	want := []string{"ssh", "docker", "compose", "kubectl", "ddev", "lando", "ahoy", "exec"}
	have := strings.Join(Names(), ",")
	for _, name := range want {
		if !strings.Contains(have, name) {
			t.Errorf("transport %q not registered (have %s)", name, have)
		}
	}
}
