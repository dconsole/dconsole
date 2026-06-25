package alias

import "gopkg.in/yaml.v3"

// AliasFile is the top-level structure of an `*.site.yml` file.
// Keys are environment names (dev, stage, prod, …). A reserved `_defaults`
// key is merged into every other env in the same file.
type AliasFile map[string]Alias

// Alias is one environment within a site file. After resolution it also
// carries the site name and env name it was looked up under.
type Alias struct {
	URI  string    `yaml:"uri,omitempty"`
	Root string    `yaml:"root,omitempty"`
	Bin  RemoteBin `yaml:"bin,omitempty"`

	// Handler is the v0.4.0+ unified config. A single handler may carry
	// a `via:` field to form a chain (e.g. ssh outer + docker inner).
	Handler Handler `yaml:"handler,omitempty"`
	// Handlers is the explicit-list form of a chain (mutually exclusive
	// with Handler — picking which is mostly stylistic).
	Handlers []Handler `yaml:"handlers,omitempty"`

	// Transport / Provider are the deprecated pre-v0.4.0 fields. Still
	// accepted on read for one minor cycle with a deprecation warning;
	// `UnmarshalYAML` does NOT auto-rewrite them, so anything iterating
	// over the alias in-memory needs to consult `LegacyChain()` which
	// folds the legacy fields into a synthetic []Handler.
	Transport Transport `yaml:"transport,omitempty"`
	Provider  Provider  `yaml:"provider,omitempty"`

	SQL     SQL               `yaml:"sql,omitempty"`
	Assets  Assets            `yaml:"assets,omitempty"`
	Policy  Policy            `yaml:"policy,omitempty"`
	EnvVars map[string]string `yaml:"env_vars,omitempty"`

	Site string `yaml:"-"`
	Env  string `yaml:"-"`
}

// Handler is a single layer of the v0.4.0 handler-chain schema. Mirrors
// the legacy Transport struct's typed-fields-plus-Raw pattern, then
// adds `via:` so it can carry the next inner layer when chained.
//
//	handler:
//	  type: ssh
//	  ssh: { host: ..., user: ... }
//	  via:
//	    type: docker
//	    docker: { container: ... }
type Handler struct {
	Type string `yaml:"type"`

	// Typed convenience fields for in-tree handlers (one of these is
	// non-nil depending on Type). Plugin handlers ignore these and use
	// Raw + Decode.
	Exec      *ExecTransport      `yaml:"exec,omitempty"`
	SSH       *SSHTransport       `yaml:"ssh,omitempty"`
	Docker    *DockerTransport    `yaml:"docker,omitempty"`
	Container *ContainerTransport `yaml:"container,omitempty"`
	Compose   *ComposeTransport   `yaml:"compose,omitempty"`
	Kubectl   *KubectlTransport   `yaml:"kubectl,omitempty"`
	DDEV      *DDEVTransport      `yaml:"ddev,omitempty"`
	Lando     *LandoTransport     `yaml:"lando,omitempty"`
	Ahoy      *AhoyTransport      `yaml:"ahoy,omitempty"`

	// Via is the next inner layer in a chain. The current layer wraps
	// Via's argv output. Nil for single-handler aliases.
	Via *Handler `yaml:"via,omitempty"`

	// Raw is the YAML mapping for this handler (for plugin Decode).
	Raw yaml.Node `yaml:"-"`
}

// UnmarshalYAML decodes typed fields and captures Raw so plugin
// factories can pull their own config.
func (h *Handler) UnmarshalYAML(n *yaml.Node) error {
	type plain Handler
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*h = Handler(p)
	h.Raw = *n
	return nil
}

// Decode unmarshals the handler's YAML block into v. Use from plugin
// factories to pull their typed config out of the opaque mapping.
func (h *Handler) Decode(v any) error {
	return h.Raw.Decode(v)
}

