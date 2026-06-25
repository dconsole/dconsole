# dconsole

A transport-agnostic CLI proxy for Drupal — one command surface across
`ssh`, `ddev`, `lando`, `ahoy`, `docker`, `docker-compose`, `kubectl`,
and any subprocess plugin you wire up.

`dconsole @site.env <drush command>` forwards to the right CLI on the
right machine, regardless of how that machine is reached. On top of
forwarding, dconsole adds a small set of higher-level commands that
solve the cross-environment chores Drupal teams write the same scripts
for over and over: pulling databases, syncing files, opening a one-time
admin login, registering project directories so aliases auto-resolve.

```sh
dconsole @example.live status                                 # forward to drush, anywhere
dconsole sql:sync @example.live @example.local                # cached, transport-aware DB sync
dconsole rsync @example.live @example.local --include-private # files (sites/default/files, private/)
dconsole login @example.local                                 # drush uli + open in browser
dconsole @example.live --help                                 # merged drush + dconsole help
```

## Why

The Drupal ecosystem has settled on `drush` as the lingua franca but the
ways teams actually reach it are heterogeneous: prod is ssh, staging is
docker, local is ddev (or lando, or ahoy, or plain exec). Existing tools
either pick one transport (`drush @alias` over ssh) or pick one workflow
(`ddev import-db`). dconsole lets you write one alias file describing
how each environment is reached, then talk to all of them the same way
— including the things that are messy across environments, like cached
DB pulls and rsync from ssh to ddev.

## Install

### Homebrew (macOS, Linux)

```sh
brew install dconsole/tap/dconsole
```

`brew upgrade dconsole` picks up new releases automatically. macOS gets
a single universal binary that runs natively on both Intel and Apple
Silicon. Linux works on amd64 and arm64 via [Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux).

### Pre-built binary (any OS)

