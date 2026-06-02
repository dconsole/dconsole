package alias

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLegacyChain_FromHandler covers the v0.4.0 schema's single-handler
// form, both flat and with a `via:` chain.
func TestLegacyChain_FromHandler(t *testing.T) {
	cases := []struct {
		name string
		yml  string
		want []string // .Type for each chain entry
	}{
		{
			name: "single handler",
			yml: `
handler:
  type: ssh
  ssh: { host: example.com, user: deploy }
`,
			want: []string{"ssh"},
		},
		{
			name: "ssh→docker via chain",
			yml: `
handler:
  type: ssh
  ssh: { host: example.com, user: deploy }
  via:
    type: docker
    docker: { container: drupal-app }
`,
			want: []string{"ssh", "docker"},
		},
		{
			name: "three-layer chain via nested via:",
			yml: `
handler:
  type: ssh
  ssh: { host: bastion, user: jump }
  via:
    type: ssh
    ssh: { host: app01, user: deploy }
    via:
      type: docker
      docker: { container: drupal }
`,
			want: []string{"ssh", "ssh", "docker"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var a Alias
			if err := yaml.Unmarshal([]byte(c.yml), &a); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			chain, err := a.LegacyChain()
			if err != nil {
				t.Fatalf("LegacyChain: %v", err)
			}
			got := typesOf(chain)
			if !sliceEq(got, c.want) {
				t.Errorf("chain types: got %v, want %v", got, c.want)
			}
		})
	}
}

// TestLegacyChain_FromHandlersList covers the list-form chain.
func TestLegacyChain_FromHandlersList(t *testing.T) {
	yml := `
handlers:
  - type: ssh
    ssh: { host: example.com, user: deploy }
  - type: docker
    docker: { container: drupal-app }
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	chain, err := a.LegacyChain()
	if err != nil {
		t.Fatal(err)
	}
	if got := typesOf(chain); !sliceEq(got, []string{"ssh", "docker"}) {
		t.Errorf("got %v", got)
	}
}

// TestLegacyChain_FromTransportOnly covers the v0.3.x compatibility path.
func TestLegacyChain_FromTransportOnly(t *testing.T) {
	yml := `
transport:
  type: ssh
  ssh: { host: example.com, user: deploy }
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	chain, err := a.LegacyChain()
	if err != nil {
		t.Fatal(err)
	}
	if got := typesOf(chain); !sliceEq(got, []string{"ssh"}) {
		t.Errorf("got %v, want [ssh]", got)
	}
}

// TestLegacyChain_FromTransportAndProvider folds legacy two-block configs
// into a two-layer chain: transport outer, provider inner.
func TestLegacyChain_FromTransportAndProvider(t *testing.T) {
	yml := `
transport:
  type: ssh
  ssh: { host: example.com, user: deploy }
provider:
  type: skpr
  skpr: { app: foo }
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	chain, err := a.LegacyChain()
	if err != nil {
		t.Fatal(err)
	}
	if got := typesOf(chain); !sliceEq(got, []string{"ssh", "skpr"}) {
		t.Errorf("got %v, want [ssh skpr]", got)
	}
}

// TestLegacyChain_RejectsMixedSchemas — using `handler:` AND `transport:`
// together should fail loudly rather than silently picking a winner.
func TestLegacyChain_RejectsMixedSchemas(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: a.com }
transport:
  type: ssh
  ssh: { host: b.com }
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LegacyChain(); err == nil {
		t.Error("expected error when mixing handler: with transport:")
	} else if !strings.Contains(err.Error(), "mixes") {
		t.Errorf("error message should explain the mix: %v", err)
	}
}

// TestLegacyChain_RejectsBothHandlerAndHandlers — pick one form.
func TestLegacyChain_RejectsBothHandlerAndHandlers(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: a.com }
handlers:
  - type: docker
    docker: { container: x }
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LegacyChain(); err == nil {
		t.Error("expected error when both handler: and handlers: are set")
	}
}

// TestLegacyChain_PreservesTypedFields — the migration must keep typed
// config blocks (ssh:, docker:, …) intact so factories that prefer the
// typed-field path don't have to fall back to Raw.Decode.
func TestLegacyChain_PreservesTypedFields(t *testing.T) {
	yml := `
transport:
  type: ssh
  ssh:
    host: example.com
    user: deploy
    port: 2222
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	chain, err := a.LegacyChain()
	if err != nil || len(chain) != 1 {
		t.Fatalf("LegacyChain: %v, len=%d", err, len(chain))
	}
	h := chain[0]
	if h.SSH == nil {
		t.Fatal("typed SSH block was dropped during migration")
	}
	if h.SSH.Host != "example.com" || h.SSH.User != "deploy" || h.SSH.Port != 2222 {
		t.Errorf("typed SSH fields wrong: %+v", h.SSH)
	}
}

// TestLegacyChain_RawSurvivesViaFlatten — when flattening a `via:` chain,
// each entry's Raw must still hold the YAML node for plugin Decode.
func TestLegacyChain_RawSurvivesViaFlatten(t *testing.T) {
	yml := `
handler:
  type: ssh
  ssh: { host: a.com }
  via:
    type: docker
    docker: { container: x }
`
	var a Alias
	if err := yaml.Unmarshal([]byte(yml), &a); err != nil {
		t.Fatal(err)
	}
	chain, err := a.LegacyChain()
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("len(chain) = %d, want 2", len(chain))
	}
	for i, h := range chain {
		if h.Raw.Kind == 0 {
			t.Errorf("chain[%d] (%s): Raw was zero-valued after flatten — plugin Decode would fail", i, h.Type)
		}
	}
}

// ---- helpers ---------------------------------------------------------

func typesOf(chain []Handler) []string {
	out := make([]string, len(chain))
	for i, h := range chain {
		out[i] = h.Type
	}
	return out
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
