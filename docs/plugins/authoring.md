# Writing a dconsole plugin

A dconsole plugin is a standalone executable named `dconsole-<type>`
that implements a small JSON-over-argv protocol. dconsole spawns it on
demand when a site alias references a transport type (or provider
type) it doesn't know about.

The canonical reference implementation lives at
[`examples/dconsole-echo/main.go`](../../examples/dconsole-echo/main.go).
Copy it as a starting point.

---

## 1. The protocol in a sentence

dconsole calls `dconsole-<type> <verb> --alias-json=<file> [-- args...]`
once per operation. The plugin reads the alias from the JSON file,
does its thing, and exits. Stdio is wired straight through, so
streaming (sql:dump) and interactive (shell) work for free.

## 2. Verbs

| Verb               | Purpose                                                                                                       | argv                                                       | stdio                                |
|--------------------|---------------------------------------------------------------------------------------------------------------|------------------------------------------------------------|--------------------------------------|
| `plugin-info`      | Identify yourself. dconsole calls this once per session before anything else.                                 | `dconsole-foo plugin-info`                                 | stdout: JSON `PluginInfo` document.  |
| `available`        | Are runtime prerequisites met right now?                                                                      | `dconsole-foo available --alias-json=…`                    | exit 0 = yes, ≠ 0 = no (msg on stderr). |
| `exec`             | Run a remote command, attached to user's TTY.                                                                 | `dconsole-foo exec --alias-json=… -- drush cr`             | all three streams plumbed through.   |
| `pipe`             | Run a remote command with explicit stdin/stdout (e.g. streaming a gzip dump).                                 | `dconsole-foo pipe --alias-json=… -- drush sql:dump`       | stdin → remote; remote stdout → stdout. |
| `shell`            | Drop user into an interactive shell on the remote.                                                            | `dconsole-foo shell --alias-json=… [--workdir=/var/www]`   | full TTY.                            |
| `preview`          | Return the local argv you *would* spawn for a given remote command. Used by `dconsole inspect`. Optional.     | `dconsole-foo preview --alias-json=… -- drush cr`          | stdout: JSON string array.           |
| `dump-for`         | (Provider plugins) emit a local path to a dump of the source DB on stdout.                                    | `dconsole-foo dump-for --alias-json=…`                     | stdout: dump file path.              |
| `load-for`         | (Provider) load a local dump into the target.                                                                 | `dconsole-foo load-for --alias-json=… --dump-path=/tmp/…`  | passthrough.                         |
| `files-download`   | (Provider) download the alias's files directory into `--target-dir`.                                          | `dconsole-foo files-download --alias-json=… --target-dir=…`| passthrough.                         |
| `login`            | (Provider/transport) run an auth flow. Optional. dconsole invokes this from `dconsole @alias auth`.           | `dconsole-foo login --alias-json=…`                        | TTY plumbed.                         |

## 3. Exit codes

Plugins MUST use these codes for the documented conditions; dconsole
maps them to error types.

| Code | Constant            | Meaning                                                                 |
|------|---------------------|-------------------------------------------------------------------------|
| 0    | `ExitOK`            | Success.                                                                |
| 1    | `ExitGeneric`       | Generic failure. Stderr explains.                                       |
| 64   | `ExitVerbUnsupported` | Optional verb the plugin doesn't implement. dconsole produces a clear "shell not supported" message; other verbs fall through. |
| 65   | `ExitProtocolError` | Malformed argv, missing required flags, unparseable alias-json.         |
| 66   | `ExitNotSupported`  | Provider returns "I don't support this for this alias" — dconsole falls back to the generic path. |

Use the constants in
[`pkg/plugin/plugin.go`](../../pkg/plugin/plugin.go) if your plugin is
written in Go.

## 4. `plugin-info` JSON

Emit on stdout, no trailing garbage. dconsole tolerates whitespace and
leading non-JSON noise (banner lines etc.) but be tidy.

```json
{
  "protocol_version": 1,
  "name": "skpr",
  "version": "1.0.0",
  "description": "Skpr hosting integration",
  "homepage": "https://github.com/skpr-io/dconsole-skpr",
  "transports": [
    {"type": "skpr", "required_cli": "skpr", "description": "Drupal hosting on Skpr"}
  ],
  "providers": [
    {"type": "skpr", "required_cli": "skpr", "description": "Skpr backups + file sync"}
  ]
}
```

`protocol_version` MUST be `1` for the v1 protocol. dconsole refuses
plugins with an unrecognised version.

`transports[].type` and `providers[].type` are what users put in their
`dconsole.site.yml` under `transport: { type: X }` and
`provider: { type: X }` respectively. One plugin binary can register
multiple types if convenient.

