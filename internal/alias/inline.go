package alias

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// IsInlineURIRef reports whether token looks like an `@<scheme>://…`
// inline URI reference (as opposed to `@site.env`). The dispatcher
// uses this BEFORE ParseRef so a host like "epp.heydon.io" doesn't
// get mistaken for "site.env".
//
// Discriminator: a literal "://" after the leading `@`. Plain `@x.y`
// keeps the existing site.env interpretation.
func IsInlineURIRef(token string) bool {
	if len(token) < 2 || token[0] != '@' {
		return false
	}
	rest := token[1:]
	colon := strings.Index(rest, ":")
	if colon <= 0 || colon+3 > len(rest) {
		return false
	}
	if rest[colon:colon+3] != "://" {
		return false
	}
	// Scheme must be a valid identifier — letters, digits, +-. (per RFC 3986).
	for i := 0; i < colon; i++ {
		c := rest[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			continue
		}
		return false
	}
	return true
}

// inlineParser is the per-scheme parse function. It receives the
// already-parsed *url.URL and returns the constructed alias (sans
// Site/Env, which the caller stamps).
type inlineParser func(u *url.URL) (*Alias, error)

// inlineSchemes maps scheme names to per-scheme URI parsers. The base
// set is platform-neutral; platform-specific schemes (e.g. Apple's
// `container`) register themselves from build-tagged files via
// RegisterInlineScheme.
var inlineSchemes = map[string]inlineParser{
	"ssh":     parseSSHURI,
	"ddev":    parseDDEVURI,
	"compose": parseComposeURI,
	"docker":  parseDockerURI,
	"kubectl": parseKubectlURI,
	"k8s":     parseKubectlURI, // synonym
	"lando":   parseLandoURI,
	"ahoy":    parseAhoyURI,
}

// RegisterInlineScheme lets plugin handlers register a URI parser for
// their scheme. The function receives the parsed URL and returns the
// alias to use.
func RegisterInlineScheme(scheme string, fn func(u *url.URL) (*Alias, error)) {
	inlineSchemes[scheme] = inlineParser(fn)
}

// ParseInline turns "@<scheme>://…" into a synthetic *Alias. The
// returned alias has Site="inline" and Env=<short hash of the
// canonical URI> so policy checks, cache keys, and cross-site
// confirmation have stable, deterministic identifiers.
func ParseInline(token string) (*Alias, error) {
	if !IsInlineURIRef(token) {
		return nil, fmt.Errorf("not an inline URI reference: %q", token)
	}
	raw := token[1:] // drop the leading @

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URI: %w", err)
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("missing URI scheme")
	}
	// Reject inline passwords up front — dconsole uses keys/agent only.
	if u.User != nil {
		if _, hasPW := u.User.Password(); hasPW {
			return nil, fmt.Errorf("inline URIs cannot carry passwords; configure ssh-agent / keys instead")
		}
	}

	parser, ok := inlineSchemes[u.Scheme]
	if !ok {
		return nil, fmt.Errorf("unknown URI scheme %q (known: ssh, ddev, compose, docker, container, kubectl/k8s, lando, ahoy, or a registered plugin)", u.Scheme)
	}

	a, err := parser(u)
	if err != nil {
		return nil, fmt.Errorf("inline %s://: %w", u.Scheme, err)
	}

	// Universal post-parse: fragment → URI; ?root= / ?bin= overrides;
	// stable synthetic Site/Env.
	if u.Fragment != "" {
		a.URI = u.Fragment
	}
	q := u.Query()
	if r := q.Get("root"); r != "" {
		a.Root = r
	}
	if uri := q.Get("uri"); uri != "" && a.URI == "" {
		a.URI = uri
	}
	if bin := q.Get("bin"); bin != "" {
		a.Bin = RemoteBin{Kind: "auto", Path: bin}
	}
	a.Site = "inline"
	a.Env = inlineEnv(token)
	return a, nil
}

// inlineEnv produces a deterministic 6-char hash of the canonical URI
// string. Used as the synthetic Env so caching, policy, and cross-site
// confirmation have a stable identifier per distinct URI.
func inlineEnv(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:3])
}

// === per-scheme parsers ==================================================

