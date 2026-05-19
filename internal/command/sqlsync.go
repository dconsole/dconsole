package command

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/provider"
	"github.com/heydon/dconsole/internal/remotebin"
	"github.com/heydon/dconsole/internal/transport"
)

// SqlSyncOpts mirrors the options of `drush sql:sync` that dconsole handles.
type SqlSyncOpts struct {
	KeepDump bool   // keep the temporary dump file after import
	DumpPath string // explicit path for the temp file (empty = OS temp)
	Force    bool   // bypass per-env sync_policy / allow_sync_* checks
}

// SqlSync dumps the source database, streams the dump to a local file,
// then imports it into the target database. Each end uses its own
// transport — no rsync-over-ssh assumption.
//
// If the source alias has a provider that implements DumpFor, the dump is
// fetched via that provider instead of via transport+drush. Same for
// LoadFor on the target.
//
// Per-env sync policies (alias.Policy) are enforced before the dump
// starts. Use --force to override.
func SqlSync(ctx context.Context, source, target *alias.Alias, out io.Writer, opts SqlSyncOpts) error {
	if err := alias.CheckSync(source, target, opts.Force); err != nil {
		return err
	}
	dumpPath, cleanup, err := obtainDump(ctx, source, out, opts)
	if err != nil {
		return err
	}
	defer cleanup()

	return loadDump(ctx, target, dumpPath, out)
}

// obtainDump returns a local path to a gzipped SQL dump of `source`. In
// priority order:
//   1. provider.DumpFor (when a provider is configured and supports it)
//   2. alias.sql.source.type (drush | file | docker_cp)
//   3. default "drush" — `<bin> sql:dump --gzip` via the alias's transport
func obtainDump(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts) (string, func(), error) {
	if p, _ := provider.For(source); p != nil {
		fmt.Fprintf(out, "fetching dump for @%s.%s via provider %s\n", source.Site, source.Env, p.Name())
		path, cleanup, err := p.DumpFor(ctx, source)
		if err == nil {
			return path, cleanup, nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return "", nil, fmt.Errorf("%s.DumpFor: %w", p.Name(), err)
		}
		fmt.Fprintf(out, "provider %s does not support DumpFor; falling back to alias.sql.source\n", p.Name())
	}

	src := source.SQL.Source
	switch src.Type {
	case "", "drush":
		return obtainDumpDrush(ctx, source, out, opts)
	case "file":
		return obtainDumpFile(ctx, source, out, opts, src)
	case "docker_cp":
		return obtainDumpDockerCp(ctx, source, out, opts, src)
	default:
		return "", nil, fmt.Errorf("@%s.%s: unknown sql.source.type %q (want drush, file, or docker_cp)", source.Site, source.Env, src.Type)
	}
}

func obtainDumpDrush(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts) (string, func(), error) {
	sourceT, err := transport.For(source)
	if err != nil {
		return "", nil, fmt.Errorf("source @%s.%s: %w", source.Site, source.Env, err)
	}
	if err := sourceT.Available(); err != nil {
		return "", nil, err
	}
	sourceBin, err := resolveBin(ctx, source, sourceT)
	if err != nil {
		return "", nil, fmt.Errorf("source bin: %w", err)
	}
	dumpPath, cleanup, err := openDump(opts)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(out, "dumping @%s.%s via %s drush → %s\n", source.Site, source.Env, sourceT.Name(), dumpPath)
	if err := dumpToFile(ctx, sourceT, sourceBin, dumpPath); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("dump: %w", err)
	}
	return dumpPath, cleanup, nil
}