// LegacyChain returns the alias's handler config as a flat outer-to-inner
// list, normalising across the three schema shapes (Handler, Handlers,
// or the deprecated Transport[+Provider] pair). Returns an empty slice
// if nothing is configured; returns an error if multiple forms are mixed.
//
// The legacy `transport: + provider:` combination is treated as a chain
// with transport outer and provider inner — preserving the intent of
// "reach the box via transport, then run provider-flavoured ops
// against it". Real-world hosting platforms (Skpr, Ironstar) ran their
// CLI locally and never used a separate transport, so this case is
// mostly defensive.
func (a *Alias) LegacyChain() ([]Handler, error) {
	hasHandler := a.Handler.Type != ""
	hasHandlers := len(a.Handlers) > 0
	hasTransport := a.Transport.Type != ""
	hasProvider := a.Provider.Type != ""

	switch {
	case hasHandler && hasHandlers:
		return nil, fmtErr(a, "defines both `handler:` and `handlers:` — pick one")
	case (hasHandler || hasHandlers) && (hasTransport || hasProvider):
		return nil, fmtErr(a, "mixes the new `handler:`/`handlers:` schema with the deprecated `transport:`/`provider:` schema — pick one")
	case hasHandlers:
		return a.Handlers, nil
	case hasHandler:
		return flattenVia(a.Handler), nil
	case hasTransport && hasProvider:
		return []Handler{
			handlerFromTransport(a.Transport),
			handlerFromProvider(a.Provider),
		}, nil
	case hasTransport:
		return []Handler{handlerFromTransport(a.Transport)}, nil
	case hasProvider:
		return []Handler{handlerFromProvider(a.Provider)}, nil
	}
	return nil, nil
}

// flattenVia walks the .Via chain of a singular handler into a flat
// slice, outer first.
func flattenVia(h Handler) []Handler {
	out := []Handler{h}
	// Strip the chain link off the head we record, so each entry stands
	// alone (otherwise the head carries its own via pointer too).
	out[0].Via = nil
	for via := h.Via; via != nil; via = via.Via {
		copy := *via
		copy.Via = nil
		out = append(out, copy)
	}
	return out
}

// handlerFromTransport copies a legacy Transport block into the new
// Handler shape. The two have the same typed-fields layout so this is
// largely a field-by-field move.
func handlerFromTransport(t Transport) Handler {
	return Handler{
		Type:      t.Type,
		Exec:      t.Exec,
		SSH:       t.SSH,
		Docker:    t.Docker,
		Container: t.Container,
		Compose:   t.Compose,
		Kubectl:   t.Kubectl,
		DDEV:      t.DDEV,
		Lando:     t.Lando,
		Raw:       t.Raw,
	}
}

// handlerFromProvider copies a legacy Provider block into the new
// Handler shape. Providers had no typed fields — only Type + Raw — so
// downstream factories rely on Raw.Decode for their own config.
func handlerFromProvider(p Provider) Handler {
	return Handler{
		Type: p.Type,
		Raw:  p.Raw,
	}
}

// fmtErr formats a per-alias error with the same `@site.env` decoration
// pkg/handler.For() uses, keeping error messages consistent across
// load + dispatch.
func fmtErr(a *Alias, msg string) error {
	return &aliasError{site: a.Site, env: a.Env, msg: msg}
}

type aliasError struct{ site, env, msg string }

func (e *aliasError) Error() string {
	if e.site == "" && e.env == "" {
		return e.msg
	}
	return "@" + e.site + "." + e.env + ": " + e.msg
}

// Assets configures `dconsole rsync` behaviour for the alias's files
// directories (sites/default/files, the private dir, …). The cache TTL
// only applies to the provider-FilesDownload path; rsync/diff modes
// are inherently fresh per run.
type Assets struct {
	Cache AssetsCacheConfig `yaml:"cache,omitempty"`
}

type AssetsCacheConfig struct {
	// TTL is how long a cached provider-supplied asset bundle stays
	// fresh. Go duration string ("6h", "30m"). Empty falls back to
	// the assetscache default of 24h.
	TTL string `yaml:"ttl,omitempty"`
}