Download for your OS+arch from the
[releases page](https://github.com/dconsole/dconsole/releases),
extract, drop on `$PATH`:

```sh
# macOS (Intel + Apple Silicon — one universal binary)
curl -L https://github.com/dconsole/dconsole/releases/latest/download/dconsole_0.3.2_darwin_all.tar.gz \
  | tar -xz
mv dconsole ~/.local/bin/

# Linux x86_64
curl -L https://github.com/dconsole/dconsole/releases/latest/download/dconsole_0.3.2_linux_amd64.tar.gz \
  | tar -xz
mv dconsole ~/.local/bin/
```

Windows users grab the `windows_amd64.zip` archive from the releases page.

Verify the install:

```sh
dconsole --version
```

### From source (Go 1.24+)

```sh
go install github.com/dconsole/dconsole/cmd/dconsole@latest
```

### In GitHub Actions

The repo doubles as a composite action that installs dconsole on the
runner and puts it on `$PATH` for subsequent steps:

```yaml
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dconsole/dconsole@v0.3.0   # installs the matching release
        # with:
        #   version: latest              # default; or pin a specific tag
      - run: dconsole sql:sync @prod @stage --confirm-cross-site
```

Works on `ubuntu-*`, `macos-*` (Intel and Apple Silicon), and
`windows-*` runners. Pinning to a specific tag (`@v0.3.0`) is
recommended over `@latest` for reproducible workflows.

## Quickstart

1. In your project directory, drop a `<site>.site.yml` with one alias per
   environment:

   ```yaml
   # ./example.site.yml
   _defaults:
     bin: { kind: drush }

   local:
     uri: https://example.ddev.site
     root: /var/www/html
     transport:
       type: ddev
       ddev: { project: example }

   prod:
     uri: https://example.com
     root: /var/www/example
     transport:
       type: ssh
       ssh:
         host: app01.example.com
         user: deploy
   ```

2. (Optional) Register the project so aliases resolve from any cwd:

   ```sh
   dconsole project:register
   ```

3. Run anything:

   ```sh
   dconsole @example.prod status
   dconsole sql:sync @example.prod @example.local
   dconsole rsync @example.prod @example.local --include-private
   dconsole login @example.local
   ```

## Alias files

dconsole looks for `*.site.yml` files in (in priority order):

1. The current directory.
2. A registered project directory matching the cwd (or any registered
   directory the cwd is inside).
3. `~/.dconsole/aliases/`.

Each file is a YAML map keyed by environment name. A reserved
`_defaults` key is merged into every other entry in the same file.

### Inline URI shorthand (v0.5.0+)

For one-off invocations you don't have to write a YAML alias at all
— `@<scheme>://…` works anywhere an `@site.env` reference does:

```sh
# Quick ssh + multisite URI (Drush-style placeholders: www-admin@server.domain.com)
dconsole @ssh://www-admin@server.domain.com/var/www/drupal#http://example.com status

# Local DDEV from any directory
dconsole @ddev://example cr

# Remote docker compose stack via the v0.4.5 host: tunnelling
dconsole @compose://www-admin@server.domain.com/srv/example/docker?service=php cr

# kubectl into a pod
dconsole @kubectl://live/drupal-pod?container=drupal status

# Apple `container` (macOS 15+, native OCI runtime)
dconsole @container://drupal-app?user=www-data cr

# container-compose: address a service by name; container-compose's
# naming convention (<project>-<service>) is computed for you.
dconsole @container-compose://hc?service=php cr
dconsole @container-compose:///Users/me/heydon?service=php&user=www-data cr

# Sync from URI to YAML alias
dconsole sql:sync @ssh://www-admin@server.domain.com/var/www/drupal @example.local
```

Schemes: `ssh`, `ddev`, `compose`, `docker`, `container` (Apple's
macOS-native runtime), `container-compose`, `kubectl` (alias `k8s`),
`lando`, `ahoy`, plus any installed plugin handler that registers a
URI parser. Multi-layer chains stay in YAML.

Use `dconsole alias:dump @<URI>` to see the YAML equivalent — handy
for debugging or copying into `dconsole.yml` as a permanent alias.

### Schema

dconsole accepts two equivalent ways of declaring how to reach an
environment:

- **`handler:`** (v0.4.0+) — singular block, optionally with `via:` for
  a chain (e.g. ssh outer + docker inner)
- **`handlers: [...]`** (v0.4.0+) — explicit list form of a chain
- **`transport:`** + **`provider:`** (legacy, pre-v0.4.0) — still
  accepted; dconsole auto-migrates `transport + provider` into a
  2-layer chain

```yaml
_defaults:
  bin:
    kind: drush         # drush | drupal | auto
    path: /usr/local/bin/drush  # optional override
  uri: https://example.com
  env_vars:
    DRUSH_OPTIONS_URI: https://example.com  # forwarded into every exec

dev:
  uri: https://dev.example.com
  root: /var/www/dev
  handler: { type: ddev, ddev: { project: example } }

# Chain example — ssh into a prod server, then docker-exec into the
# Drupal container. The outer layer wraps the inner.
prod:
  uri: https://prod.example.com
  root: /var/www/html
  handler:
    type: ssh
    ssh: { host: prod.example.com, user: deploy }
    via:
      type: docker
      docker: { container: drupal-app }

  # Optional: per-env policy. "protected" rejects sync with envs
  # not listed in allow_sync_from / allow_sync_to. --force overrides.
  policy:
    sync_policy: open                  # open | protected
    allow_sync_from: [stage, prod]
    allow_sync_to:   [local]

  # Optional: how sql:sync produces a dump from this env.
  sql:
    cache: { ttl: 6h }                  # default 24h
    source:
      type: drush                       # drush | file | docker_cp
      database: default                 # drush DB key
      structure_tables: [cache_*, sessions, watchdog]
      # structure_tables_key: common    # alternative — name from drush config
      # path: /var/backups/latest.sql.gz   # file
      # container: db-dumper              # docker_cp
      # gzipped: true
    target:
      type: drush
      database: default                 # for cross-DB sync (e.g. d7→d11 migrate)

  # Optional: rsync tunables.
  assets:
    cache: { ttl: 24h }   # provider-supplied bundles only

prod:
  uri: https://example.com
  root: /var/www/prod
  transport:
    type: ssh
    ssh:
      host: app01.example.com
      user: deploy
      identity_file: ~/.ssh/id_ed25519
      options: ["-o", "ServerAliveInterval=30"]
  policy: { sync_policy: protected }
```

### Transports

The shipped transports cover the typical Drupal hosting surfaces. Each
gets its own typed config block (in addition to `type:`).

| Type      | Required CLI on `$PATH` | Config keys |
|-----------|------------------------|-------------|
| `exec`    | — (runs locally)       | `dir` |
| `ssh`     | `ssh`                  | `host`, `user`, `port`, `identity_file`, `options` |
| `ddev`    | `ddev`                 | `project`, `service` (auto-detected from `.ddev/config.yaml` walking up from `root`) |
| `lando`   | `lando`                | `app_dir`, `service` |
| `ahoy`    | `ahoy`                 | `dir`, `task` (defaults to the bin basename) |
| `docker`  | `docker`               | `container`, `user`, `exec_options`, `host`, `context` |
| `container` *(experimental)* | `container` (Apple)  | `container`, `user`, `exec_options` (macOS 15+; Apple's containerization stack is still pre-1.0 and its CLI shape is moving) |
| `container-compose` *(experimental)* | `container` (Apple) | `project_name` or `project_dir`, `service`, `user`, `exec_options` (macOS 15+; for projects managed by [container-compose](https://github.com/mcrich23/container-compose) — dconsole derives the container name as `<project>-<service>` and shells to `container exec`, since container-compose has no `exec` subcommand) |
| `compose` | `docker compose`       | `project_dir`, `service`, `exec_options`, `host`, `context` |
| `kubectl` | `kubectl`              | `namespace`, `resource`, `container`, `kubeconfig` |
| `<plugin>`| via `dconsole plugin install` | whatever the plugin declares |

`dconsole transport:list` prints the registered set, including any
installed subprocess plugins. List aliases with `dconsole site:alias`
(or the shorter `dconsole sa`).

### Override files

Alongside any `*.site.yml`, dconsole loads a sibling `*.override.yml`
and a project-wide `dconsole.override.yml` if present. Override layers
deep-merge into the base — useful for keeping local-laptop tweaks
(custom `identity_file`, alternate ports, container names) out of the
shared yml.

## Commands

```
dconsole @alias <any drush command>     # forward to drush over the alias's transport

# Cross-env workflows
dconsole sql:sync   @src @dst [opts]    # cached, transport-aware DB sync
dconsole rsync      @src @dst [opts]    # files (sites/default/files, %private)
dconsole login      @alias              # drush uli + open URL in browser
dconsole auth       @alias              # open an interactive shell on the transport
dconsole sh|ssh     @alias              # alias for auth

# Cache management
dconsole sql:cache    list | clear [@alias]
dconsole assets:cache list | clear [@alias]

# Aliases & projects
dconsole site:alias | sa                # list all known aliases
dconsole alias:convert <file>           # convert a drush 8 PHP alias file
dconsole project:init                   # bootstrap dconsole.yml in cwd
dconsole project:register               # remember this dir for alias auto-resolution
dconsole project:list                   # registered projects
dconsole project:forget [path]          # unregister

# Plugins (out-of-tree transports + providers)
dconsole plugin list
dconsole plugin search <query>
dconsole plugin info    <name>
dconsole plugin install <name>[@<version>]
dconsole plugin update  [<name>]
dconsole plugin remove  <name>

# Debugging
dconsole inspect @alias <cmd>           # show resolved argv without executing
dconsole transport:list                 # registered transports (with required CLIs)
dconsole dconsole:bin                   # absolute path to this binary
dconsole --help                         # top-level help
dconsole @alias --help                  # merged drush + dconsole help
dconsole --version
```

### Verbose logging

`-v`, `-vv`, `-vvv` print increasingly detailed traces of what dconsole
is doing — each level is also forwarded to drush as `-v` / `-vv` /
`-vvv`. `-vvv` prints every command being executed across every
transport.

```sh
dconsole -v   sql:sync @prod @local        # high-level steps + chosen strategy
dconsole -vv  sql:sync @prod @local        # + per-command stderr
dconsole -vvv rsync    @prod @local        # + every argv
```

## `sql:sync` — database sync

```sh
dconsole sql:sync @source.env @target.env [options]
```

Dumps the source database and imports it into the target. Both ends
are reached through their transports — dconsole sits in the middle and
streams gzipped bytes through.

Dump resolution priority:

1. `provider.SyncTo` on the source (full end-to-end takeover — e.g. a
   plugin that knows how to pull an image-based backup).
2. Local cache for this `(alias, strategy, database, structure-tables)`
   tuple, if fresh under the effective TTL.
3. `provider.DumpFor` on the source (provider supplies the dump file,
   dconsole loads it).
4. `alias.sql.source.type`:
   - `drush` (default) — `drush sql:dump --gzip` via the transport.
   - `file` — `cat` a pre-made dump file via the transport.
   - `docker_cp` — `docker cp <container>:<path>` (bypasses transport).

Load resolution priority:

1. `provider.LoadFor` on the target.
2. Target transport's `DBImporter` (ddev → `ddev import-db --database=…`).
3. `drush sql:cli --database=<target>` via the target's transport.

Notable options:

```
--target-database=KEY      # load INTO a different drush DB key (d7 default → d11 migrate)
--source-database=KEY      # dump FROM a non-default key
--structure-tables=LIST    # cache_*, sessions, watchdog → schema-only
--structure-tables-key=KEY # reference a named drush array
--cache-ttl=DUR            # override per-alias TTL for this run (e.g. 6h)
--refresh                  # invalidate this entry, re-fetch
--no-cache                 # don't read, don't write
--keep-dump                # leave the temp dump file in place after import
--dump-path=PATH           # write the dump to PATH (disables cache for the run)
--confirm-cross-site       # CI-friendly cross-site confirmation (NOT --yes)
--force                    # bypass per-env sync_policy
```

The cache lives at `$XDG_CACHE_HOME/dconsole/sql/<sha256>.sql.gz`.
Compression happens AT THE SOURCE so the wire only carries gzipped
bytes. Manage with `dconsole sql:cache list | clear [@alias]`.

**Cross-site safety**: when `source.Site != target.Site` (e.g.
`@projectA.prod → @projectB.local`), dconsole prompts you to type the
target site name before proceeding. The prompt is deliberately NOT
aliased to `--yes/-y` — habitual `--force` use can't bypass it. Use
`--confirm-cross-site` in CI.

## `rsync` — file sync

```sh
dconsole rsync @source @target [options]
```

Two branches:

- **Orchestrator** — both endpoints are alias-bound and the path is one
  of `%files`, `%private`, or empty (defaults to `%files`). dconsole
  picks the fastest viable strategy. Cross-site safety, provider hooks,
  and caching all apply.
- **Legacy tar-stream** — anything else (freeform paths, local
  endpoints). The existing behaviour: tar the source, untar at the
  destination, via `transport.Pipe`. Untouched for backward compat.

### Modes (orchestrator)

```
--mode=auto              # default — priority chain, first match wins
--mode=rsync             # force a real rsync invocation; fail if impossible
--mode=diff              # skip rsync; use universal diff+tar
--mode=stage-file-proxy  # configure the Stage File Proxy module on target
--proxy                  # shortcut for --mode=stage-file-proxy
```

**`auto` (default)** walks four strategies top to bottom; first that
applies wins, failures cascade:

1. **Same-host rsync** — both endpoints share an ssh endpoint. dconsole
   ssh's in once and runs a local-to-local rsync on the remote.
2. **Local-mediated rsync** — source is ssh-reachable from this
   machine; pull to a staging dir, then push to the target via its
   transport (ddev import-files / rsync push / tar-stream).
3. **Source-driven rsync** — source ssh's INTO the target with
   agent-forwarded creds.
4. **Diff+tar fallback** — universal: `find` on both ends, compute
   changed/deleted, tar-pipe only the changed files through
   `transport.Pipe`. Works across heterogeneous transports (ssh →
   ddev, docker → exec, …).

The chosen strategy is logged at `-v`.

**`stage-file-proxy`** — the escape hatch for cases where syncing
isn't viable (asset tree is huge, source is read-only, no rsync
available). Enables and configures the
[Stage File Proxy](https://www.drupal.org/project/stage_file_proxy)
module on the target so missing file requests lazy-fetch from the
source's `uri`. Two drush commands run on the target with a drush 8
fallback (`pm:enable` → `pm-enable`, `config:set` → `vset`).

### Path tokens

```
--pathspec=%files,%private   # comma-separated; runs the chosen mode per pathspec
--include-private            # shortcut for --pathspec=%files,%private
--delete                     # remove files on target absent from source
```

Tokens resolve via `drush status --format=json`; absolute paths fall
through unchanged.

### Examples

```sh
dconsole rsync @prod @local                            # auto, %files only
dconsole rsync @prod @local --include-private
dconsole rsync @prod @local --mode=rsync               # force rsync, fail loudly
dconsole rsync @prod @local --mode=diff                # universal diff+tar
dconsole rsync @prod @local --mode=stage-file-proxy    # configure proxy, no transfer
dconsole rsync @prod @local --delete --confirm-cross-site
dconsole rsync @prod:%files ./local-backup             # legacy freeform
```

## `login` and `auth`

```sh
dconsole login @alias         # drush uli + open URL in browser
dconsole auth  @alias         # interactive shell on the transport (ssh / ddev exec / …)
dconsole sh    @alias         # alias for auth
```

`login` runs `drush user:login --uri=<alias.uri>` and pipes the
returned URL into `open` (macOS), `xdg-open` (Linux), or `start`
(Windows).

## Plugins

dconsole's transport and provider layers are extensible via subprocess
plugins — separate binaries named `dconsole-<name>` that speak a small
verb protocol on stdin/stdout. The reference implementation is
[`examples/dconsole-echo`](examples/dconsole-echo).

```sh
dconsole plugin search skpr
dconsole plugin install skpr
dconsole plugin info    skpr
dconsole plugin update
dconsole plugin remove  skpr
```

Plugins are discovered in `~/.dconsole/plugins/`, then on `$PATH`.
The index is a public YAML-per-plugin GitHub repo (brew-style); see
[docs/plugins/distribution.md](docs/plugins/distribution.md) for
publishing your own.

Authoring guide: [docs/plugins/authoring.md](docs/plugins/authoring.md).

### Provider plugins

Providers describe hosting platforms with their own backup / sync
semantics. Each method is opt-in via `ErrNotSupported` fallthrough, so
a provider only implements what it can do; dconsole drops to the
transport path for everything else.

| Method            | When dconsole calls it |
|-------------------|------------------------|
| `SyncTo`          | sql:sync — end-to-end DB takeover (e.g. image-pull) |
| `DumpFor`         | sql:sync — provider supplies the dump; dconsole loads it |
| `LoadFor`         | sql:sync — provider takes the dump and loads it into the target |
| `SyncFilesTo`     | rsync — end-to-end file takeover |
| `FilesDownload`   | rsync — provider supplies an asset bundle; dconsole loads it |
| `LoadFilesFor`    | rsync — provider loads a bundle into the target |
| `Login`           | login — provider produces the one-time login URL |

## Project bookkeeping

```sh
dconsole project:init       # write a dconsole.yml in cwd (project metadata)
dconsole project:register   # remember this dir so @site.env resolves from anywhere inside it
dconsole project:list
dconsole project:forget     # unregister cwd (or pass a path)
```

`dconsole.yml` is project metadata (plugin pins, default alias hints).
`dconsole.override.yml` is the gitignored sibling for per-laptop tweaks.

## Caches

| Cache                   | Path                                          | Populated by |
|-------------------------|-----------------------------------------------|--------------|
| SQL dumps               | `$XDG_CACHE_HOME/dconsole/sql/<key>.sql.gz`    | `sql:sync` |
| Provider asset bundles  | `$XDG_CACHE_HOME/dconsole/assets/<key>.tar.gz` | `rsync` (provider path only) |
| Resolved sitepaths      | `~/.cache/dconsole/sitepath-<key>.json`        | `sql:sync` / `rsync` (memoised `drush status`) |

Atomic writes via `.tmp.<rand>` + `os.Rename`. SHA256 keys derived from
`(site, env, strategy, …)` so distinct configurations cache
independently.

## Building from source

```sh
git clone https://github.com/dconsole/dconsole.git
cd dconsole
go build -o dconsole ./cmd/dconsole

# Run tests
go test ./...

# Cross-compile (same matrix the release uses)
goreleaser release --snapshot --clean
```

Go 1.24+ required (uses `testing.Chdir`).

## License

dconsole uses a split license to keep the core copyleft while letting
third-party plugins ship under any terms — including closed-source
commercial:

| Path | License | Why |
|------|---------|-----|
| `cmd/`, `internal/` | [GPL-2.0-only](LICENSE) | The CLI itself. Anyone modifying or redistributing dconsole stays under copyleft. |
| `pkg/plugin`, `pkg/provider`, `pkg/transport` | [Apache-2.0](pkg/LICENSE) | The plugin SDK. Plugin authors import these and need a permissive license so they aren't pulled into the GPL. |

This is the same split [Linux uses for its UAPI headers](https://lkml.org/lkml/2003/12/9/108) and Terraform uses for its [plugin SDK](https://github.com/hashicorp/terraform-plugin-sdk/blob/main/LICENSE): the runtime API surface is permissive, the implementation is copyleft.

**For plugin authors**: imports under `github.com/dconsole/dconsole/pkg/*` are Apache-2.0. You can publish your plugin under any license — MIT, BSD, Apache, GPL, proprietary, commercial. Plugins also run as separate subprocess binaries communicating over stdin/stdout, which under [the FSF's interpretation](https://www.gnu.org/licenses/gpl-faq.html#MereAggregation) is not derivative-work territory even before the SDK carve-out.

© Heydon Consulting.