// obtainDumpFile pulls a pre-made dump file via the alias's transport
// (e.g. `cat /backups/latest.sql.gz` over ssh/docker exec/etc.). If the
// remote file is not gzipped, dconsole gzips it locally so downstream
// consumers always see a .sql.gz.
func obtainDumpFile(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts, src alias.SQLSource) (string, func(), error) {
	if src.Path == "" {
		return "", nil, fmt.Errorf("@%s.%s: sql.source.type=file requires sql.source.path", source.Site, source.Env)
	}
	sourceT, err := transport.For(source)
	if err != nil {
		return "", nil, fmt.Errorf("source @%s.%s: %w", source.Site, source.Env, err)
	}
	if err := sourceT.Available(); err != nil {
		return "", nil, err
	}
	dumpPath, cleanup, err := openDump(opts)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(out, "fetching dump file @%s.%s:%s via %s → %s\n", source.Site, source.Env, src.Path, sourceT.Name(), dumpPath)

	f, err := os.Create(dumpPath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer f.Close()

	var sink io.Writer = f
	var closeGz func() error
	if !src.SourceGzipped() {
		gw := gzip.NewWriter(f)
		sink = gw
		closeGz = gw.Close
	}
	if err := sourceT.Pipe(ctx, []string{"cat", src.Path}, nil, sink); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("cat %s via %s: %w", src.Path, sourceT.Name(), err)
	}
	if closeGz != nil {
		if err := closeGz(); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dumpPath, cleanup, nil
}

// obtainDumpDockerCp shells out to `docker cp <container>:<path> <local>`
// directly, bypassing the alias's transport. Right primitive when a
// sidecar container ships a fresh backup and you don't want drush inside
// it (the user's old client's setup).
func obtainDumpDockerCp(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts, src alias.SQLSource) (string, func(), error) {
	if src.Container == "" || src.Path == "" {
		return "", nil, fmt.Errorf("@%s.%s: sql.source.type=docker_cp requires container and path", source.Site, source.Env)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "", nil, fmt.Errorf("sql.source.type=docker_cp needs docker on PATH: %w", err)
	}
	dumpPath, cleanup, err := openDump(opts)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(out, "copying %s:%s → %s via docker cp\n", src.Container, src.Path, dumpPath)

	landAt := dumpPath
	if !src.SourceGzipped() {
		landAt = dumpPath + ".raw"
	}
	cmd := exec.CommandContext(ctx, "docker", "cp", src.Container+":"+src.Path, landAt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker cp: %w\n%s", err, stderr.String())
	}
	if landAt != dumpPath {
		if err := gzipFile(landAt, dumpPath); err != nil {
			cleanup()
			os.Remove(landAt)
			return "", nil, err
		}
		os.Remove(landAt)
	}
	return dumpPath, cleanup, nil
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	gw := gzip.NewWriter(out)
	if _, err := io.Copy(gw, in); err != nil {
		return err
	}
	return gw.Close()
}

// loadDump either calls the target provider's LoadFor, or runs
// `<target.bin> sql:cli` with the dump on stdin via the target transport.
func loadDump(ctx context.Context, target *alias.Alias, dumpPath string, out io.Writer) error {
	if p, _ := provider.For(target); p != nil {
		err := p.LoadFor(ctx, target, dumpPath)
		if err == nil {
			fmt.Fprintf(out, "imported into @%s.%s via provider %s\n", target.Site, target.Env, p.Name())
			return nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return fmt.Errorf("%s.LoadFor: %w", p.Name(), err)
		}
		fmt.Fprintf(out, "provider %s does not support LoadFor; using transport import\n", p.Name())
	}

	targetT, err := transport.For(target)
	if err != nil {
		return fmt.Errorf("target @%s.%s: %w", target.Site, target.Env, err)
	}
	if err := targetT.Available(); err != nil {
		return err
	}
	targetBin, err := resolveBin(ctx, target, targetT)
	if err != nil {
		return fmt.Errorf("target bin: %w", err)
	}
	fmt.Fprintf(out, "importing into @%s.%s via %s\n", target.Site, target.Env, targetT.Name())
	if err := importFromFile(ctx, targetT, targetBin, dumpPath); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	fmt.Fprintln(out, "sql:sync complete")
	return nil
}

func openDump(opts SqlSyncOpts) (path string, cleanup func(), err error) {
	if opts.DumpPath != "" {
		return opts.DumpPath, func() {
			if !opts.KeepDump {
				_ = os.Remove(opts.DumpPath)
			}
		}, nil
	}
	f, err := os.CreateTemp("", "dconsole-dump-*.sql.gz")
	if err != nil {
		return "", nil, err
	}
	f.Close()
	return f.Name(), func() {
		if !opts.KeepDump {
			_ = os.Remove(f.Name())
		}
	}, nil
}

func dumpToFile(ctx context.Context, t transport.Transport, bin *remotebin.Resolved, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := bin.Argv([]string{"sql:dump", "--gzip"})
	return t.Pipe(ctx, cmd, nil, f)
}

func importFromFile(ctx context.Context, t transport.Transport, bin *remotebin.Resolved, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("dump is not gzip: %w", err)
	}
	defer gz.Close()
	cmd := bin.Argv([]string{"sql:cli"})
	return t.Pipe(ctx, cmd, gz, io.Discard)
}