func parseSSHURI(u *url.URL) (*Alias, error) {
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, errors.New("missing host")
	}
	cfg := SSHTransport{
		Host: host,
		Port: port,
	}
	if u.User != nil {
		cfg.User = u.User.Username()
	}
	a := &Alias{
		Handler: newHandler("ssh", map[string]any{"ssh": sshToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		a.Root = "/" + p
	}
	return a, nil
}

func parseDDEVURI(u *url.URL) (*Alias, error) {
	if u.Host == "" {
		return nil, errors.New("project name required: @ddev://<project>")
	}
	q := u.Query()
	cfg := DDEVTransport{Project: u.Host, Service: q.Get("service")}
	a := &Alias{
		Handler: newHandler("ddev", map[string]any{"ddev": ddevToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}
	return a, nil
}

func parseComposeURI(u *url.URL) (*Alias, error) {
	q := u.Query()
	service := q.Get("service")
	if service == "" {
		return nil, errors.New("?service= is required for compose URIs")
	}

	projectDir := u.Path
	if projectDir == "" || projectDir == "/" {
		return nil, errors.New("missing project-dir path: @compose://[user@host]/<project-dir>?service=…")
	}

	cfg := ComposeTransport{ProjectDir: projectDir, Service: service}

	// user@host → ComposeTransport.Host as an ssh:// URL.
	if u.Host != "" {
		if u.User == nil {
			return nil, errors.New("when host is set in compose URI, a user is required (compose://user@host/…)")
		}
		cfg.Host = "ssh://" + u.User.Username() + "@" + u.Host
	}
	if ctx := q.Get("context"); ctx != "" {
		if cfg.Host != "" {
			return nil, errors.New("`?context=` is mutually exclusive with `user@host` in compose URIs")
		}
		cfg.Context = ctx
	}

	a := &Alias{
		Handler: newHandler("compose", map[string]any{"compose": composeToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}
	return a, nil
}

func parseDockerURI(u *url.URL) (*Alias, error) {
	q := u.Query()
	container := strings.TrimPrefix(u.Path, "/")

	// Two forms:
	//   1. docker://container                  → host empty, path empty,
	//      so url.Parse puts "container" in u.Host. Promote it.
	//   2. docker://user@host/container        → u.User+u.Host populated,
	//      container is in path.
	if u.User == nil && container == "" {
		container = u.Host
		u.Host = ""
	}
	if container == "" {
		return nil, errors.New("missing container name: @docker://[user@host/]<container>")
	}

	cfg := DockerTransport{Container: container, User: q.Get("user")}

	if u.Host != "" {
		if u.User == nil {
			return nil, errors.New("when host is set in docker URI, a user is required (docker://user@host/container)")
		}
		cfg.Host = "ssh://" + u.User.Username() + "@" + u.Host
	}
	if ctx := q.Get("context"); ctx != "" {
		if cfg.Host != "" {
			return nil, errors.New("`?context=` is mutually exclusive with `user@host` in docker URIs")
		}
		cfg.Context = ctx
	}

	a := &Alias{
		Handler: newHandler("docker", map[string]any{"docker": dockerToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}
	return a, nil
}

func parseKubectlURI(u *url.URL) (*Alias, error) {
	q := u.Query()
	cfg := KubectlTransport{
		Container:  q.Get("container"),
		Kubeconfig: q.Get("kubeconfig"),
	}
	// Path forms:
	//   kubectl://resource             → host=resource, path=""
	//   kubectl://ns/resource          → host=ns,       path=/resource
	if u.Host != "" && (u.Path == "" || u.Path == "/") {
		cfg.Resource = u.Host
	} else if u.Host != "" {
		cfg.Namespace = u.Host
		cfg.Resource = strings.TrimPrefix(u.Path, "/")
	} else if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		// kubectl:///resource — unusual but accept it.
		cfg.Resource = p
	}
	if cfg.Resource == "" {
		return nil, errors.New("missing resource: @kubectl://[<namespace>/]<resource>")
	}
	a := &Alias{
		Handler: newHandler("kubectl", map[string]any{"kubectl": kubectlToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}
	return a, nil
}

func parseLandoURI(u *url.URL) (*Alias, error) {
	dir := u.Path
	if u.Host != "" && dir == "" {
		// lando://<dir-with-no-leading-slash> — rare; promote host to path.
		dir = u.Host
	}
	if dir == "" || dir == "/" {
		return nil, errors.New("missing app-dir: @lando://<app-dir>")
	}
	q := u.Query()
	cfg := LandoTransport{AppDir: dir, Service: q.Get("service")}
	return &Alias{
		Handler: newHandler("lando", map[string]any{"lando": landoToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}, nil
}

func parseAhoyURI(u *url.URL) (*Alias, error) {
	dir := u.Path
	if u.Host != "" && dir == "" {
		dir = u.Host
	}
	if dir == "" || dir == "/" {
		return nil, errors.New("missing dir: @ahoy://<dir>")
	}
	q := u.Query()
	cfg := AhoyTransport{Dir: dir, Task: q.Get("task")}
	return &Alias{
		Handler: newHandler("ahoy", map[string]any{"ahoy": ahoyToYAML(cfg)}),
		Bin:     RemoteBin{Kind: "auto"},
	}, nil
}

// === helpers =============================================================

// newHandler wraps a typed config block into the Handler struct, mirroring
// what NewTransport does for the legacy Transport field.
func newHandler(typ string, block map[string]any) Handler {
	doc := map[string]any{"type": typ}
	for k, v := range block {
		doc[k] = v
	}
	// Marshal + unmarshal so the typed convenience pointer fields are
	// populated AND Raw captures the original node.
	body, err := yaml.Marshal(doc)
	if err != nil {
		panic("alias.newHandler: marshal: " + err.Error())
	}
	var h Handler
	if err := yaml.Unmarshal(body, &h); err != nil {
		panic("alias.newHandler: unmarshal: " + err.Error())
	}
	return h
}

// splitHostPort separates "host", "host:port", and "[ipv6]:port". Returns
// host and port (0 if absent). Lifted from net's HostPort but tolerant of
// the no-port case.
func splitHostPort(authority string) (string, int, error) {
	if authority == "" {
		return "", 0, nil
	}
	// IPv6 literal: [::1] or [::1]:22.
	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end == -1 {
			return "", 0, errors.New("invalid IPv6 host (missing ])")
		}
		host := authority[:end+1]
		rest := authority[end+1:]
		if rest == "" {
			return host, 0, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, errors.New("unexpected text after IPv6 host")
		}
		port, err := strconv.Atoi(rest[1:])
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %w", err)
		}
		return host, port, nil
	}
	// host[:port]
	if i := strings.LastIndex(authority, ":"); i >= 0 {
		port, err := strconv.Atoi(authority[i+1:])
		if err == nil {
			return authority[:i], port, nil
		}
		// Not a number — fall through and treat the whole thing as host.
	}
	return authority, 0, nil
}

// === per-handler YAML serialisation =====================================
//
// `newHandler` builds a generic map[string]any and Marshals → Unmarshals
// it. Each handler type has small differences in YAML field naming, so
// we project the Go struct into the right map shape here. (Could be
// done reflectively but the explicit form is clearer.)

func sshToYAML(c SSHTransport) map[string]any {
	out := map[string]any{}
	if c.Host != "" {
		out["host"] = c.Host
	}
	if c.User != "" {
		out["user"] = c.User
	}
	if c.Port != 0 {
		out["port"] = c.Port
	}
	if c.IdentityFile != "" {
		out["identity_file"] = c.IdentityFile
	}
	if len(c.Options) > 0 {
		out["options"] = c.Options
	}
	return out
}

func ddevToYAML(c DDEVTransport) map[string]any {
	out := map[string]any{}
	if c.Project != "" {
		out["project"] = c.Project
	}
	if c.Service != "" {
		out["service"] = c.Service
	}
	return out
}

func composeToYAML(c ComposeTransport) map[string]any {
	out := map[string]any{}
	if c.ProjectDir != "" {
		out["project_dir"] = c.ProjectDir
	}
	if c.Service != "" {
		out["service"] = c.Service
	}
	if c.Host != "" {
		out["host"] = c.Host
	}
	if c.Context != "" {
		out["context"] = c.Context
	}
	if len(c.ExecOptions) > 0 {
		out["exec_options"] = c.ExecOptions
	}
	return out
}

func dockerToYAML(c DockerTransport) map[string]any {
	out := map[string]any{}
	if c.Container != "" {
		out["container"] = c.Container
	}
	if c.User != "" {
		out["user"] = c.User
	}
	if c.Host != "" {
		out["host"] = c.Host
	}
	if c.Context != "" {
		out["context"] = c.Context
	}
	if len(c.ExecOptions) > 0 {
		out["exec_options"] = c.ExecOptions
	}
	return out
}

func kubectlToYAML(c KubectlTransport) map[string]any {
	out := map[string]any{}
	if c.Namespace != "" {
		out["namespace"] = c.Namespace
	}
	if c.Resource != "" {
		out["resource"] = c.Resource
	}
	if c.Container != "" {
		out["container"] = c.Container
	}
	if c.Kubeconfig != "" {
		out["kubeconfig"] = c.Kubeconfig
	}
	return out
}

func landoToYAML(c LandoTransport) map[string]any {
	out := map[string]any{}
	if c.AppDir != "" {
		out["app_dir"] = c.AppDir
	}
	if c.Service != "" {
		out["service"] = c.Service
	}
	return out
}

func ahoyToYAML(c AhoyTransport) map[string]any {
	out := map[string]any{}
	if c.Dir != "" {
		out["dir"] = c.Dir
	}
	if c.Task != "" {
		out["task"] = c.Task
	}
	return out
}
