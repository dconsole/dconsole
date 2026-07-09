package command

import (
	"fmt"
	"io"

	"github.com/dconsole/dconsole/internal/alias"
	"gopkg.in/yaml.v3"
)

// AliasDump renders the resolved YAML equivalent of a `@ref` or
// `@<URI>` token. Useful both as a debugging aid for inline URIs and
// as a migration path — copy the output into dconsole.yml to save
// the URI as a permanent alias.
//
// Legacy `transport:` / `provider:` YAML aliases are normalised
// through LegacyChain() and rendered as `handler:` (single link) or
// `handlers:` (multi-link), so dump doubles as a legacy→modern
// migration tool: run dump on your old alias, paste the output back
// into your dconsole.yml, done.
func AliasDump(loader *alias.Loader, token string, out io.Writer) error {
	res, err := ResolveContextual(loader, []string{token})
	if err != nil {
		return err
	}
	a := res.Alias

	// Normalise Handler / Handlers / legacy Transport+Provider into a
	// single canonical chain. Errors from LegacyChain (e.g. schema
	// mixing) bubble up so the user knows their alias is malformed.
	chain, err := a.LegacyChain()
	if err != nil {
		return err
	}

	display := struct {
		URI      string            `yaml:"uri,omitempty"`
		Root     string            `yaml:"root,omitempty"`
		Bin      alias.RemoteBin   `yaml:"bin,omitempty"`
		Handler  *alias.Handler    `yaml:"handler,omitempty"`
		Handlers []alias.Handler   `yaml:"handlers,omitempty"`
		Policy   alias.Policy      `yaml:"policy,omitempty"`
		EnvVars  map[string]string `yaml:"env_vars,omitempty"`
	}{
		URI:     a.URI,
		Root:    a.Root,
		Bin:     a.Bin,
		Policy:  a.Policy,
		EnvVars: a.EnvVars,
	}
	// One-link chains render as the compact `handler:` form; two or
	// more links use the explicit `handlers:` list.
	switch len(chain) {
	case 0:
		// no-op — no handler configured
	case 1:
		h := chain[0]
		display.Handler = &h
	default:
		display.Handlers = chain
	}

	body, err := yaml.Marshal(map[string]any{
		fmt.Sprintf("%s:%s", a.Site, a.Env): display,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	fmt.Fprintf(out, "# Resolved %q as:\n", res.Source)
	fmt.Fprintln(out, "#")
	_, err = out.Write(body)
	return err
}
