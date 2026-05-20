package plugin

// ProtocolV1 is the current wire-protocol version dconsole understands.
// Plugins MUST return this (or a compatible value) from their
// "plugin-info" verb's ProtocolVersion field.
const ProtocolV1 = 1

// Verb names. Plugins MUST switch on argv[1] and implement the verbs
// they support. Unsupported optional verbs (Shell, FilesDownload) may
// exit with code VerbUnsupported and a short message on stderr.
const (
	VerbPluginInfo    = "plugin-info"
	VerbAvailable     = "available"
	VerbExec          = "exec"
	VerbPipe          = "pipe"
	VerbShell         = "shell"
	VerbPreview       = "preview"
	VerbDumpFor       = "dump-for"
	VerbLoadFor       = "load-for"
	VerbFilesDownload = "files-download"
	VerbLogin         = "login"
	// VerbSyncTo lets a provider take over an entire sql:sync end-to-end
	// (e.g. Skpr's "pull pre-built image" pattern that bypasses the
	// dump+load chain). Providers that don't implement it should exit
	// with ExitVerbUnsupported or ExitNotSupported; dconsole then falls
	// back to the standard dump+load flow.
	VerbSyncTo = "sync-to"
)

// Exit codes. Plugins MUST use these for the documented conditions;
// dconsole maps them to error types.
const (
	ExitOK              = 0 // success
	ExitGeneric         = 1 // generic failure (stderr explains)
	ExitVerbUnsupported = 64
	ExitProtocolError   = 65 // malformed argv, missing required flags, bad JSON
	ExitNotSupported    = 66 // provider-style ErrNotSupported (caller may fall through)
)

// PluginInfo is the JSON document a plugin emits on stdout in response
// to the "plugin-info" verb. dconsole reads this once per session and
// caches it.
type PluginInfo struct {
	// ProtocolVersion is the wire protocol the plugin speaks. dconsole
	// refuses plugins whose version it doesn't recognise.
	ProtocolVersion int `json:"protocol_version"`
	// Name is the plugin's short identifier (matches the suffix of the
	// binary name: "dconsole-skpr" → "skpr"). Used in help text and
	// errors.
	Name string `json:"name"`
	// Version is the plugin's own release version (semver string,
	// informational).
	Version string `json:"version"`
	// Transports lists transport types this plugin implements. dconsole
	// uses these to dispatch unknown transport types to the plugin.
	Transports []TransportSpec `json:"transports,omitempty"`
	// Providers lists provider types this plugin implements. Symmetric
	// with Transports.
	Providers []ProviderSpec `json:"providers,omitempty"`
	// Description is a short one-line summary shown in `dconsole plugin
	// list` and `dconsole plugin info`.
	Description string `json:"description,omitempty"`
	// Homepage is an optional URL for users to learn more / file bugs.
	Homepage string `json:"homepage,omitempty"`
}

// TransportSpec describes one transport type this plugin provides.
type TransportSpec struct {
	Type        string `json:"type"`
	RequiredCLI string `json:"required_cli,omitempty"`
	Description string `json:"description,omitempty"`
}

// ProviderSpec describes one provider type this plugin provides.
type ProviderSpec struct {
	Type        string `json:"type"`
	RequiredCLI string `json:"required_cli,omitempty"`
	Description string `json:"description,omitempty"`
}

// AliasEnvelope is the JSON document written to the file passed via
// --alias-json. Plugins decode it to get the alias they're operating
// against. The shape is intentionally a subset of dconsole's internal
// Alias struct — only the fields a transport/provider plugin should
// reasonably need.
type AliasEnvelope struct {
	Site     string         `json:"site"`
	Env      string         `json:"env"`
	URI      string         `json:"uri,omitempty"`
	Root     string         `json:"root,omitempty"`
	Bin      BinSpec        `json:"bin,omitempty"`
	Config   map[string]any `json:"config,omitempty"`
	Provider map[string]any `json:"provider,omitempty"`
}

// BinSpec mirrors alias.RemoteBin in a form safe to expose to plugins.
type BinSpec struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
}
