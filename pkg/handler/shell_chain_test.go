// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	_ "github.com/dconsole/dconsole/internal/transport"
	"gopkg.in/yaml.v3"
)

// TestChainShell_SSHThenComposeWrapsThroughBothTTYs is the hc.prod
// regression test: ssh → compose chain, `dconsole sh` must produce
// an argv that:
//   - starts with `ssh -t` (otherwise no remote tty, shell exits)
//   - includes `docker compose ... exec` (NOT `exec -T`, which kills
//     stdin)
//   - and lands in the configured service container, not on the host.
//
// We can't easily test the spawn end-to-end without a real remote,
// but we CAN inspect the argv Chain.Shell would compose — which is
// the actual fix point.
func TestChainShell_SSHThenComposeWrapsThroughBothTTYs(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: hosting.example.com, user: deploy }
  via:
    type: compose
    compose:
      project_dir: /home/deploy/docker/site
      service: php
`
	var a alias.Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	a.Site = "hc"
	a.Env = "prod"

	h, err := For(&a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	chain, ok := h.(*Chain)
	if !ok {
		t.Fatalf("expected *Chain, got %T", h)
	}

	// Compose Shell argv the same way Chain.Shell does — innermost
	// first, each layer preferring ShellWrapper over plain Wrap.
	cur := []string{"bash", "-l"}
	for i := len(chain.layers) - 1; i >= 0; i-- {
		if sw, ok := chain.layers[i].(ShellWrapper); ok {
			cur = sw.WrapShell(cur)
		} else {
			cur = chain.layers[i].Wrap(cur)
		}
	}

	// ssh must allocate a tty.
	if len(cur) < 2 || cur[0] != "ssh" || cur[1] != "-t" {
		t.Errorf("expected argv to start with `ssh -t`, got %v", cur)
	}

	// The full argv joins to one string we can string-search.
	flat := strings.Join(cur, " ")

	// Must NOT contain "-T" — that would suppress tty inside docker compose,
	// causing the shell to exit immediately.
	if strings.Contains(flat, " -T ") || strings.HasSuffix(flat, " -T") {
		t.Errorf("argv contains -T (no-tty) which would kill an interactive shell:\n  %s", flat)
	}

	// Must mention docker compose exec.
	for _, want := range []string{"docker", "compose", "exec", "php", "bash"} {
		if !strings.Contains(flat, want) {
			t.Errorf("argv missing %q:\n  %s", want, flat)
		}
	}
}

// TestChainShell_SingleSSHRegression — non-chain ssh aliases keep
// using ssh.Shell which already does -t etc. Verifies handler.For
// returns the bare ssh handler (not a Chain) so we don't accidentally
// regress single-handler behaviour.
func TestChainShell_SingleSSHRegression(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: a.example.com, user: deploy }
`
	var a alias.Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	a.Site = "x"
	a.Env = "y"

	h, err := For(&a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if _, isChain := h.(*Chain); isChain {
		t.Errorf("single-handler alias should not produce a Chain (would route through Chain.Shell instead of ssh.Shell)")
	}
}
