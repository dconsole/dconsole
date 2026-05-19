package alias

import (
	"reflect"
	"testing"
)

func TestExtractIdentityFile(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		wantArgs []string
		wantID   string
	}{
		{
			name:     "no -i",
			in:       []string{"-o", "StrictHostKeyChecking=no", "-p", "22"},
			wantArgs: []string{"-o", "StrictHostKeyChecking=no", "-p", "22"},
			wantID:   "",
		},
		{
			name:     "-i with separate path",
			in:       []string{"-i", "~/.ssh/ansible", "-o", "StrictHostKeyChecking=no"},
			wantArgs: []string{"-o", "StrictHostKeyChecking=no"},
			wantID:   "~/.ssh/ansible",
		},
		{
			name:     "-i=path",
			in:       []string{"-i=~/.ssh/x", "-o", "x=y"},
			wantArgs: []string{"-o", "x=y"},
			wantID:   "~/.ssh/x",
		},
		{
			name:     "--identity-file long form",
			in:       []string{"--identity-file", "/keys/foo", "-p", "2222"},
			wantArgs: []string{"-p", "2222"},
			wantID:   "/keys/foo",
		},
		{
			name:     "first match wins",
			in:       []string{"-i", "/a", "-i", "/b"},
			wantArgs: []string{"-i", "/b"},
			wantID:   "/a",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotArgs, gotID := extractIdentityFile(c.in)
			if gotID != c.wantID {
				t.Errorf("id  = %q, want %q", gotID, c.wantID)
			}
			if !reflect.DeepEqual(gotArgs, c.wantArgs) {
				t.Errorf("args = %q, want %q", gotArgs, c.wantArgs)
			}
		})
	}
}

func TestConvertSplitsIdentityOutOfDrushSSHOptions(t *testing.T) {
	src := `
live:
  host: prod.example.com
  user: deploy
  ssh:
    options: -i ~/.ssh/ansible -o StrictHostKeyChecking=accept-new
`
	got := convertString(t, src)
	live := got["live"]
	if live.Transport.SSH == nil {
		t.Fatal("no ssh block")
	}
	if live.Transport.SSH.IdentityFile != "~/.ssh/ansible" {
		t.Errorf("identity_file = %q, want ~/.ssh/ansible", live.Transport.SSH.IdentityFile)
	}
	wantOpts := []string{"-o", "StrictHostKeyChecking=accept-new"}
	if !reflect.DeepEqual(live.Transport.SSH.Options, wantOpts) {
		t.Errorf("options = %q, want %q (the -i pair should be split out)", live.Transport.SSH.Options, wantOpts)
	}
}
