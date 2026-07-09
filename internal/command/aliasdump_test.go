package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
)

// TestAliasDumpLegacyRendersAsHandler — legacy `transport:` YAML is
// normalised through LegacyChain and dumped in the modern `handler:`
// shape so the output serves as a migration aid (paste it back into
// dconsole.yml to move off the deprecated schema).
func TestAliasDumpLegacyRendersAsHandler(t *testing.T) {
	dir := t.TempDir()
	sites := filepath.Join(dir, "dconsole", "sites")
	if err := os.MkdirAll(sites, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := []byte(`dev:
  root: /var/www
  transport:
    type: ssh
    ssh:
      host: legacy.example.com
      user: gordon
`)
	if err := os.WriteFile(filepath.Join(sites, "legacy.site.yml"), yml, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	loader := alias.NewLoader()
	WireProjectResolution(loader)

	var buf bytes.Buffer
	if err := AliasDump(loader, "@legacy.dev", &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, "handler:") {
		t.Errorf("expected `handler:` in output; got:\n%s", got)
	}
	if strings.Contains(got, "transport:") {
		t.Errorf("dump must not echo legacy `transport:` — should render as `handler:` for migration:\n%s", got)
	}
	// The SSH block content should be preserved.
	for _, want := range []string{"type: ssh", "legacy.example.com", "gordon"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}

// TestAliasDumpModernRoundtrips — a modern `handler:` alias dumps
// unchanged (no legacy migration needed, so no schema switch).
func TestAliasDumpModernRoundtrips(t *testing.T) {
	dir := t.TempDir()
	sites := filepath.Join(dir, "dconsole", "sites")
	if err := os.MkdirAll(sites, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := []byte(`prod:
  root: /var/www
  handler:
    type: ssh
    ssh:
      host: modern.example.com
      user: gordon
`)
	if err := os.WriteFile(filepath.Join(sites, "modern.site.yml"), yml, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	loader := alias.NewLoader()
	WireProjectResolution(loader)

	var buf bytes.Buffer
	if err := AliasDump(loader, "@modern.prod", &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, "handler:") {
		t.Errorf("expected `handler:` in output; got:\n%s", got)
	}
	if strings.Contains(got, "transport:") {
		t.Errorf("modern alias must not gain a `transport:` block in dump; got:\n%s", got)
	}
	if !strings.Contains(got, "modern.example.com") {
		t.Errorf("output missing host; got:\n%s", got)
	}
}
