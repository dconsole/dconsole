package command

import (
	"bytes"
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

	// Build the display structure as a yaml.Node so plugin-handler
	// config (which lives only in Handler.Raw, not on the typed fields)
	// survives the round-trip. Struct-level marshaling would silently
	// drop any Raw-only keys.
	envKey := fmt.Sprintf("%s:%s", a.Site, a.Env)
	envNode, err := buildDumpEnvNode(a, chain)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	doc := &yaml.Node{Kind: yaml.MappingNode}
	doc.Content = append(doc.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: envKey},
		envNode,
	)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	body := buf.Bytes()

	fmt.Fprintf(out, "# Resolved %q as:\n", res.Source)
	fmt.Fprintln(out, "#")
	_, err = out.Write(body)
	return err
}

// buildDumpEnvNode constructs the env-level YAML node (uri/root/bin/
// handler[s]/policy/env_vars). The handler section is built from each
// Handler.Raw where possible so plugin-handler config (which doesn't
// map to any typed field) survives.
func buildDumpEnvNode(a *alias.Alias, chain []alias.Handler) (*yaml.Node, error) {
	env := &yaml.Node{Kind: yaml.MappingNode}

	appendScalar := func(key, value string) {
		if value == "" {
			return
		}
		env.Content = append(env.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: value},
		)
	}
	appendField := func(key string, value any) error {
		n := &yaml.Node{}
		if err := n.Encode(value); err != nil {
			return err
		}
		// Skip empty mappings / sequences (matches yaml:",omitempty" intent).
		if n.Kind == yaml.MappingNode && len(n.Content) == 0 {
			return nil
		}
		if n.Kind == yaml.SequenceNode && len(n.Content) == 0 {
			return nil
		}
		env.Content = append(env.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			n,
		)
		return nil
	}

	appendScalar("uri", a.URI)
	appendScalar("root", a.Root)
	if err := appendField("bin", a.Bin); err != nil {
		return nil, err
	}

	switch len(chain) {
	case 0:
		// no handler configured
	case 1:
		hn, err := handlerDumpNode(chain[0])
		if err != nil {
			return nil, err
		}
		env.Content = append(env.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "handler"},
			hn,
		)
	default:
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, h := range chain {
			hn, err := handlerDumpNode(h)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, hn)
		}
		env.Content = append(env.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "handlers"},
			seq,
		)
	}

	if err := appendField("policy", a.Policy); err != nil {
		return nil, err
	}
	if len(a.EnvVars) > 0 {
		if err := appendField("env_vars", a.EnvVars); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// handlerDumpNode returns a mapping node for one handler. When Raw is
// populated (handler parsed from YAML) its mapping is reused so plugin
// config carries through; the `via:` key is dropped because the dump
// has already flattened the chain. When Raw is empty (handler
// synthesised from legacy transport/provider), fall back to encoding
// the typed struct.
func handlerDumpNode(h alias.Handler) (*yaml.Node, error) {
	if h.Raw.Kind == yaml.MappingNode && len(h.Raw.Content) > 0 {
		out := &yaml.Node{Kind: yaml.MappingNode}
		// `type:` first for readability; inline-URI synthesized aliases
		// have random map-iteration key order in Raw, so we re-pin it.
		var typeKV []*yaml.Node
		for i := 0; i < len(h.Raw.Content); i += 2 {
			k := h.Raw.Content[i]
			if k.Value == "type" {
				typeKV = []*yaml.Node{k, h.Raw.Content[i+1]}
				break
			}
		}
		out.Content = append(out.Content, typeKV...)
		for i := 0; i < len(h.Raw.Content); i += 2 {
			k := h.Raw.Content[i]
			if k.Value == "type" || k.Value == "via" {
				continue
			}
			out.Content = append(out.Content, k, h.Raw.Content[i+1])
		}
		return out, nil
	}
	n := &yaml.Node{}
	// Encode without the Via field — we've already flattened the chain.
	h.Via = nil
	if err := n.Encode(h); err != nil {
		return nil, err
	}
	return n, nil
}
