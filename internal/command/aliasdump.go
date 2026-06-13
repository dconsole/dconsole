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
func AliasDump(loader *alias.Loader, token string, out io.Writer) error {
	res, err := ResolveContextual(loader, []string{token})
	if err != nil {
		return err
	}
	a := res.Alias

	// Strip noise: Site/Env are populated at load time, not authored
	// in YAML. Provider/Transport are the legacy fields and shouldn't
	// appear in dumped output for inline URIs (which always use the
	// new Handler shape). Bin's empty zero value is suppressed by
	// the omitempty tag.
	display := struct {
		URI       string            `yaml:"uri,omitempty"`
		Root      string            `yaml:"root,omitempty"`
		Bin       alias.RemoteBin   `yaml:"bin,omitempty"`
		Handler   alias.Handler     `yaml:"handler,omitempty"`
		Handlers  []alias.Handler   `yaml:"handlers,omitempty"`
		Transport alias.Transport   `yaml:"transport,omitempty"`
		Provider  alias.Provider    `yaml:"provider,omitempty"`
		Policy    alias.Policy      `yaml:"policy,omitempty"`
		EnvVars   map[string]string `yaml:"env_vars,omitempty"`
	}{
		URI:       a.URI,
		Root:      a.Root,
		Bin:       a.Bin,
		Handler:   a.Handler,
		Handlers:  a.Handlers,
		Transport: a.Transport,
		Provider:  a.Provider,
		Policy:    a.Policy,
		EnvVars:   a.EnvVars,
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
