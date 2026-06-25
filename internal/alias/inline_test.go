package alias

import (
	"strings"
	"testing"
)

// TestIsInlineURIRef — discriminator must accept @<scheme>://… and
// REJECT plain @site.env or @env so the existing ParseRef path is
// unaffected.
func TestIsInlineURIRef(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"@ssh://host/path", true},
		{"@ddev://hc", true},
		{"@compose://user@host/path?service=php", true},
		{"@kubectl://ns/pod", true},
		{"@k8s://pod", true},
		{"@docker://drupal", true},

		// must NOT match — the existing ParseRef cases
		{"@site.env", false},
		{"@hc.prod", false},
		{"@hc", false},
		{"@", false},
		{"", false},
		{"not-an-alias", false},

		// host-with-dots was the original ambiguity — must not match
		{"@hosting.epp.heydon.io/path", false},
		{"@ssh:/host/path", false}, // missing second slash
		{"@://host", false},        // no scheme
	}
	for _, c := range cases {
		t.Run(c.token, func(t *testing.T) {
			if got := IsInlineURIRef(c.token); got != c.want {
				t.Errorf("IsInlineURIRef(%q) = %v, want %v", c.token, got, c.want)
			}
		})
	}
}

// TestParseInlineSSH covers the ssh scheme — user@host[:port][/root][#uri].
func TestParseInlineSSH(t *testing.T) {
	cases := []struct {
		name     string
		token    string
		wantHost string
		wantUser string
		wantPort int
		wantRoot string
		wantURI  string
	}{
		{
			"basic user@host/path",
			"@ssh://gordon@hosting.epp.heydon.io/var/www/html",
			"hosting.epp.heydon.io", "gordon", 0, "/var/www/html", "",
		},
		{
			"with explicit port",
			"@ssh://deploy@prod.example.com:2222/var/www#https://my-multisite.com",
			"prod.example.com", "deploy", 2222, "/var/www", "https://my-multisite.com",
		},
		{
			"no user, no root",
			"@ssh://host.example.com",
			"host.example.com", "", 0, "", "",
		},
		{
			"root via ?root=",
			"@ssh://deploy@prod?root=/var/www/html",
			"prod", "deploy", 0, "/var/www/html", "",
		},
		{
			"IPv6 bracketed host",
			"@ssh://user@[2001:db8::1]/var/www",
			"[2001:db8::1]", "user", 0, "/var/www", "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := ParseInline(c.token)
			if err != nil {
				t.Fatalf("ParseInline: %v", err)
			}
			if a.Site != "inline" {
				t.Errorf("Site = %q, want inline", a.Site)
			}
			if a.Env == "" {
				t.Errorf("Env not set (should be a hash of the URI)")
			}
			if a.Handler.Type != "ssh" {
				t.Errorf("Handler.Type = %q, want ssh", a.Handler.Type)
			}
			if a.Handler.SSH == nil {
				t.Fatalf("Handler.SSH is nil")
			}
			s := a.Handler.SSH
			if s.Host != c.wantHost {
				t.Errorf("host = %q, want %q", s.Host, c.wantHost)
			}
			if s.User != c.wantUser {
				t.Errorf("user = %q, want %q", s.User, c.wantUser)
			}
			if s.Port != c.wantPort {
				t.Errorf("port = %d, want %d", s.Port, c.wantPort)
			}
			if a.Root != c.wantRoot {
				t.Errorf("root = %q, want %q", a.Root, c.wantRoot)
			}
			if a.URI != c.wantURI {
				t.Errorf("uri = %q, want %q", a.URI, c.wantURI)
			}
		})
	}
}

func TestParseInlineDDEV(t *testing.T) {
	a, err := ParseInline("@ddev://hc?service=db")
	if err != nil {
		t.Fatal(err)
	}
	if a.Handler.Type != "ddev" || a.Handler.DDEV == nil {
		t.Fatalf("handler: %+v", a.Handler)
	}
	if a.Handler.DDEV.Project != "hc" {
		t.Errorf("project = %q", a.Handler.DDEV.Project)
	}
	if a.Handler.DDEV.Service != "db" {
		t.Errorf("service = %q", a.Handler.DDEV.Service)
	}
}

func TestParseInlineDDEVRequiresProject(t *testing.T) {
	if _, err := ParseInline("@ddev://"); err == nil {
		t.Error("expected error for bare @ddev://")
	}
}

