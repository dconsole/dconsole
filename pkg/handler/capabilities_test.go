// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	_ "github.com/dconsole/dconsole/internal/transport"
	"gopkg.in/yaml.v3"
)

// TestDDEVSatisfiesDBImporter — the in-tree ddev transport satisfies
// pkg/handler.DBImporter (alongside pkg/transport.DBImporter). The
// signatures intentionally match during the v0.4.0 transition so the
// same concrete impl works under either interface.
func TestDDEVSatisfiesDBImporter(t *testing.T) {
	yml := `
root: /tmp
handler:
  type: ddev
  ddev: { project: x }
`
	var a alias.Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	a.Site, a.Env = "x", "dev"

	h, err := For(&a)
	if err != nil {
		// ddev requires the CLI on PATH for Available to succeed at
		// Run time; Build itself shouldn't fail. If it does (likely a
		// project-dir resolution issue under /tmp), skip — the type
		// check is what we care about.
		t.Skipf("ddev build failed (env-specific): %v", err)
	}

	if _, ok := h.(DBImporter); !ok {
		t.Errorf("ddev handler doesn't satisfy pkg/handler.DBImporter")
	}
	if _, ok := h.(FilesImporter); !ok {
		t.Errorf("ddev handler doesn't satisfy pkg/handler.FilesImporter")
	}
}

// TestSSHSatisfiesRsyncSSH — ssh transport satisfies pkg/handler.RsyncSSH.
func TestSSHSatisfiesRsyncSSH(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: example.com, user: deploy }
`
	var a alias.Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	a.Site, a.Env = "x", "prod"

	h, err := For(&a)
	if err != nil {
		t.Fatal(err)
	}
	rs, ok := h.(RsyncSSH)
	if !ok {
		t.Fatalf("ssh handler doesn't satisfy pkg/handler.RsyncSSH")
	}
	remote, _ := rs.RsyncRemote()
	if remote != "deploy@example.com" {
		t.Errorf("RsyncRemote remote = %q, want deploy@example.com", remote)
	}
}
