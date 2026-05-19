package alias

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestTransport_RawPreservesUnknownTypes confirms that yaml.v3 round-
// tripping through Transport keeps the raw mapping intact for transport
// types that have no typed field (i.e. out-of-tree plugin types).
// Without this, plugin factories can't recover their own config block.
func TestTransport_RawPreservesUnknownTypes(t *testing.T) {
	input := `
type: skpr
skpr:
  account: example
  environment: production
  options:
    - --foo
    - --bar
`
	var tr Transport
	if err := yaml.Unmarshal([]byte(input), &tr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tr.Type != "skpr" {
		t.Errorf("Type = %q, want skpr", tr.Type)
	}
	// None of the in-tree pointer fields should be populated.
	if tr.SSH != nil || tr.DDEV != nil || tr.Docker != nil {
		t.Errorf("typed fields incorrectly populated for unknown type")
	}
	// Raw should round-trip the full mapping.
	type skprCfg struct {
		Account     string   `yaml:"account"`
		Environment string   `yaml:"environment"`
		Options     []string `yaml:"options"`
	}
	var cfg skprCfg
	// The "skpr:" block is nested under the transport mapping — plugin
	// factories decode the whole mapping and pull their key.
	type wrapper struct {
		Skpr skprCfg `yaml:"skpr"`
	}
	var w wrapper
	if err := tr.Decode(&w); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	cfg = w.Skpr
	if cfg.Account != "example" {
		t.Errorf("Account = %q, want example", cfg.Account)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, want production", cfg.Environment)
	}
	if strings.Join(cfg.Options, ",") != "--foo,--bar" {
		t.Errorf("Options = %v, want [--foo --bar]", cfg.Options)
	}
}

// TestTransport_TypedFieldsStillPopulated confirms the in-tree typed
// fields keep decoding alongside Raw — existing factories continue to
// access a.Transport.SSH etc.
func TestTransport_TypedFieldsStillPopulated(t *testing.T) {
	input := `
type: ssh
ssh:
  host: example.com
  user: deploy
  port: 2222
`
	var tr Transport
	if err := yaml.Unmarshal([]byte(input), &tr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tr.Type != "ssh" {
		t.Errorf("Type = %q, want ssh", tr.Type)
	}
	if tr.SSH == nil {
		t.Fatal("SSH typed field not populated")
	}
	if tr.SSH.Host != "example.com" || tr.SSH.User != "deploy" || tr.SSH.Port != 2222 {
		t.Errorf("SSH config decoded incorrectly: %+v", tr.SSH)
	}
	// Raw should also be populated.
	if tr.Raw.Kind == 0 {
		t.Errorf("Raw not populated for in-tree type")
	}
}

// TestProvider_RawPreservesUnknownTypes mirrors the transport test for
// providers.
func TestProvider_RawPreservesUnknownTypes(t *testing.T) {
	input := `
type: skpr
skpr:
  api_token: ${SKPR_TOKEN}
`
	var p Provider
	if err := yaml.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != "skpr" {
		t.Errorf("Type = %q, want skpr", p.Type)
	}
	if p.Ironstar != nil {
		t.Errorf("Ironstar should be nil for unknown type")
	}
	type wrapper struct {
		Skpr struct {
			APIToken string `yaml:"api_token"`
		} `yaml:"skpr"`
	}
	var w wrapper
	if err := p.Decode(&w); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if w.Skpr.APIToken != "${SKPR_TOKEN}" {
		t.Errorf("APIToken = %q, want ${SKPR_TOKEN}", w.Skpr.APIToken)
	}
}
