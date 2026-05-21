# dconsole-echo — reference plugin

`dconsole-echo` is the canonical example transport plugin for dconsole.
It implements the full subprocess plugin protocol (see
[`pkg/plugin`](../../pkg/plugin)) but performs no real work — every
verb echoes what it would have done to stderr and streams stdin to
stdout.

## What it's for

- **Integration-test fixture.** dconsole's own subprocess-runner tests
  build this binary and exercise every verb against it.
- **Reference implementation.** Copy `main.go` as a starting point when
  you're writing your own plugin (e.g. `dconsole-skpr`).
- **Smoke test.** Drop it into `~/.dconsole/plugins/`, give dconsole an
  alias with `transport: { type: echo }`, and run any command — the
  plugin's stderr shows the verb and the alias envelope dconsole built.

## Build & install

```sh
# From the dconsole repo root:
go build -o dconsole-echo ./examples/dconsole-echo

# Install for personal use (no sudo, no PATH editing required):
mkdir -p ~/.dconsole/plugins
ln -sf "$PWD/dconsole-echo" ~/.dconsole/plugins/
```

Or once `dconsole plugin install` is wired up, you can install a local
tarball:

```sh
tar -czf dconsole-echo.tgz dconsole-echo
dconsole plugin install --path=./dconsole-echo.tgz
```

## Try it

Copy [`echo.site.yml.example`](./echo.site.yml.example) into a
dconsole site directory as `echo.site.yml`. Then:

```sh
dconsole @echo.dev status
# [dconsole-echo] verb=exec alias=@echo.dev remoteCmd=[drush status]
# [dconsole-echo] alias envelope: site=echo env=dev uri=https://example.test root=/var/www/example
```

`dconsole inspect @echo.dev status` will call the plugin's `preview`
verb and show its planned argv (`echo: drush status`).

## Wire protocol

See [`pkg/plugin/plugin.go`](../../pkg/plugin/plugin.go) for the full
spec. In short:

- `plugin-info` → JSON document on stdout describing the plugin.
- `available` → exit 0 iff the plugin can be invoked right now.
- `exec`, `pipe` → run a remote command. Stdio is wired straight
  through. argv is everything after a literal `--`.
- `shell` → drop into an interactive shell. (Echo exits with
  `ExitVerbUnsupported` = 64 for this.)
- `preview` → emit the planned local argv as a JSON string array; used
  by `dconsole inspect`.

All verbs receive `--alias-json=<path>` where the file contains an
[`AliasEnvelope`](../../pkg/plugin/plugin.go) describing the alias the
operation is targeting.

## Distribution

Plugins are listed in the curated index at
[`dconsole/dconsole-plugin-index`](https://github.com/dconsole/dconsole-plugin-index)
(separate repo). Authors PR a single YAML file describing their plugin
and the per-platform release artifact URLs + sha256s — see
[`docs/plugins/distribution.md`](../../docs/plugins/distribution.md).
