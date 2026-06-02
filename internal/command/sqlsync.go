package command

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/dbcache"
	"github.com/dconsole/dconsole/internal/dlog"
	"github.com/dconsole/dconsole/internal/provider"
	"github.com/dconsole/dconsole/internal/remotebin"
	"github.com/dconsole/dconsole/pkg/handler"
)

// SqlSyncOpts mirrors the options of `drush sql:sync` that dconsole handles.
type SqlSyncOpts struct {
	KeepDump bool   // keep the temporary dump file after import
	DumpPath string // explicit path for the temp file (empty = OS temp); disables caching
	Force    bool   // bypass per-env sync_policy / allow_sync_* checks

	// Cache controls
	Refresh  bool          // invalidate cache and re-dump; also writes the fresh dump into the cache
	NoCache  bool          // bypass the cache entirely for this run (neither reads nor writes it)
	CacheTTL time.Duration // overrides alias.sql.cache.ttl and the dbcache default (0 = use config / default)

	// Drush-strategy passthroughs (drush sql:dump / sql:cli)
	SourceDatabase     string   // --database= for the source dump (drush DB connection key); default "default"
	TargetDatabase     string   // --database= for the target import; default "default"
	StructureTables    []string // --structure-tables-list= (overrides alias.sql.source.structure_tables)
	StructureTablesKey string   // --structure-tables-key= (overrides alias.sql.source.structure_tables_key)

	// ConfirmCrossSite bypasses the interactive prompt when
	// source.Site != target.Site. Intentionally NOT bound to Force or
	// any drush --yes-style flag.
	ConfirmCrossSite bool
}

// SqlSync dumps the source database, streams the dump to a local file
// (caching it for subsequent runs), then imports it into the target
// database. Each end uses its own transport — no rsync-over-ssh
// assumption.
//
// Priority chain:
//  0. Cross-site safety check (when source.Site != target.Site).
//  1. provider.SyncTo on the source (full end-to-end takeover, e.g. Skpr image pull).
//  2. Otherwise: obtainDump (cache → provider.DumpFor → strategy chain) + loadDump
//     (provider.LoadFor → transport.DBImporter → drush sql:cli fallback).
//
// `in` is the reader used to interactively confirm cross-site syncs.
// Pass nil for non-interactive contexts (CI, tests); the cross-site
// gate then requires opts.ConfirmCrossSite.
func SqlSync(ctx context.Context, source, target *alias.Alias, out io.Writer, in io.Reader, opts SqlSyncOpts) error {
	// 0. Cross-site safety. Runs BEFORE policy check so habitual --force
	// use can't bypass it.
	if source.Site != target.Site {
		if err := confirmCrossSite(source, target, opts.ConfirmCrossSite, in, out); err != nil {
			return err
		}
	}

	if err := alias.CheckSync(source, target, opts.Force); err != nil {
		return err
	}

	// 1. Handler end-to-end takeover (was provider.SyncTo). Tried first
	// so platforms that don't ship SQL dumps (Skpr-style image pull)
	// can short-circuit.
	sourceH, err := handler.For(source)
	if err != nil {
		return fmt.Errorf("source @%s.%s: %w", source.Site, source.Env, err)
	}
	if syncer, ok := handler.Inner(sourceH).(handler.DBSyncer); ok {
		err := syncer.SyncTo(ctx, source, target)
		if err == nil {
			fmt.Fprintf(out, "synced via %s (end-to-end)\n", sourceH.Name())
			return nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return fmt.Errorf("%s.SyncTo: %w", sourceH.Name(), err)
		}
		// fall through to dump + load
	}

	// 2. Dump + load.
	dumpPath, cleanup, err := obtainDump(ctx, source, out, opts)
	if err != nil {
		return err
	}
	defer cleanup()
	return loadDump(ctx, target, dumpPath, out, opts)
}