func TestParseInlineCompose(t *testing.T) {
	a, err := ParseInline("@compose://gordon@hosting.epp.heydon.io/home/gordon/docker/heydon?service=php&root=/var/www/html")
	if err != nil {
		t.Fatal(err)
	}
	c := a.Handler.Compose
	if c == nil {
		t.Fatal("compose handler nil")
	}
	if c.Host != "ssh://gordon@hosting.epp.heydon.io" {
		t.Errorf("host = %q, want ssh:// form", c.Host)
	}
	if c.ProjectDir != "/home/gordon/docker/heydon" {
		t.Errorf("project_dir = %q", c.ProjectDir)
	}
	if c.Service != "php" {
		t.Errorf("service = %q", c.Service)
	}
	if a.Root != "/var/www/html" {
		t.Errorf("root = %q (from ?root=)", a.Root)
	}
}

func TestParseInlineComposeLocal(t *testing.T) {
	a, err := ParseInline("@compose:///path/to/project?service=php")
	if err != nil {
		t.Fatal(err)
	}
	c := a.Handler.Compose
	if c.Host != "" {
		t.Errorf("local compose should have no host: %q", c.Host)
	}
	if c.ProjectDir != "/path/to/project" {
		t.Errorf("project_dir = %q", c.ProjectDir)
	}
}

func TestParseInlineComposeServiceRequired(t *testing.T) {
	if _, err := ParseInline("@compose:///path/to/project"); err == nil {
		t.Error("expected error when ?service= is missing")
	}
}

func TestParseInlineComposeHostAndContextMutex(t *testing.T) {
	_, err := ParseInline("@compose://gordon@host/path?service=php&context=x")
	if err == nil {
		t.Fatal("expected error when both host and context are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutex error, got %v", err)
	}
}

func TestParseInlineDocker(t *testing.T) {
	a, err := ParseInline("@docker://gordon@hosting.epp.heydon.io/drupal-app?user=www-data")
	if err != nil {
		t.Fatal(err)
	}
	d := a.Handler.Docker
	if d.Container != "drupal-app" {
		t.Errorf("container = %q", d.Container)
	}
	if d.Host != "ssh://gordon@hosting.epp.heydon.io" {
		t.Errorf("host = %q", d.Host)
	}
	if d.User != "www-data" {
		t.Errorf("user = %q", d.User)
	}
}

func TestParseInlineDockerLocal(t *testing.T) {
	a, err := ParseInline("@docker://drupal-app")
	if err != nil {
		t.Fatal(err)
	}
	d := a.Handler.Docker
	if d.Container != "drupal-app" {
		t.Errorf("container = %q", d.Container)
	}
	if d.Host != "" {
		t.Errorf("local docker shouldn't have host: %q", d.Host)
	}
}

func TestParseInlineContainer(t *testing.T) {
	a, err := ParseInline("@container://drupal-app?user=www-data")
	if err != nil {
		t.Fatal(err)
	}
	if a.Handler.Type != "container" || a.Handler.Container == nil {
		t.Fatalf("handler: %+v", a.Handler)
	}
	c := a.Handler.Container
	if c.Container != "drupal-app" {
		t.Errorf("container = %q", c.Container)
	}
	if c.User != "www-data" {
		t.Errorf("user = %q", c.User)
	}
}

// TestParseInlineContainerRejectsHost — Apple container has no remote
// daemon. user@host syntax should be rejected loudly rather than
// silently dropped (it'd look like the docker scheme but wouldn't
// actually be tunnelled).
func TestParseInlineContainerRejectsHost(t *testing.T) {
	_, err := ParseInline("@container://gordon@host/drupal-app")
	if err == nil {
		t.Fatal("expected error for user@host in container URI")
	}
	if !strings.Contains(err.Error(), "no remote-host") {
		t.Errorf("error should explain the no-remote-host limitation: %v", err)
	}
}

