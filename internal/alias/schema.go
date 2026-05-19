package alias

import "gopkg.in/yaml.v3"

// AliasFile is the top-level structure of an `*.site.yml` file.
// Keys are environment names (dev, stage, prod, …). A reserved `_defaults`
// key is merged into every other env in the same file.
type AliasFile map[string]Alias

// Alias is one environment within a site file. After resolution it also
// carries the site name and env name it was looked up under.
type Alias struct {
	URI       string    `yaml:"uri,omitempty"`
	Root      string    `yaml:"root,omitempty"`
	Bin       RemoteBin `yaml:"bin,omitempty"`
	Transport Transport `yaml:"transport,omitempty"`
	Provider  Provider  `yaml:"provider,omitempty"`
	SQL       SQL       `yaml:"sql,omitempty"`
	Policy    Policy    `yaml:"policy,omitempty"`
	EnvVars   map[string]string `yaml:"env_vars,omitempty"`

	Site string `yaml:"-"`
	Env  string `yaml:"-"`
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
	Source SQLSource `yaml:"source,omitempty"`
	Target SQLTarget `yaml:"target,omitempty"`
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
}

// SQLTarget selects how dconsole loads a dump into this alias. type:
// "drush" (default) feeds the dump via stdin to `<bin> sql:cli`. Future
// strategies (e.g. a target-side restore script) plug in here.
type SQLTarget struct {
	Type string `yaml:"type,omitempty"`
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
	Type    string            `yaml:"type"`
	Exec    *ExecTransport    `yaml:"exec,omitempty"`
	SSH     *SSHTransport     `yaml:"ssh,omitempty"`
	Docker  *DockerTransport  `yaml:"docker,omitempty"`
	Compose *ComposeTransport `yaml:"compose,omitempty"`
	Kubectl *KubectlTransport `yaml:"kubectl,omitempty"`
	DDEV    *DDEVTransport    `yaml:"ddev,omitempty"`
	Lando   *LandoTransport   `yaml:"lando,omitempty"`

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
	Host         string   `yaml:"host"`
	User         string   `yaml:"user,omitempty"`
	Port         int      `yaml:"port,omitempty"`
	IdentityFile string   `yaml:"identity_file,omitempty"`
	Options      []string `yaml:"options,omitempty"`
}

type DockerTransport struct {
	Container   string   `yaml:"container"`
	User        string   `yaml:"user,omitempty"`
	ExecOptions []string `yaml:"exec_options,omitempty"`
}

type ComposeTransport struct {
	ProjectDir  string   `yaml:"project_dir"`
	Service     string   `yaml:"service"`
	ExecOptions []string `yaml:"exec_options,omitempty"`
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

