// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"io"

	"github.com/dconsole/dconsole/internal/alias"
)

// Handler is the mandatory interface every alias plugin implements.
//
//   - Name returns the type as it appears in alias YAML (e.g. "ssh", "ddev", "skpr").
//   - Available reports whether the runtime prerequisites are met
//     (binary on PATH, container reachable, …).
//   - Wrap takes an inner argv (what we want drush to run) and returns
//     the LOCAL argv to spawn. For single-handler aliases this is
//     called once with the drush argv. For chains, Wrap is called
//     from inside out — each outer layer wraps the previous output.
//     The exec handler is the identity transform; ssh shell-quotes
//     the inner; docker/kubectl/ddev prepend their exec preamble.
//   - Exec runs a single forwarded command, wiring stdio.
//   - Pipe runs a command with explicit stdin/stdout — used for sql:dump
//     streaming, file fetches, etc.
//   - Shell drops the user into an interactive shell in the remote env.
//   - Preview returns the LOCAL argv Exec would spawn — for `inspect`
//     output. In simple implementations Preview is just `Wrap(cmd)`.
type Handler interface {
	Name() string
	Available() error
	Wrap(inner []string) []string
	Exec(ctx context.Context, cmd []string, stdio Stdio) error
	Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error
	Shell(ctx context.Context, workDir string) error
	Preview(remoteCmd []string) []string
}

// === Optional capabilities ============================================
//
// Handlers implement these via type assertion — same pattern as the
// older transport.DBImporter. The orchestrator checks for the cap and
// uses it when present; otherwise it falls back to the generic
// pipe-to-drush path.

// DBImporter is a faster path for loading a SQL dump than feeding it
// to `drush sql:cli` via Pipe. ddev's `ddev import-db` is the
// canonical example.
//
// Note: the *alias.Alias parameter mirrors today's transport.DBImporter
// signature so existing in-tree implementations (e.g. ddev) satisfy
// both interfaces during the v0.4.0 transition. After pkg/transport
// is removed, the alias may be folded into the handler's construction
// and dropped from the method signature.
type DBImporter interface {
	ImportDB(ctx context.Context, a *alias.Alias, dumpPath string) error
}

// FilesImporter is the asset-bundle sibling of DBImporter. ddev's
// `ddev import-files` is the canonical example.
type FilesImporter interface {
	ImportFiles(ctx context.Context, a *alias.Alias, bundlePath string) error
}

// RsyncSSH lets the rsync orchestrator build `rsync -e "ssh <opts>"`
// argv against an ssh-reachable handler.
type RsyncSSH interface {
	RsyncRemote() (remote string, sshOpts []string)
}

// LoginCapable lets `dconsole login` get a one-time admin URL from a
// platform that supplies its own (Skpr, Pantheon, …) instead of
// running `drush uli`.
type LoginCapable interface {
	Login(ctx context.Context, a *alias.Alias) error
}

// DBSyncer takes over the entire `sql:sync source target` chain. Used
// by platforms whose preferred sync model isn't a SQL dump — e.g. Skpr
// publishes a nightly image you pull + rebuild locally.
type DBSyncer interface {
	SyncTo(ctx context.Context, source, target *alias.Alias) error
}

// DBDumper supplies a dump for the alias — used when the platform
// already has a fresh backup and bypassing drush sql:dump is faster.
// The returned cleanup is called when the dump is no longer needed.
type DBDumper interface {
	DumpFor(ctx context.Context, a *alias.Alias) (dumpPath string, cleanup func(), err error)
}

// DBLoader consumes a dump and loads it into the handler's alias.
type DBLoader interface {
	LoadFor(ctx context.Context, a *alias.Alias, dumpPath string) error
}

// FilesSyncer is the rsync-equivalent of DBSyncer.
type FilesSyncer interface {
	SyncFilesTo(ctx context.Context, source, target *alias.Alias) error
}

// FilesDownloader pulls the alias's files dir into targetDir.
type FilesDownloader interface {
	FilesDownload(ctx context.Context, a *alias.Alias, targetDir string) error
}

// FilesLoader consumes an asset bundle and loads it into the alias.
type FilesLoader interface {
	LoadFilesFor(ctx context.Context, a *alias.Alias, bundlePath string) error
}