func TestParseInlineKubectl(t *testing.T) {
	cases := []struct {
		token         string
		wantNamespace string
		wantResource  string
		wantContainer string
	}{
		{"@kubectl://drupal-pod", "", "drupal-pod", ""},
		{"@kubectl://prod/drupal-pod", "prod", "drupal-pod", ""},
		{"@kubectl://prod/drupal-pod?container=drupal", "prod", "drupal-pod", "drupal"},
		{"@k8s://prod/drupal-pod", "prod", "drupal-pod", ""}, // synonym
	}
	for _, c := range cases {
		t.Run(c.token, func(t *testing.T) {
			a, err := ParseInline(c.token)
			if err != nil {
				t.Fatal(err)
			}
			k := a.Handler.Kubectl
			if k.Namespace != c.wantNamespace {
				t.Errorf("namespace = %q, want %q", k.Namespace, c.wantNamespace)
			}
			if k.Resource != c.wantResource {
				t.Errorf("resource = %q, want %q", k.Resource, c.wantResource)
			}
			if k.Container != c.wantContainer {
				t.Errorf("container = %q, want %q", k.Container, c.wantContainer)
			}
			if a.Handler.Type != "kubectl" {
				t.Errorf("type = %q, want kubectl (k8s should normalise)", a.Handler.Type)
			}
		})
	}
}

func TestParseInlineLando(t *testing.T) {
	a, err := ParseInline("@lando:///Users/gordon/Source/myapp?service=appserver")
	if err != nil {
		t.Fatal(err)
	}
	l := a.Handler.Lando
	if l.AppDir != "/Users/gordon/Source/myapp" {
		t.Errorf("app_dir = %q", l.AppDir)
	}
	if l.Service != "appserver" {
		t.Errorf("service = %q", l.Service)
	}
}

func TestParseInlineAhoy(t *testing.T) {
	a, err := ParseInline("@ahoy:///path?task=drush")
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler.Ahoy
	if h.Dir != "/path" {
		t.Errorf("dir = %q", h.Dir)
	}
	if h.Task != "drush" {
		t.Errorf("task = %q", h.Task)
	}
}

// TestParseInlineRejectsPassword — URIs are not allowed to carry
// inline passwords; we want a clear error.
func TestParseInlineRejectsPassword(t *testing.T) {
	_, err := ParseInline("@ssh://user:secret@host/path")
	if err == nil {
		t.Fatal("expected error for inline password")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("error should mention password: %v", err)
	}
}

// TestParseInlineUnknownScheme — a scheme that's not registered should
// produce a friendly error listing the known schemes.
func TestParseInlineUnknownScheme(t *testing.T) {
	_, err := ParseInline("@unknown-scheme://host/path")
	if err == nil {
		t.Fatal("expected error for unknown scheme")
	}
	if !strings.Contains(err.Error(), "unknown URI scheme") {
		t.Errorf("error should mention unknown scheme: %v", err)
	}
}

// TestParseInlineStableEnv — the same URI must produce the same Env
// hash twice (so caching is stable).
func TestParseInlineStableEnv(t *testing.T) {
	a1, _ := ParseInline("@ssh://user@host/path")
	a2, _ := ParseInline("@ssh://user@host/path")
	if a1.Env != a2.Env {
		t.Errorf("identical URIs produced different Env: %q vs %q", a1.Env, a2.Env)
	}
	// Different URIs should produce different envs.
	a3, _ := ParseInline("@ssh://user@host/different")
	if a1.Env == a3.Env {
		t.Errorf("different URIs produced the same Env: %q", a1.Env)
	}
}

// TestParseInlineHandlerRoundTrips — the synthetic Alias must survive
// a yaml.Marshal → yaml.Unmarshal cycle and end up with identical
// handler config. This is what makes inline URIs interchangeable with
// hand-written YAML aliases.
func TestParseInlineHandlerRoundTrips(t *testing.T) {
	a, err := ParseInline("@compose://gordon@host/path?service=php")
	if err != nil {
		t.Fatal(err)
	}
	if a.Handler.Compose == nil {
		t.Fatal("typed compose field not populated — the YAML round-trip via newHandler is broken")
	}
	if a.Handler.Raw.Kind == 0 {
		t.Fatal("Handler.Raw.Kind == 0 — plugin Decode() would fail on this alias")
	}
}

// TestParseInlineRegressionDotHost — the host
// "hosting.epp.heydon.io" was the original parser-ambiguity case.
// IsInlineURIRef must accept it (it's a URI, not a site.env).
func TestParseInlineRegressionDotHost(t *testing.T) {
	token := "@ssh://gordon@hosting.epp.heydon.io/var/www/html"
	if !IsInlineURIRef(token) {
		t.Fatal("URI with dotted host wasn't recognised as a URI ref")
	}
	a, err := ParseInline(token)
	if err != nil {
		t.Fatal(err)
	}
	if a.Handler.SSH.Host != "hosting.epp.heydon.io" {
		t.Errorf("host = %q, want hosting.epp.heydon.io", a.Handler.SSH.Host)
	}
}
