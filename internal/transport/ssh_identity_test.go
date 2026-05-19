package transport

import (
	"reflect"
	"strings"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
)

func TestSSHArgs_WithIdentityFile(t *testing.T) {
	s := &sshTransport{cfg: &alias.SSHTransport{
		Host:         "x.example.com",
		User:         "deploy",
		IdentityFile: "/keys/ansible",
	}}
	got := s.sshArgs([]string{"drush", "status"})
	want := []string{
		"-i", "/keys/ansible",
		"-o", "IdentitiesOnly=yes",
		"deploy@x.example.com",
		"--",
		"drush status",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n  got  %q\n  want %q", got, want)
	}
}

func TestSSHArgs_TildeExpanded(t *testing.T) {
	s := &sshTransport{cfg: &alias.SSHTransport{
		Host:         "x.example.com",
		User:         "deploy",
		IdentityFile: "~/.ssh/ansible",
	}}
	got := s.sshArgs([]string{"drush", "status"})
	// At least one arg should contain `/.ssh/ansible` (absolute path)
	// and no entry should be a literal "~".
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "/.ssh/ansible") {
		t.Errorf("expected ~ to be expanded; argv = %q", got)
	}
	for _, a := range got {
		if a == "~" || strings.HasPrefix(a, "~/") {
			t.Errorf("unexpanded ~ in argv: %q", a)
		}
	}
}

func TestSSHArgs_IdentityOnlyComesFirst(t *testing.T) {
	// IdentitiesOnly must be set so ssh actually USES the -i key instead
	// of treating it as one of many candidates.
	s := &sshTransport{cfg: &alias.SSHTransport{
		Host:         "x",
		IdentityFile: "/k",
	}}
	got := s.sshArgs([]string{"true"})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-o IdentitiesOnly=yes") {
		t.Errorf("missing IdentitiesOnly=yes; argv = %q", got)
	}
}