// confirmCrossSite warns about a cross-site sync and either prompts
// interactively (when `in` is a usable reader) or requires the
// confirm flag. Returns nil if the user approved; an error (without
// transferring any bytes) if not. Shared between sql:sync and rsync.
func confirmCrossSite(source, target *alias.Alias, confirmed bool, in io.Reader, out io.Writer) error {
	fmt.Fprintf(out, "─── CROSS-SITE sql:sync ──────────────────────────────\n")
	fmt.Fprintf(out, "  source: @%s.%s (%s)\n", source.Site, source.Env, source.URI)
	fmt.Fprintf(out, "  target: @%s.%s (%s)\n", target.Site, target.Env, target.URI)
	fmt.Fprintf(out, "\nThis sync writes data from site %q INTO site %q. Confirm by typing the\n", source.Site, target.Site)
	fmt.Fprintf(out, "target site's name (%q), or rerun with --confirm-cross-site for scripted use.\n", target.Site)

	if confirmed {
		fmt.Fprintln(out, "  → --confirm-cross-site supplied; proceeding")
		return nil
	}
	if in == nil {
		return fmt.Errorf("cross-site sql:sync (@%s → @%s) requires interactive confirmation or --confirm-cross-site", source.Site, target.Site)
	}

	fmt.Fprintf(out, "\nType target site name to proceed: ")
	br := bufio.NewReader(in)
	line, _ := br.ReadString('\n')
	got := strings.TrimSpace(line)
	if got != target.Site {
		return fmt.Errorf("cross-site confirmation aborted (typed %q, expected %q)", got, target.Site)
	}
	return nil
}

// obtainDump returns a local path to a gzipped SQL dump of `source`,
// using the cache if possible. Priority:
//  1. Cache hit (skipped if --no-cache or --refresh).
//  2. provider.DumpFor.
//  3. alias.sql.source.type (drush | file | docker_cp).
//  4. Default "drush" — `<bin> sql:dump --gzip [--database=…] [--structure-tables-…]` via the alias's transport.
//
// Per-env sync policies (alias.Policy) are enforced before this is
// called by SqlSync.
//
// The returned cleanup is a no-op for cache-hit and cache-store paths
// (the cached file outlives the run); it's the temp-file cleanup
// otherwise.
func obtainDump(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts) (string, func(), error) {
	useCache := !opts.NoCache && opts.DumpPath == ""
	strategy := sourceStrategyTag(source)
	cacheKey := dbcache.KeyFor(dbcache.KeyInputs{
		Site:            source.Site,
		Env:             source.Env,
		Strategy:        strategy,
		SourceDatabase:  effectiveSourceDatabase(source, opts),
		StructureTables: effectiveStructureTables(source, opts),
		StructTablesKey: effectiveStructureTablesKey(source, opts),
	})

	// Refresh invalidates the cache before reading; that way a fresh
	// dump is generated AND becomes the cached value for next time.
	if useCache && opts.Refresh {
		if err := dbcache.Invalidate(cacheKey); err != nil {
			fmt.Fprintf(out, "warning: failed to invalidate cached dump: %v\n", err)
		}
	}

	if useCache && !opts.Refresh {
		ttl, err := effectiveTTL(source, opts)
		if err != nil {
			return "", nil, err
		}
		path, hit, err := dbcache.Get(cacheKey, ttl)
		if err != nil {
			fmt.Fprintf(out, "warning: cache read failed (proceeding without cache): %v\n", err)
		} else if hit {
			fmt.Fprintf(out, "cache hit: %s (TTL %s)\n", path, ttl)
			return path, func() {}, nil
		}
	}

	// Cache miss (or bypass) → run the strategy chain to produce a
	// fresh dump.
	tempPath, tempCleanup, err := obtainDumpUncached(ctx, source, out, opts)
	if err != nil {
		return "", nil, err
	}

	if !useCache {
		return tempPath, tempCleanup, nil
	}

	// Cache the freshly-dumped bytes for subsequent runs. The cache
	// holds a COPY; the temp file is still cleaned up.
	cachedPath, err := dbcache.Put(cacheKey, tempPath, dbcache.Metadata{
		Site:         source.Site,
		Env:          source.Env,
		Strategy:     strategy,
		SourceDB:     effectiveSourceDatabase(source, opts),
		StructTables: effectiveStructureTables(source, opts),
		StructKey:    effectiveStructureTablesKey(source, opts),
	})
	if err != nil {
		// Cache failure is non-fatal: return the temp path so the load
		// still happens; just warn.
		fmt.Fprintf(out, "warning: cache write failed (using temp dump): %v\n", err)
		return tempPath, tempCleanup, nil
	}
	tempCleanup() // remove the temp; we'll use the cache path
	fmt.Fprintf(out, "cached dump at %s\n", cachedPath)
	return cachedPath, func() {}, nil
}

