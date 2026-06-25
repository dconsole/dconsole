//go:build darwin

// Inline URI parser for Apple's `container` CLI. Built only on
// macOS — the runtime doesn't exist elsewhere, so registering the
// scheme on Linux/Windows would parse a URI that immediately fails
// at handler-dispatch with a less-helpful error.

package alias

import (
	"errors"
	"net/url"
	"strings"
)

func init() {
	inlineSchemes["container"] = parseContainerURI
}

// parseContainerURI handles Apple's `container` CLI. No host or
// context — the Apple runtime is local-only.
//
//	@container://drupal-app
//	@container://drupal-app?user=www-data
func parseContainerURI(u *url.URL) (*Alias, error) {
	q := u.Query()
	container := strings.TrimPrefix(u.Path, "/")
	if u.User == nil && container == "" {
		container = u.Host
	} else if u.User != nil {
		return nil, errors.New("container URIs have no remote-host concept — drop the user@ prefix")
	}
	if container == "" {
		return nil, errors.New("missing container name: @container://<container>")
	}
	cfg := ContainerTransport{Container: container, User: q.Get("user")}
	return &Alias{
		Handler: newHandler("container", map[string]any{"container": containerToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}, nil
}

func containerToYAML(c ContainerTransport) map[string]any {
	out := map[string]any{}
	if c.Container != "" {
		out["container"] = c.Container
	}
	if c.User != "" {
		out["user"] = c.User
	}
	if len(c.ExecOptions) > 0 {
		out["exec_options"] = c.ExecOptions
	}
	return out
}