// Policy controls which other envs may sync with this one. Default is
// "open" — no restrictions. Set sync_policy: protected to reject all
// inbound/outbound sync unless the other env appears in an allow_sync_*
// list. The user can always override with --force.
//
// Direction model:
//   - allow_sync_from: lists envs (by name within the same site) that may
//     PULL from this env. I.e. when this is the SOURCE of sql:sync/rsync,
//     the target's env name must appear here.
//   - allow_sync_to: lists envs that this env may PUSH to. I.e. when this
//     is the SOURCE, target's env name must appear here. (Same direction,
//     stated from the source side.)
//
// In practice, define direction on whichever side is more conservative.
// dconsole consults BOTH sides: a sync proceeds only if neither side's
// policy refuses it.
type Policy struct {
	SyncPolicy     string   `yaml:"sync_policy,omitempty"` // "" or "open" | "protected"
	AllowSyncFrom  []string `yaml:"allow_sync_from,omitempty"`
	AllowSyncTo    []string `yaml:"allow_sync_to,omitempty"`
}

// SQL configures how this alias produces a database dump (Source) and how
// it consumes one (Target) during `dconsole sql:sync`. Both default to
// `drush` semantics — i.e. shell out to the remote CLI's sql:dump/sql:cli
// — if omitted. Within a single deployment, all envs are expected to use
// the same strategy; the schema doesn't enforce that but operationally
// it's the sensible default.
type SQL struct {
	Source SQLSource      `yaml:"source,omitempty"`
	Target SQLTarget      `yaml:"target,omitempty"`
	Cache  SQLCacheConfig `yaml:"cache,omitempty"`
}

// SQLCacheConfig tunes dconsole's local cache of source dumps for this
// alias. Empty values use defaults from the dbcache package.
type SQLCacheConfig struct {
	// TTL is how long a cached dump is considered fresh. Go duration
	// string ("6h", "30m", "24h"). Empty falls back to the dbcache
	// default of 24h.
	TTL string `yaml:"ttl,omitempty"`
}

// SQLSource selects how dconsole obtains a dump for this alias.
//
// type values:
//   - "drush" (default): run `<bin> sql:dump --gzip` via the alias's
//     transport and capture stdout. Output is gzipped.
//   - "file": fetch a pre-made dump file at Path via the alias's transport
//     (cat over the transport). Set Gzipped to declare whether the file
//     is gzipped.
//   - "docker_cp": shell out to `docker cp <container>:<path> <local>`
//     directly, bypassing the alias's transport. Useful when a dedicated
//     dump container ships a fresh backup at a known path and you don't
//     want to install drush inside it.
type SQLSource struct {
	Type      string `yaml:"type,omitempty"`
	Path      string `yaml:"path,omitempty"`
	Container string `yaml:"container,omitempty"`
	Gzipped   *bool  `yaml:"gzipped,omitempty"`

	// Database is the drush DB connection key to dump from
	// (--database=<key>). Defaults to "default" when unset.
	Database string `yaml:"database,omitempty"`

	// StructureTables lists table names (globs OK) to dump schema-only,
	// forwarded as `drush sql:dump --structure-tables-list=...`. Useful
	// for cache_*, sessions, watchdog tables you don't want data for.
	// Drush-strategy only — ignored by file / docker_cp / provider dumps.
	StructureTables []string `yaml:"structure_tables,omitempty"`

	// StructureTablesKey references a named array in drush's own config
	// (e.g. "common"), forwarded as `--structure-tables-key=`. Mutually
	// usable with StructureTables; drush merges them.
	StructureTablesKey string `yaml:"structure_tables_key,omitempty"`
}

// SQLTarget selects how dconsole loads a dump into this alias. type:
// "drush" (default) feeds the dump via stdin to `<bin> sql:cli`. Future
// strategies (e.g. a target-side restore script) plug in here.
type SQLTarget struct {
	Type string `yaml:"type,omitempty"`

	// Database is the drush DB connection key to load INTO
	// (--database=<key>). Forwarded to `drush sql:cli --database=` and
	// `ddev import-db --database=`. Defaults to "default" when unset.
	Database string `yaml:"database,omitempty"`
}

// SourceGzipped reports whether SQLSource expects gzipped bytes. Defaults
// to true (which matches drush's --gzip behavior and the *.sql.gz suffix
// most providers use).
func (s SQLSource) SourceGzipped() bool {
	if s.Gzipped == nil {
		return true
	}
	return *s.Gzipped
}

// RemoteBin selects which CLI lives on the remote: drush, drupal, or auto.
type RemoteBin struct {
	Kind string `yaml:"kind,omitempty"`
	Path string `yaml:"path,omitempty"`
}

