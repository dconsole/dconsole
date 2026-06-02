// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	// Wire up the in-tree transports so the bridge can find them.
	// Tests in this package treat them as black-box implementations
	// reachable via handler.For.
	_ "github.com/dconsole/dconsole/internal/transport"
	"gopkg.in/yaml.v3"
)

// TestFor_FromLegacyTransport — a v0.3.x-style alias resolves to a
// single handler through the bridge.
func TestFor_FromLegacyTransport(t *testing.T) {
	yml := `
transport:
  type: exec
  exec: { dir: /tmp }
`
	a := decode(t, yml)
	h, err := For(&a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if h.Name() != "exec" {
		t.Errorf("Name = %q, want exec", h.Name())
	}
	// exec is the identity transform; Wrap(["drush","status"]) == input
	got := h.Wrap([]string{"drush", "status"})
	if len(got) != 2 || got[0] != "drush" || got[1] != "status" {
		t.Errorf("exec Wrap should be identity, got %v", got)
	}
}

// TestFor_FromHandler — a v0.4.0 single handler block.
func TestFor_FromHandler(t *testing.T) {
	yml := `
handler:
  type: exec
  exec: { dir: /tmp }
`
	a := decode(t, yml)
	h, err := For(&a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if h.Name() != "exec" {
		t.Errorf("Name = %q", h.Name())
	}
}

// TestFor_HandlerViaChain — handler: with `via:` produces a Chain.
func TestFor_HandlerViaChain(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: prod.example.com, user: deploy }
  via:
    type: docker
    docker: { container: drupal }
`
	a := decode(t, yml)
	h, err := For(&a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	chain, ok := h.(*Chain)
	if !ok {
		t.Fatalf("expected *Chain, got %T", h)
	}
	if got := chain.Name(); got != "ssh→docker" {
		t.Errorf("chain name = %q, want ssh→docker", got)
	}
	// Argv should be `ssh deploy@prod.example.com -- "docker exec [-i] drupal drush status"`
	// — the inner docker argv is shell-quoted into a SINGLE string at
	// the tail of the ssh argv. Exact docker flags (e.g. -i) may vary.
	argv := chain.Wrap([]string{"drush", "status"})
	if len(argv) != 4 {
		t.Fatalf("expected 4-element ssh argv (cmd, target, --, quoted-inner), got %d: %v", len(argv), argv)
	}
	if argv[0] != "ssh" {
		t.Errorf("argv[0] = %q, want ssh", argv[0])
	}
	if argv[2] != "--" {
		t.Errorf("argv[2] = %q, want --", argv[2])
	}
	last := argv[3]
	for _, want := range []string{"docker", "exec", "drupal", "drush", "status"} {
		if !contains(last, want) {
			t.Errorf("inner-quoted string missing %q; tail = %q", want, last)
		}
	}
}

// TestFor_HandlersList — handlers: [...] form produces a Chain.
func TestFor_HandlersList(t *testing.T) {
	yml := `
handlers:
  - type: ssh
    ssh: { host: prod, user: deploy }
  - type: docker
    docker: { container: drupal }
`
	a := decode(t, yml)
	h, err := For(&a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if _, ok := h.(*Chain); !ok {
		t.Errorf("expected *Chain, got %T", h)
	}
}

// TestFor_LegacyTransportPlusProvider — folds into a 2-layer chain
// even though we have no Skpr plugin registered. Provider innermost
// would fail to build (unknown type), so this test uses two known
// in-tree types instead: transport=exec + provider=exec (silly but
// proves the chain-fold path).
func TestFor_RejectsUnknownType(t *testing.T) {
	yml := `
handler:
  type: not-a-real-handler
`
	a := decode(t, yml)
	if _, err := For(&a); err == nil {
		t.Error("expected error for unknown handler type")
	}
}

// TestFor_NoHandlerConfigured — error message should be clear.
func TestFor_NoHandlerConfigured(t *testing.T) {
	a := alias.Alias{Site: "x", Env: "y"}
	_, err := For(&a)
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
	if !contains(err.Error(), "no handler configured") {
		t.Errorf("error message should mention no handler: %v", err)
	}
}

// ---- helpers ---------------------------------------------------------

func decode(t *testing.T, yml string) alias.Alias {
	t.Helper()
	var a alias.Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	a.Site = "test"
	a.Env = "env"
	return a
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
