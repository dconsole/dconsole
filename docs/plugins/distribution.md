# Distributing a dconsole plugin

dconsole has a first-party plugin manager (`dconsole plugin install`).
Once your plugin binary is listed in the curated index, users on every
OS install it the same way — no Homebrew tap, no scoop bucket, no
install script required.

This document covers what you build, where you upload it, and how to
get listed.

---

## 1. Cross-platform binaries

dconsole supports macOS, Linux, and Windows on amd64 and arm64. Ship
all five:

| Platform key       | GOOS    | GOARCH | Tarball name (convention)                                  |
|--------------------|---------|--------|------------------------------------------------------------|
| `darwin-amd64`     | darwin  | amd64  | `dconsole-foo-<version>-darwin-amd64.tar.gz`               |
| `darwin-arm64`     | darwin  | arm64  | `dconsole-foo-<version>-darwin-arm64.tar.gz`               |
| `linux-amd64`      | linux   | amd64  | `dconsole-foo-<version>-linux-amd64.tar.gz`                |
| `linux-arm64`      | linux   | arm64  | `dconsole-foo-<version>-linux-arm64.tar.gz`                |
| `windows-amd64`    | windows | amd64  | `dconsole-foo-<version>-windows-amd64.tar.gz`              |

Inside each tarball: one executable named `dconsole-<type>` (no
subdirectories needed; the extractor handles flat layouts). Build with:

```sh
GOOS=darwin GOARCH=arm64 go build -o dconsole-foo-1.0.0-darwin-arm64/dconsole-foo .
tar -czf dconsole-foo-1.0.0-darwin-arm64.tar.gz -C dconsole-foo-1.0.0-darwin-arm64 .
```

A GitHub Actions release workflow that does all five in one job is the
intended setup; the template repo `dconsole-plugin-template` (to come)
will ship one.

## 2. Publish a GitHub release

Tag the release in your plugin's repo. Attach all five tarballs as
release assets. dconsole consumes them directly via
`https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>`,
so this URL pattern is what you'll reference in the index.

Capture each tarball's sha256 — every modern shasum tool emits it:

```sh
sha256sum dconsole-foo-1.0.0-*.tar.gz
```

## 3. Add yourself to the curated index

Open a PR against
[`dconsole/dconsole-plugin-index`](https://github.com/dconsole/dconsole-plugin-index)
(separate repo, public). Add one file:

```
plugins/foo.yml
```

with this content:

```yaml
name: foo
description: One-line summary that appears in `dconsole plugin search`
homepage: https://github.com/your-org/dconsole-foo
license: MIT
transports: [foo]      # the types declared in plugin-info
providers: [foo]       # omit if your plugin doesn't ship a provider
versions:
  - version: 1.0.0
    requires_dconsole: ">=0.5.0"     # optional; matches dconsole's --version
    platforms:
      darwin-arm64:
        url:    https://github.com/your-org/dconsole-foo/releases/download/v1.0.0/dconsole-foo-1.0.0-darwin-arm64.tar.gz
        sha256: <hex sha256 of the tarball>
      darwin-amd64:  { url: …, sha256: … }
      linux-arm64:   { url: …, sha256: … }
      linux-amd64:   { url: …, sha256: … }
      windows-amd64: { url: …, sha256: … }
```

The index maintainer reviews:

- The release exists and is downloadable.
- The sha256s match the artifacts.
- The transport / provider types don't collide with built-ins or other
  plugins.
- The plugin's `plugin-info` output is consistent with the YAML.

Once merged, a GitHub Action regenerates `_generated/index.json` (the
summary file used by `dconsole plugin search`) on every push, and your
plugin is live.

## 4. Releasing a new version

Append a new `versions:` block to your `plugins/foo.yml` — don't
modify existing versions. dconsole picks the **last** entry by default,
but `dconsole plugin install foo --version=1.0.0` still works for
users who pinned an older release.

```yaml
versions:
  - version: 1.0.0
    # ... existing entry, unchanged
  - version: 1.1.0
    requires_dconsole: ">=0.5.0"
    platforms:
      darwin-arm64: { url: …, sha256: … }
      # …
```

## 5. Private and unlisted plugins

Two paths bypass the curated index entirely:

```sh
# Direct URL (with mandatory --sha256)
dconsole plugin install \
  --url=https://internal.example.com/dconsole-secret-1.0.0-linux-amd64.tar.gz \
  --sha256=3a7b…

# Local tarball
dconsole plugin install --path=./dconsole-secret.tgz
```

Both still extract into `~/.dconsole/plugins/` and run `plugin-info`
to verify the binary works. No curation, no listing, no PR.

This is the right path for:

- Plugins shipping inside a corporate network.
- Plugins distributed via your own infrastructure (a private S3
  bucket, an internal artifact repo).
- Plugins in active development before you submit to the index.

## 6. Updates

For now, re-running `dconsole plugin install <name>` fetches whatever
the index currently lists and overwrites the installed binary. A
proper `dconsole plugin update` (with version comparison + diff) is
planned but not yet built.

## 7. Optional value-adds: Homebrew, scoop, etc.

You don't need any of these — the curated index covers every OS. But
some users prefer to manage all CLIs through one package manager. If
you publish a Homebrew formula or scoop manifest that drops your
binary onto `$PATH`, dconsole's discovery layer picks it up
automatically (PATH is the second-priority search location after
`~/.dconsole/plugins/`).

Skip them in v1 unless a real user asks.

## See also

- [Authoring guide](./authoring.md) — the protocol your binary must
  implement.
- [Echo reference plugin](../../examples/dconsole-echo) — minimal
  working example.
- [dconsole-plugin-index](https://github.com/dconsole/dconsole-plugin-index)
  — the index repo (separate; not created yet at time of writing).