// Transport selects how dconsole reaches the remote.
//
// In-tree transports get their typed config decoded into the matching
// pointer field (Exec, SSH, …) by yaml.v3. Out-of-tree subprocess
// plugins access their raw YAML block via Raw and decode it with
// Decode(&cfg). Raw is populated for every Transport, in-tree or not.
type Transport struct {
	Type      string              `yaml:"type"`
	Exec      *ExecTransport      `yaml:"exec,omitempty"`
	SSH       *SSHTransport       `yaml:"ssh,omitempty"`
	Docker    *DockerTransport    `yaml:"docker,omitempty"`
	Container *ContainerTransport `yaml:"container,omitempty"`
	Compose   *ComposeTransport   `yaml:"compose,omitempty"`
	Kubectl   *KubectlTransport   `yaml:"kubectl,omitempty"`
	DDEV      *DDEVTransport      `yaml:"ddev,omitempty"`
	Lando     *LandoTransport     `yaml:"lando,omitempty"`

	// Raw is the original YAML mapping node so plugin transports can
	// decode their own config block via Decode(). Populated by
	// UnmarshalYAML; not emitted on marshal (write-path uses the typed
	// fields).
	Raw yaml.Node `yaml:"-"`
}

// UnmarshalYAML decodes both the typed convenience fields and captures
// the raw node so plugin factories can decode their own block.
func (t *Transport) UnmarshalYAML(n *yaml.Node) error {
	// Decode into a shadow type to avoid recursion into this method.
	type plain Transport
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*t = Transport(p)
	t.Raw = *n
	return nil
}

// Decode unmarshals the transport's YAML block into v. Use from
// out-of-tree (or in-tree future) factories to pull their typed config:
//
//	var cfg myTransport
//	if err := a.Transport.Decode(&cfg); err != nil { … }
func (t *Transport) Decode(v any) error {
	return t.Raw.Decode(v)
}

// NewTransport constructs a Transport from a type name + nested config
// struct, populating both the typed pointer fields and Raw so the
// result is identical to one produced by yaml.Unmarshal. Use when
// building aliases in-memory (tests, project generators, rsync's
// internal exec helper). Panics on programmer-error inputs because
// the only failure mode is "you handed me an unmarshalable config",
// which is a build-time mistake.
func NewTransport(typ string, cfg any) Transport {
	doc := map[string]any{"type": typ}
	if cfg != nil {
		doc[typ] = cfg
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		panic("alias.NewTransport: marshal: " + err.Error())
	}
	var t Transport
	if err := yaml.Unmarshal(body, &t); err != nil {
		panic("alias.NewTransport: unmarshal: " + err.Error())
	}
	return t
}

type ExecTransport struct {
	Dir string `yaml:"dir,omitempty"` // optional working directory
}

type SSHTransport struct {
	Host         string `yaml:"host"`
	User         string `yaml:"user,omitempty"`
	Port         int    `yaml:"port,omitempty"`
	IdentityFile string `yaml:"identity_file,omitempty"`

	// Options are extra arguments passed to ssh. Two input shapes are
	// accepted:
	//   - Argv tokens: ["-o", "BatchMode=yes", "-C"]
	//   - Bare key=value shortcuts: ["BatchMode=yes", "ConnectTimeout=20"]
	// dconsole auto-prefixes "-o" to bare key=value tokens, so the
	// shortcut form behaves the way most users expect.
	Options []string `yaml:"options,omitempty"`
}

// ContainerTransport spawns commands via Apple's `container` CLI —
// a lightweight OCI runtime that ships with macOS 15+ and mirrors
// docker's command surface. No Host/Context fields: the Apple CLI
// doesn't support remote daemons (yet); everything is local.
type ContainerTransport struct {
	// Container is the running container name or ID.
	Container string `yaml:"container"`
	// User sets the in-container user (e.g. "www-data" or "uid:gid").
	User string `yaml:"user,omitempty"`
	// ExecOptions are extra flags inserted between `exec` and the
	// container ID (e.g. ["--env", "FOO=bar"]).
	ExecOptions []string `yaml:"exec_options,omitempty"`
}

