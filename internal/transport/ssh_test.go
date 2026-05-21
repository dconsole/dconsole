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
			name:      "bare key=value gets auto-prefixed with -o",
			cfg:       alias.SSHTransport{Host: "h", User: "u", Options: []string{"BatchMode=yes", "ConnectTimeout=20"}},
			remoteCmd: []string{"drush", "status"},
			want:      []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "u@h", "--", "drush status"},
		},
		{
			name:      "mixed: short flag + shortcut + already-prefixed",
			cfg:       alias.SSHTransport{Host: "h", User: "u", Options: []string{"-C", "BatchMode=yes", "-o", "ConnectTimeout=20"}},
			remoteCmd: []string{"drush", "cr"},
			want:      []string{"-C", "-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "u@h", "--", "drush cr"},
		},
		{
			name:      "value half of -o pair isn't double-wrapped",
			cfg:       alias.SSHTransport{Host: "h", User: "u", Options: []string{"-o", "ServerAliveInterval=60"}},
			remoteCmd: []string{"drush", "st"},
			want:      []string{"-o", "ServerAliveInterval=60", "u@h", "--", "drush st"},
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

func TestExpandSSHOptions(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"empty", []string{}, []string{}},
		{"empty string skipped", []string{"", "-C"}, []string{"-C"}},
		{"single short flag", []string{"-C"}, []string{"-C"}},
		{"single key=value", []string{"BatchMode=yes"}, []string{"-o", "BatchMode=yes"}},
		{"two key=value", []string{"BatchMode=yes", "ConnectTimeout=20"}, []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=20"}},
		{"explicit -o pair", []string{"-o", "BatchMode=yes"}, []string{"-o", "BatchMode=yes"}},
		{"explicit -i pair (path doesn't contain =)", []string{"-i", "/path/to/key"}, []string{"-i", "/path/to/key"}},
		{"explicit -p port", []string{"-p", "2222"}, []string{"-p", "2222"}},
		{"explicit -F config", []string{"-F", "/etc/ssh/myconf"}, []string{"-F", "/etc/ssh/myconf"}},
		{"-J jump (with key=value-looking host)", []string{"-J", "user@bastion:22"}, []string{"-J", "user@bastion:22"}},
		{"two-token + shortcut interleaved", []string{"-q", "BatchMode=yes", "-o", "ConnectTimeout=5"}, []string{"-q", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}},
		{"bare non-= token passes through (unusual but valid)", []string{"-N", "destination"}, []string{"-N", "destination"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := expandSSHOptions(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("expandSSHOptions(%q):\n  got  %q\n  want %q", c.in, got, c.want)
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