// obtainDumpUncached runs the strategy chain to produce a fresh dump
// (the pre-cache version of the original obtainDump). The returned
// cleanup removes the temp file.
func obtainDumpUncached(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts) (string, func(), error) {
	srcH, err := handler.For(source)
	if err != nil {
		return "", nil, fmt.Errorf("source @%s.%s: %w", source.Site, source.Env, err)
	}
	if dumper, ok := handler.Inner(srcH).(handler.DBDumper); ok {
		fmt.Fprintf(out, "fetching dump for @%s.%s via %s\n", source.Site, source.Env, srcH.Name())
		path, cleanup, err := dumper.DumpFor(ctx, source)
		if err == nil {
			return path, cleanup, nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return "", nil, fmt.Errorf("%s.DumpFor: %w", srcH.Name(), err)
		}
		fmt.Fprintf(out, "%s does not support DumpFor; falling back to alias.sql.source\n", srcH.Name())
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

// sourceStrategyTag is the string fed into the cache key so different
// dump strategies don't collide. Provider name wins when a provider is
// configured; otherwise it's the alias.sql.source.type or "drush".
func sourceStrategyTag(a *alias.Alias) string {
	if a.Provider.Type != "" {
		return "provider:" + a.Provider.Type
	}
	t := a.SQL.Source.Type
	if t == "" {
		return "drush"
	}
	return t
}

func effectiveTTL(a *alias.Alias, opts SqlSyncOpts) (time.Duration, error) {
	if opts.CacheTTL != 0 {
		return opts.CacheTTL, nil
	}
	return dbcache.ParseTTL(a.SQL.Cache.TTL)
}

func effectiveSourceDatabase(a *alias.Alias, opts SqlSyncOpts) string {
	if opts.SourceDatabase != "" {
		return opts.SourceDatabase
	}
	return a.SQL.Source.Database
}

func effectiveTargetDatabase(a *alias.Alias, opts SqlSyncOpts) string {
	if opts.TargetDatabase != "" {
		return opts.TargetDatabase
	}
	return a.SQL.Target.Database
}

func effectiveStructureTables(a *alias.Alias, opts SqlSyncOpts) []string {
	if len(opts.StructureTables) > 0 {
		return opts.StructureTables
	}
	return a.SQL.Source.StructureTables
}

func effectiveStructureTablesKey(a *alias.Alias, opts SqlSyncOpts) string {
	if opts.StructureTablesKey != "" {
		return opts.StructureTablesKey
	}
	return a.SQL.Source.StructureTablesKey
}

func obtainDumpDrush(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts) (string, func(), error) {
	sourceH, err := handler.For(source)
	if err != nil {
		return "", nil, fmt.Errorf("source @%s.%s: %w", source.Site, source.Env, err)
	}
	if err := sourceH.Available(); err != nil {
		return "", nil, err
	}
	sourceBin, err := resolveBin(ctx, source, sourceH)
	if err != nil {
		return "", nil, fmt.Errorf("source bin: %w", err)
	}
	dumpPath, cleanup, err := openDump(opts)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(out, "dumping @%s.%s via %s drush → %s\n", source.Site, source.Env, sourceH.Name(), dumpPath)
	if err := dumpToFile(ctx, sourceH, sourceBin, dumpPath, source, opts); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("dump: %w", err)
	}
	return dumpPath, cleanup, nil
}

// obtainDumpFile pulls a pre-made dump file via the alias's transport.
// When the remote file is NOT gzipped, dconsole compresses it ON THE
// REMOTE (`gzip -c <path>`) so the transport pipe only carries the
// compressed stream — important on high-latency / low-bandwidth links.
// If the remote file IS gzipped, we cat it directly.
func obtainDumpFile(ctx context.Context, source *alias.Alias, out io.Writer, opts SqlSyncOpts, src alias.SQLSource) (string, func(), error) {
	if src.Path == "" {
		return "", nil, fmt.Errorf("@%s.%s: sql.source.type=file requires sql.source.path", source.Site, source.Env)
	}
	sourceH, err := handler.For(source)
	if err != nil {
		return "", nil, fmt.Errorf("source @%s.%s: %w", source.Site, source.Env, err)
	}
	if err := sourceH.Available(); err != nil {
		return "", nil, err
	}
	dumpPath, cleanup, err := openDump(opts)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(out, "fetching dump file @%s.%s:%s via %s → %s\n", source.Site, source.Env, src.Path, sourceH.Name(), dumpPath)

	f, err := os.Create(dumpPath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer f.Close()

	// In-flight compression: gzip on the REMOTE when the file isn't
	// already compressed, so the wire only carries gzipped bytes.
	remoteCmd := []string{"gzip", "-c", src.Path}
	if src.SourceGzipped() {
		remoteCmd = []string{"cat", src.Path}
	}
	dlog.Cmdf(sourceH.Preview(remoteCmd))
	if err := sourceH.Pipe(ctx, remoteCmd, nil, f); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("fetch %s via %s: %w", src.Path, sourceH.Name(), err)
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

// loadDump either calls the target provider's LoadFor, the target
// transport's DBImporter (duck-typed; ddev implements this), or falls
// back to streaming the dump through `<target.bin> sql:cli`.
func loadDump(ctx context.Context, target *alias.Alias, dumpPath string, out io.Writer, opts SqlSyncOpts) error {
	targetH, err := handler.For(target)
	if err != nil {
		return fmt.Errorf("target @%s.%s: %w", target.Site, target.Env, err)
	}

	// 1. Handler DBLoader (was provider.LoadFor). Tried first because a
	// platform-aware loader almost always beats generic drush sql:cli.
	if loader, ok := handler.Inner(targetH).(handler.DBLoader); ok {
		err := loader.LoadFor(ctx, target, dumpPath)
		if err == nil {
			fmt.Fprintf(out, "imported into @%s.%s via %s\n", target.Site, target.Env, targetH.Name())
			return nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return fmt.Errorf("%s.LoadFor: %w", targetH.Name(), err)
		}
		fmt.Fprintf(out, "%s does not support LoadFor; using transport import\n", targetH.Name())
	}

	if err := targetH.Available(); err != nil {
		return err
	}

	// 2. Handler DBImporter (duck-typed). ddev implements via
	// `ddev import-db --file=<path>` — dramatically faster than the
	// drush sql:cli stream-pipe.
	if imp, ok := handler.Inner(targetH).(handler.DBImporter); ok {
		// Plumb the target DB key through the alias so ImportDB picks
		// it up.
		t := *target
		if db := effectiveTargetDatabase(target, opts); db != "" {
			t.SQL.Target.Database = db
		}
		fmt.Fprintf(out, "importing into @%s.%s via %s import-db\n", target.Site, target.Env, targetH.Name())
		if err := imp.ImportDB(ctx, &t, dumpPath); err != nil {
			return fmt.Errorf("%s import-db: %w", targetH.Name(), err)
		}
		fmt.Fprintln(out, "sql:sync complete")
		return nil
	}

	// 3. Fallback: drush sql:cli pipe.
	targetBin, err := resolveBin(ctx, target, targetH)
	if err != nil {
		return fmt.Errorf("target bin: %w", err)
	}
	fmt.Fprintf(out, "importing into @%s.%s via %s drush sql:cli\n", target.Site, target.Env, targetH.Name())
	if err := importFromFile(ctx, targetH, targetBin, dumpPath, target, opts); err != nil {
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

// dumpToFile pipes `<bin> sql:dump --gzip [--database=…]
// [--structure-tables-list=…] [--structure-tables-key=…]` into path.
func dumpToFile(ctx context.Context, h handler.Handler, bin *remotebin.Resolved, path string, source *alias.Alias, opts SqlSyncOpts) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	args := []string{"sql:dump", "--gzip"}
	if db := effectiveSourceDatabase(source, opts); db != "" && db != "default" {
		args = append(args, "--database="+db)
	}
	if k := effectiveStructureTablesKey(source, opts); k != "" {
		args = append(args, "--structure-tables-key="+k)
	}
	if st := effectiveStructureTables(source, opts); len(st) > 0 {
		args = append(args, "--structure-tables-list="+strings.Join(st, ","))
	}
	args = augmentDrushContext(source, args)
	args = append(args, dlog.DrushFlags()...)
	cmd := bin.Argv(args)
	dlog.Cmdf(h.Preview(cmd))
	return h.Pipe(ctx, cmd, nil, f)
}

// importFromFile streams a gzipped dump into `<bin> sql:cli
// [--database=…]` on the target.
func importFromFile(ctx context.Context, h handler.Handler, bin *remotebin.Resolved, path string, target *alias.Alias, opts SqlSyncOpts) error {
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
	args := []string{"sql:cli"}
	if db := effectiveTargetDatabase(target, opts); db != "" && db != "default" {
		args = append(args, "--database="+db)
	}
	args = augmentDrushContext(target, args)
	args = append(args, dlog.DrushFlags()...)
	cmd := bin.Argv(args)
	dlog.Cmdf(h.Preview(cmd))
	return h.Pipe(ctx, cmd, gz, io.Discard)
}