type DockerTransport struct {
	Container   string   `yaml:"container"`
	User        string   `yaml:"user,omitempty"`
	ExecOptions []string `yaml:"exec_options,omitempty"`

	// Host points the docker CLI at a remote daemon, e.g.
	// "ssh://user@host" (most common; docker tunnels over ssh
	// internally), "tcp://host:2376" with TLS, or
	// "unix:///custom/socket". Maps to `docker -H <value>`.
	// Mutually exclusive with Context.
	Host string `yaml:"host,omitempty"`

	// Context names a saved docker-context entry on the local machine
	// (`docker context create`). Maps to `docker --context <name>`.
	// Mutually exclusive with Host.
	Context string `yaml:"context,omitempty"`
}

type ComposeTransport struct {
	ProjectDir  string   `yaml:"project_dir"`
	Service     string   `yaml:"service"`
	ExecOptions []string `yaml:"exec_options,omitempty"`

	// Host / Context — see DockerTransport for semantics. Compose
	// inherits these via `docker [-H … | --context …] compose …`.
	Host    string `yaml:"host,omitempty"`
	Context string `yaml:"context,omitempty"`
}

type KubectlTransport struct {
	Namespace  string `yaml:"namespace,omitempty"`
	Resource   string `yaml:"resource"`
	Container  string `yaml:"container,omitempty"`
	Kubeconfig string `yaml:"kubeconfig,omitempty"`
}

type DDEVTransport struct {
	Project string `yaml:"project,omitempty"`
	Service string `yaml:"service,omitempty"`
}

type LandoTransport struct {
	AppDir  string `yaml:"app_dir,omitempty"`
	Service string `yaml:"service,omitempty"`
}

// AhoyTransport runs commands via the `ahoy` CLI, which reads tasks
// from a `.ahoy.yml` at or above the working directory. Common in
// Drupal dev environments as a thin abstraction over ddev / lando /
// docker-compose.
//
// dconsole rewrites a remote command like `[drush, sql:dump]` to
// `ahoy <task> sql:dump` — the task name defaults to the basename of
// the resolved bin (typically "drush" or "drupal") so the user just
// needs to define `drush:` / `drupal:` tasks in their .ahoy.yml.
type AhoyTransport struct {
	// Dir is the directory dconsole runs `ahoy` from. ahoy walks up
	// from this directory looking for .ahoy.yml. Defaults to alias.Root
	// when unset; if .ahoy.yml isn't reachable from either, transport
	// construction fails with a clear error.
	Dir string `yaml:"dir,omitempty"`

	// Task overrides the ahoy task name. When empty, dconsole uses the
	// basename of the resolved bin path (so `bin.kind=drush` →
	// `ahoy drush`).
	Task string `yaml:"task,omitempty"`
}

// Provider attaches a hosting-provider plugin to an alias. dconsole
// ships with no in-tree providers — every implementation arrives as a
// subprocess plugin. Plugin factories decode their YAML block via
// Raw + Decode().
type Provider struct {
	Type string `yaml:"type"`

	// Raw captures the YAML mapping so the plugin's Build function can
	// pull its typed config out via Decode(&cfg).
	Raw yaml.Node `yaml:"-"`
}

// UnmarshalYAML mirrors Transport.UnmarshalYAML — typed fields decode
// normally, Raw captures the whole node.
func (p *Provider) UnmarshalYAML(n *yaml.Node) error {
	type plain Provider
	var tmp plain
	if err := n.Decode(&tmp); err != nil {
		return err
	}
	*p = Provider(tmp)
	p.Raw = *n
	return nil
}

// Decode unmarshals the provider's YAML block into v. Use from plugin
// factories to pull their typed config out of the opaque mapping.
func (p *Provider) Decode(v any) error {
	return p.Raw.Decode(v)
}

// NewProvider mirrors NewTransport for providers.
func NewProvider(typ string, cfg any) Provider {
	doc := map[string]any{"type": typ}
	if cfg != nil {
		doc[typ] = cfg
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		panic("alias.NewProvider: marshal: " + err.Error())
	}
	var p Provider
	if err := yaml.Unmarshal(body, &p); err != nil {
		panic("alias.NewProvider: unmarshal: " + err.Error())
	}
	return p
}

