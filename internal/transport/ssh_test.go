package transport

import (
	"reflect"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
)

func TestSSHArgs(t *testing.T) {
	cases := []struct {
		name      string
		cfg       alias.SSHTransport
		remoteCmd []string
		want      []string
	}{
		{
			name:      "simple host with user",
			cfg:       alias.SSHTransport{Host: "dev.example.com", User: "deploy"},
			remoteCmd: []string{"/var/www/html/vendor/bin/drush", "status"},
			want:      []string{"deploy@dev.example.com", "--", "/var/www/html/vendor/bin/drush status"},
		},
		{
			name:      "host without user",
			cfg:       alias.SSHTransport{Host: "dev.example.com"},
			remoteCmd: []string{"drush", "cr"},
			want:      []string{"dev.example.com", "--", "drush cr"},
		},
		{
			name:      "custom port",
			cfg:       alias.SSHTransport{Host: "h", User: "u", Port: 2222},
			remoteCmd: []string{"drush", "cr"},
			want:      []string{"-p", "2222", "u@h", "--", "drush cr"},
		},
		{
			name:      "extra options pass through",
			cfg:       alias.SSHTransport{Host: "h", User: "u", Options: []string{"-o", "StrictHostKeyChecking=no"}},
			remoteCmd: []string{"drush", "status"},
			want:      []string{"-o", "StrictHostKeyChecking=no", "u@h", "--", "drush status"},
		},
		{
			name:      "arg with spaces gets quoted",
			cfg:       alias.SSHTransport{Host: "h", User: "u"},
			remoteCmd: []string{"drush", "sset", "system.site.name", "hello world"},
			want:      []string{"u@h", "--", "drush sset system.site.name 'hello world'"},
		},
		{
			name:      "single quote in arg",
			cfg:       alias.SSHTransport{Host: "h", User: "u"},
			remoteCmd: []string{"echo", "it's"},
			want:      []string{"u@h", "--", `echo 'it'\''s'`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &sshTransport{cfg: &c.cfg}
			got := s.sshArgs(c.remoteCmd)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("sshArgs:\n  got  %q\n  want %q", got, c.want)
			}
		})
	}
}

func TestSingleQuote(t *testing.T) {
	cases := map[string]string{
		"":          "''",
		"hello":     "hello",
		"hello.world": "hello.world",
		"path/to/x":  "path/to/x",
		"hi there":   "'hi there'",
		"a;b":        "'a;b'",
		"it's":       `'it'\''s'`,
	}
	for in, want := range cases {
		if got := singleQuote(in); got != want {
			t.Errorf("singleQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
