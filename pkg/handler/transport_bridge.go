// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"fmt"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/pkg/transport"
)

// For returns the Handler an alias is configured to use. Behind the
// scenes it bridges to pkg/transport: alias.LegacyChain() normalises
// whichever schema form is present (handler/handlers/transport[+provider])
// into a flat outer-to-inner []Handler, then each layer is built by
// calling transport.For with a scoped copy of the alias.
//
// Every in-tree transport adds a Wrap() method specifically so its
// concrete type also satisfies handler.Handler — the type assertion
// below makes that contract explicit. Subprocess plugins are required
// to provide Wrap via the `preview` verb (the same code path
// subprocessTransport.Wrap delegates to).
func For(a *alias.Alias) (Handler, error) {
	layers, err := a.LegacyChain()
	if err != nil {
		return nil, err
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("alias @%s.%s has no handler configured", a.Site, a.Env)
	}

	built := make([]Handler, 0, len(layers))
	for _, layer := range layers {
		h, err := buildOne(a, layer)
		if err != nil {
			return nil, err
		}
		built = append(built, h)
	}
	if len(built) == 1 {
		return built[0], nil
	}
	return NewChain(built), nil
}

// buildOne constructs a single Handler for one chain layer. The alias
// is scoped down so its Transport block mirrors the layer's config —
// that's what the pkg/transport factories read from. The resulting
// concrete value must also implement Handler (Wrap()); if it doesn't,
// the bridge surfaces a clear error.
func buildOne(a *alias.Alias, layer alias.Handler) (Handler, error) {
	if layer.Type == "" {
		return nil, fmt.Errorf("handler entry on @%s.%s has no type", a.Site, a.Env)
	}
	scoped := *a
	scoped.Transport = alias.Transport{
		Type:    layer.Type,
		Exec:    layer.Exec,
		SSH:     layer.SSH,
		Docker:  layer.Docker,
		Compose: layer.Compose,
		Kubectl: layer.Kubectl,
		DDEV:    layer.DDEV,
		Lando:   layer.Lando,
		Raw:     layer.Raw,
	}
	// Provider block isn't needed by transport.For; clearing avoids
	// any incidental shadowing if a later refactor reads it.
	scoped.Provider = alias.Provider{}

	t, err := transport.For(&scoped)
	if err != nil {
		return nil, err
	}
	h, ok := t.(Handler)
	if !ok {
		return nil, fmt.Errorf("handler %q (type %T) is missing Wrap() — can't participate in the handler API", layer.Type, t)
	}
	return h, nil
}