## 5. The alias envelope (`--alias-json`)

Every verb (except `plugin-info`) receives `--alias-json=<filepath>`.
The file contains an `AliasEnvelope`:

```json
{
  "site": "demo",
  "env": "dev",
  "uri": "https://demo.test",
  "root": "/var/www/demo",
  "bin": {"kind": "drush", "path": "/usr/local/bin/drush"},
  "config":   { "type": "skpr", "skpr": { "account": "demo", "environment": "production" } },
  "provider": { "type": "skpr", "skpr": { "api_token": "…" } }
}
```

`config` is the raw transport mapping from the YAML — your plugin's
own block lives nested under its type key (`skpr`/`fake`/…), and
you decode just that part. `provider` is the symmetric provider
mapping. Both fields are optional.

Use the literal `--` separator to mark the end of dconsole's flags
and the start of remote-command args:

```
dconsole-skpr exec --alias-json=/tmp/dconsole-alias-…json -- drush cr --verbose
```

## 6. Streaming and signals

- **Stdio** for `exec`/`pipe`/`shell`/`login` is plumbed straight from
  the dconsole process to the plugin. Don't buffer remote output —
  dconsole expects byte-for-byte passthrough for `pipe` (sql:dump
  consumers depend on it).
- **Context cancellation** from dconsole becomes `SIGTERM` on the
  plugin process. Forward it to any subprocess you've spawned. Don't
  ignore it.
- **Exit codes**: forward the underlying command's exit code as your
  own when possible — that's how dconsole's `Forward` reports drush
  errors back to the user.

## 7. Picking transport vs provider

If your integration wraps `ssh`/`docker exec`/`kubectl exec`-style
remote-command execution, it's a **transport**. Implement `exec`/`pipe`/
`shell`/`preview`/`available`.

If it sits *next* to a transport — pulling pre-computed DB backups,
syncing files, providing the auth flow — it's a **provider**. Implement
`dump-for`/`load-for`/`files-download`/`login`. The provider's transport
is whatever the alias's `transport:` block says.

Plugins can declare both in a single `plugin-info` document if they
naturally bundle (e.g. Skpr offers both a transport AND a provider).

## 8. Writing the plugin in Go

Depend on `github.com/dconsole/dconsole/pkg/plugin` (the wire types) and
optionally `github.com/dconsole/dconsole/pkg/transport` /
`github.com/dconsole/dconsole/pkg/provider` (the SDK interfaces, useful
if you're embedding a transport in another tool). **Do not import
`github.com/dconsole/dconsole/internal/*`** — those packages are not part
of the stable API.

The echo plugin is a complete, working example in ~150 LOC.

## 9. Writing the plugin in another language

Nothing about the protocol is Go-specific. The wire is argv +
stdin/stdout + JSON on a file. Any language that can:

- read argv,
- read a file,
- parse JSON,
- write JSON to stdout,
- exit with a numeric code,

can implement a plugin. Bash + jq is fine for prototyping.

## 10. Testing

dconsole's own subprocess-runner test
([`internal/transport/subprocess_test.go`](../../internal/transport/subprocess_test.go))
builds the echo plugin at test time and exercises every verb against
it. Adopt the same pattern: build your plugin into `t.TempDir()`,
point `XDG_DATA_HOME` at it, and run dconsole-driven assertions.

For development without packaging, just symlink your binary:

```sh
mkdir -p ~/.dconsole/plugins
ln -sf "$PWD/dconsole-skpr" ~/.dconsole/plugins/
```

Then any dconsole alias with `transport: { type: skpr }` will route
through your in-development binary.

## Licensing your plugin

The plugin SDK packages (`pkg/plugin`, `pkg/provider`, `pkg/transport`)
are licensed under **Apache-2.0**. Importing them carries no copyleft
obligation. You can publish your plugin under any license: MIT, BSD,
Apache, GPL, proprietary, or commercial.

The dconsole CLI itself stays under GPL-2.0, so anyone modifying or
redistributing dconsole's core stays under copyleft — but a plugin
distributed as a separate binary that talks to dconsole over its
documented protocol is not a derivative work of the CLI.

See the top-level [README License section](../../README.md#license)
for the full split.

## See also

- [Distribution](./distribution.md) — how to publish a release and get
  listed in the curated index so users can `dconsole plugin install`.
- [Echo reference plugin](../../examples/dconsole-echo) — full working
  Go source.
- [`pkg/plugin/plugin.go`](../../pkg/plugin/plugin.go) — protocol
  constants and types in code form.
