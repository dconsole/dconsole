//go:build darwin

// Inline URI parser for container-compose
// (https://github.com/mcrich23/container-compose). Built only on
// macOS — the underlying Apple `container` runtime doesn't exist
// elsewhere, so registering the scheme on Linux/Windows would parse
// a URI that immediately fails at handler-dispatch with a less-
// helpful error.

package alias

import (
	"errors"
	"net/url"
	"strings"
)

func init() {
	inlineSchemes["container-compose"] = parseContainerComposeURI
}

// parseContainerComposeURI handles the container-compose scheme. The
// path is interpreted in one of two ways depending on whether it
// starts with "/":
//
//	@container-compose:///path/to/project?service=php   # absolute → project_dir
//	@container-compose://hc?service=php                  # bare → project_name
//	@container-compose:///path/to/project?service=php&user=www-data
//	@container-compose://hc?service=php&root=/var/www
//
// Both forms reach the same container name at runtime:
// "<project>-<service>", matching container-compose's documented
// convention (Sources/Container-Compose/Commands/ComposeUp.swift).
func parseContainerComposeURI(u *url.URL) (*Alias, error) {
	if u.User != nil {
		return nil, errors.New("container-compose URIs have no remote-host concept — drop the user@ prefix")
	}
	q := u.Query()
	service := q.Get("service")
	if service == "" {
		return nil, errors.New("container-compose URI requires ?service=<name>")
	}

	cfg := ContainerComposeTransport{
		Service: service,
		User:    q.Get("user"),
	}

	// Reconstruct raw "host + path" so the leading "/" survives. With
	// `@container-compose:///abs/path`, net/url parses host="" path="/abs/path".
	// With `@container-compose://name`, host="name" path="".
	raw := u.Host + u.Path
	if raw == "" {
		return nil, errors.New("container-compose URI needs a project: @container-compose:///dir?service=… or @container-compose://name?service=…")
	}
	if strings.HasPrefix(raw, "/") {
		cfg.ProjectDir = raw
	} else {
		cfg.ProjectName = raw
	}

	a := &Alias{
		Handler: newHandler("container-compose", map[string]any{
			"container_compose": containerComposeToYAML(cfg),
		}),
		Bin: RemoteBin{Kind: "auto"},
	}
	if r := q.Get("root"); r != "" {
		a.Root = r
	}
	return a, nil
}

func containerComposeToYAML(c ContainerComposeTransport) map[string]any {
	out := map[string]any{}
	if c.ProjectDir != "" {
		out["project_dir"] = c.ProjectDir
	}
	if c.ProjectName != "" {
		out["project_name"] = c.ProjectName
	}
	if c.Service != "" {
		out["service"] = c.Service
	}
	if c.User != "" {
		out["user"] = c.User
	}
	if len(c.ExecOptions) > 0 {
		out["exec_options"] = c.ExecOptions
	}
	return out
}

