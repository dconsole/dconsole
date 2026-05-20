package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/assetscache"
	"github.com/heydon/dconsole/internal/dlog"
	"github.com/heydon/dconsole/internal/provider"
	"github.com/heydon/dconsole/internal/sitepath"
	pkgtransport "github.com/heydon/dconsole/pkg/transport"
	"github.com/heydon/dconsole/internal/transport"
)

// Endpoint identifies a file location for `dconsole rsync`. Exactly one of
// Local or Alias is set.
type Endpoint struct {
	Local    string       // local filesystem path, empty if Alias is set
	Alias    *alias.Alias // remote alias, nil if Local is set
	PathSpec string       // "%files", "%root", "/abs/path", etc.
}

// ParseEndpoint turns "@site.env:%files" or "./local/path" into an
// Endpoint. Returns an error if the syntax is malformed.
func ParseEndpoint(token string, loader *alias.Loader) (*Endpoint, error) {
	if strings.HasPrefix(token, "@") {
		// Split off the path spec after the first ':'. Aliases never
		// contain ':' in their @site.env form.
		colon := strings.IndexByte(token, ':')
		var ref, spec string
		if colon < 0 {
			ref, spec = token, ""
		} else {
			ref, spec = token[:colon], token[colon+1:]
		}
		a, err := loader.ResolveRef(ref)
		if err != nil {
			return nil, err
		}
		return &Endpoint{Alias: a, PathSpec: spec}, nil
	}
	return &Endpoint{Local: token, PathSpec: token}, nil
}

// RsyncOpts are the supported flags for `dconsole rsync`.
//
// Mode-related fields (Mode, IncludePrivate, Pathspec, Delete) only
// take effect when both endpoints resolve to aliases — the legacy
// freeform tar-stream path ignores them. Cache fields apply to the
// provider-FilesDownload path; the rsync / diff modes are inherently
// fresh per run.
type RsyncOpts struct {
	Verbose bool
	Force   bool // bypass per-env sync_policy / allow_sync_* checks

	// Mode: "" / "auto" (default) | "rsync" | "diff" | "stage-file-proxy".
	Mode string

	// Pathspec scope. IncludePrivate adds %private. PathspecOverride
	// (set via --pathspec=LIST) replaces the default %files / %private
	// pair with an explicit list.
	IncludePrivate    bool
	PathspecOverride  []string

	// Delete: in diff mode, also remove files only on target.
	Delete bool

	// Cross-site safety bypass for scripted use (parity with sql:sync).
	ConfirmCrossSite bool

	// Provider-bundle cache controls. No-op in non-provider paths.
	Refresh  bool
	NoCache  bool
	CacheTTL time.Duration
}

// Rsync copies files between two endpoints. Two modes:
//
//   - Orchestrator branch (both endpoints alias-bound, pathspec is a
//     token like %files / %private): cross-site safety, provider
//     SyncFilesTo / FilesDownload, mode dispatch (auto / rsync / diff
//     / stage-file-proxy). Pulls and pushes via the strategy chain.
//   - Legacy tar-stream branch (freeform paths or local endpoint):
//     existing per-endpoint tar-pipe behaviour, unchanged. Provider
//     hook only fires when both endpoints are alias-bound with the
//     %files token.
//
// Cross-site (source.Site != target.Site) prompts for confirmation
// before any policy check and before any bytes move; --confirm-cross-
// site bypasses it for scripts.
func Rsync(ctx context.Context, src, dst *Endpoint, out io.Writer, in io.Reader, opts RsyncOpts) error {
	if isOrchestratable(src, dst) {
		return runAssetSync(ctx, src, dst, out, in, opts)
	}
	return rsyncLegacy(ctx, src, dst, out, opts)
}

// isOrchestratable reports whether both endpoints qualify for the new
// orchestrator path: each carries an Alias and a pathspec that resolves
// to %files / %private (the supported tokens). Freeform paths
// (`@x:/abs/path`, `./local/files`) keep the legacy behaviour.
func isOrchestratable(src, dst *Endpoint) bool {
	if src.Alias == nil || dst.Alias == nil {
		return false
	}
	return isPathspecToken(src.PathSpec) && isPathspecToken(dst.PathSpec)
}

func isPathspecToken(ps string) bool {
	switch ps {
	case "", "%files", "%private":
		return true
	}
	return false
}

// rsyncLegacy is the pre-orchestrator behaviour, kept for backward
// compatibility with freeform endpoints (arbitrary paths, local).
func rsyncLegacy(ctx context.Context, src, dst *Endpoint, out io.Writer, opts RsyncOpts) error {
	if src.Alias != nil && dst.Alias != nil {
		if err := alias.CheckSync(src.Alias, dst.Alias, opts.Force); err != nil {
			return err
		}
	}
	srcAbs, srcReader, srcCleanup, err := readSide(ctx, src, "source")
	if err != nil {
		return err
	}
	defer srcCleanup()
	if opts.Verbose {
		fmt.Fprintf(out, "source: %s\n", srcAbs)
	}
	dstAbs, dstCleanup, err := writeSide(ctx, dst, srcReader, "destination")
	if err != nil {
		return err
	}
	defer dstCleanup()
	if opts.Verbose {
		fmt.Fprintf(out, "destination: %s\n", dstAbs)
	}
	fmt.Fprintln(out, "rsync complete")
	return nil
}

// runAssetSync is the orchestrator path. Iterates pathspecs and
// dispatches by mode.
func runAssetSync(ctx context.Context, src, dst *Endpoint, out io.Writer, in io.Reader, opts RsyncOpts) error {
	if src.Alias.Site != dst.Alias.Site {
		if err := confirmCrossSite(src.Alias, dst.Alias, opts.ConfirmCrossSite, in, out); err != nil {
			return err
		}
	}
	if err := alias.CheckSync(src.Alias, dst.Alias, opts.Force); err != nil {
		return err
	}

	pathspecs := effectivePathspecs(src.PathSpec, opts)
	for _, ps := range pathspecs {
		if err := syncOnePathspec(ctx, src.Alias, dst.Alias, ps, out, opts); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "rsync complete")
	return nil
}

// effectivePathspecs decides which pathspecs to sync for this run.
// PathspecOverride (set via --pathspec=LIST) wins outright; otherwise
// %files always runs, plus %private when --include-private is set.
// An explicit pathspec on the endpoint (`@x:%private`) restricts to
// just that one.
func effectivePathspecs(endpointPS string, opts RsyncOpts) []string {
	if len(opts.PathspecOverride) > 0 {
		return opts.PathspecOverride
	}
	if endpointPS != "" {
		return []string{endpointPS}
	}
	out := []string{"%files"}
	if opts.IncludePrivate {
		out = append(out, "%private")
	}
	return out
}

// syncOnePathspec handles a single pathspec end-to-end: provider
// hooks first, then mode dispatch.
func syncOnePathspec(ctx context.Context, source, target *alias.Alias, ps string, out io.Writer, opts RsyncOpts) error {
	// 1. Provider SyncFilesTo — full end-to-end takeover. Skpr image
	//    pulls fall here.
	if p, _ := provider.For(source); p != nil {
		err := p.SyncFilesTo(ctx, source, target)
		if err == nil {
			fmt.Fprintf(out, "synced assets (%s) via provider %s (end-to-end)\n", ps, p.Name())
			return nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return fmt.Errorf("%s.SyncFilesTo: %w", p.Name(), err)
		}
	}

	// 2. Provider FilesDownload + LoadFilesFor — provider supplies the
	//    bundle, dconsole loads it.
	if p, _ := provider.For(source); p != nil {
		bundle, cleanup, err := tryProviderBundle(ctx, p, source, ps, out, opts)
		if err == nil {
			defer cleanup()
			return loadProviderBundle(ctx, target, bundle, out)
		} else if !errors.Is(err, provider.ErrNotSupported) {
			return err
		}
	}

	// 3. Mode dispatch — uses live source/target transports.
	srcAbs, dstAbs, err := resolvePathspecEnds(ctx, source, target, ps)
	if err != nil {
		return err
	}
	switch opts.Mode {
	case "stage-file-proxy":
		return configureStageFileProxy(ctx, source, target, out, opts)
	case "diff":
		return diffSyncOne(ctx, source, target, srcAbs, dstAbs, out, opts)
	case "rsync":
		return rsyncOnly(ctx, source, target, srcAbs, dstAbs, opts, out)
	default: // "auto" or ""
		err := autoRsync(ctx, source, target, srcAbs, dstAbs, opts, out)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errStrategyNotApplicable) {
			return err
		}
		dlog.Infof("auto: falling back to diff+tar")
		return diffSyncOne(ctx, source, target, srcAbs, dstAbs, out, opts)
	}
}

// resolvePathspecEnds asks each side's sitepath for the absolute path
// of the named pathspec and returns the pair.
func resolvePathspecEnds(ctx context.Context, source, target *alias.Alias, ps string) (string, string, error) {
	srcT, err := transport.For(source)
	if err != nil {
		return "", "", fmt.Errorf("source transport: %w", err)
	}
	srcBin, err := resolveBin(ctx, source, srcT)
	if err != nil {
		return "", "", fmt.Errorf("source bin: %w", err)
	}
	srcPaths, err := sitepath.Resolve(ctx, source, srcBin, &transportProber{t: srcT})
	if err != nil {
		return "", "", fmt.Errorf("source sitepath: %w", err)
	}
	srcAbs, err := sitepathToken(source, srcPaths, ps)
	if err != nil {
		return "", "", err
	}

	tgtT, err := transport.For(target)
	if err != nil {
		return "", "", fmt.Errorf("target transport: %w", err)
	}
	tgtBin, err := resolveBin(ctx, target, tgtT)
	if err != nil {
		return "", "", fmt.Errorf("target bin: %w", err)
	}
	tgtPaths, err := sitepath.Resolve(ctx, target, tgtBin, &transportProber{t: tgtT})
	if err != nil {
		return "", "", fmt.Errorf("target sitepath: %w", err)
	}
	dstAbs, err := sitepathToken(target, tgtPaths, ps)
	if err != nil {
		return "", "", err
	}
	return srcAbs, dstAbs, nil
}

// sitepathToken maps a token to the absolute path on the alias.
func sitepathToken(a *alias.Alias, paths *sitepath.Paths, ps string) (string, error) {
	switch ps {
	case "", "%files":
		if paths.Files == "" {
			return "", fmt.Errorf("@%s.%s has no files path", a.Site, a.Env)
		}
		return paths.Files, nil
	case "%private":
		if paths.Private == "" {
			return "", fmt.Errorf("@%s.%s has no private files path", a.Site, a.Env)
		}
		return paths.Private, nil
	}
	return "", fmt.Errorf("unsupported pathspec %q for orchestrator", ps)
}

// diffSyncOne runs the diff mode for one pathspec.
func diffSyncOne(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, out io.Writer, opts RsyncOpts) error {
	srcT, _ := transport.For(source)
	tgtT, _ := transport.For(target)
	fmt.Fprintf(out, "scanning @%s.%s:%s and @%s.%s:%s …\n", source.Site, source.Env, srcAbs, target.Site, target.Env, dstAbs)
	srcFiles, tgtFiles, err := scanRemoteConcurrent(ctx, srcT, srcAbs, tgtT, dstAbs)
	if err != nil {
		return err
	}
	changed, deletedOnTarget := diffSets(srcFiles, tgtFiles)
	summary := Summarise(srcFiles, changed, deletedOnTarget)
	fmt.Fprintf(out, "%d changed, %d unchanged, %d only-on-target (--delete=%v)\n",
		summary.Changed, summary.UnchangedFiles, summary.DeletedOnTarget, opts.Delete)
	if len(changed) > 0 {
		if err := streamChanged(ctx, srcT, srcAbs, tgtT, dstAbs, changed); err != nil {
			return err
		}
	}
	if opts.Delete && len(deletedOnTarget) > 0 {
		if err := removeOnTarget(ctx, tgtT, dstAbs, deletedOnTarget); err != nil {
			return err
		}
	}
	return nil
}

// tryProviderBundle handles the provider.FilesDownload path with
// caching. Returns ErrNotSupported when the provider declines.
func tryProviderBundle(ctx context.Context, p provider.Provider, source *alias.Alias, ps string, out io.Writer, opts RsyncOpts) (string, func(), error) {
	if opts.NoCache {
		return providerDownloadAndBundle(ctx, p, source, ps, out)
	}
	key := assetscache.KeyFor(assetscache.KeyInputs{
		Site:     source.Site,
		Env:      source.Env,
		Strategy: "provider:" + p.Name(),
		Pathspec: ps,
	})
	if !opts.Refresh {
		ttl, err := effectiveAssetsTTL(source, opts)
		if err != nil {
			return "", nil, err
		}
		if path, hit, _ := assetscache.Get(key, ttl); hit {
			fmt.Fprintf(out, "cache hit: %s (TTL %s)\n", path, ttl)
			return path, func() {}, nil
		}
	} else {
		_ = assetscache.Invalidate(key)
	}
	bundle, cleanup, err := providerDownloadAndBundle(ctx, p, source, ps, out)
	if err != nil {
		return "", nil, err
	}
	cached, err := assetscache.Put(key, bundle, assetscache.Metadata{
		Site:     source.Site,
		Env:      source.Env,
		Strategy: "provider:" + p.Name(),
		Pathspec: ps,
	})
	if err != nil {
		fmt.Fprintf(out, "warning: cache write failed (%v); using temp bundle\n", err)
		return bundle, cleanup, nil
	}
	cleanup() // remove the temp bundle; cached file is canonical
	fmt.Fprintf(out, "cached bundle at %s\n", cached)
	return cached, func() {}, nil
}

func providerDownloadAndBundle(ctx context.Context, p provider.Provider, source *alias.Alias, ps string, out io.Writer) (string, func(), error) {
	stage, err := os.MkdirTemp("", "dconsole-provider-files-")
	if err != nil {
		return "", nil, err
	}
	if err := p.FilesDownload(ctx, source, stage); err != nil {
		os.RemoveAll(stage)
		return "", nil, err
	}
	bundle, err := tarGzipDir(stage)
	os.RemoveAll(stage)
	if err != nil {
		return "", nil, err
	}
	return bundle, func() { os.Remove(bundle) }, nil
}

func loadProviderBundle(ctx context.Context, target *alias.Alias, bundle string, out io.Writer) error {
	if p, _ := provider.For(target); p != nil {
		err := p.LoadFilesFor(ctx, target, bundle)
		if err == nil {
			fmt.Fprintf(out, "loaded bundle via provider %s\n", p.Name())
			return nil
		}
		if !errors.Is(err, provider.ErrNotSupported) {
			return fmt.Errorf("%s.LoadFilesFor: %w", p.Name(), err)
		}
	}
	tgtT, err := transport.For(target)
	if err != nil {
		return err
	}
	if imp, ok := tgtT.(pkgtransport.FilesImporter); ok {
		fmt.Fprintf(out, "loading bundle via %s import-files\n", tgtT.Name())
		return imp.ImportFiles(ctx, target, bundle)
	}
	// Plain tar-stream: stage and push.
	stage, err := os.MkdirTemp("", "dconsole-loadbundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := untarLocalFile(bundle, stage); err != nil {
		return err
	}
	// Without a known target abs path, can't tar-stream-push meaningfully.
	return fmt.Errorf("target transport %s has no native files importer; provider-bundle path falls through to per-pathspec push (use diff mode for now)", tgtT.Name())
}

func untarLocalFile(tarball, dst string) error {
	cmd := exec.Command("tar", "xzf", tarball, "-C", dst)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// effectiveAssetsTTL: --cache-ttl wins, then alias.assets.cache.ttl,
// then assetscache.DefaultTTL.
func effectiveAssetsTTL(a *alias.Alias, opts RsyncOpts) (time.Duration, error) {
	if opts.CacheTTL != 0 {
		return opts.CacheTTL, nil
	}
	return assetscache.ParseTTL(a.Assets.Cache.TTL)
}

// configureStageFileProxy enables and configures the Stage File Proxy
// contributed module on the target via drush. drush 9+ uses
// `pm:enable` + `config:set`; drush 8 uses `pm-enable` + `vset`. We
// try the d9+ syntax first and fall back on failure.
func configureStageFileProxy(ctx context.Context, source, target *alias.Alias, out io.Writer, opts RsyncOpts) error {
	if source.URI == "" {
		return fmt.Errorf("--mode=stage-file-proxy requires the source alias (@%s.%s) to declare uri:", source.Site, source.Env)
	}
	tgtT, err := transport.For(target)
	if err != nil {
		return err
	}
	if err := tgtT.Available(); err != nil {
		return err
	}
	bin, err := resolveBin(ctx, target, tgtT)
	if err != nil {
		return err
	}

	// Try drush 9+ commands first.
	enableD9 := augmentDrushContext(target, []string{"pm:enable", "stage_file_proxy", "-y"})
	enableD9 = append(enableD9, dlog.DrushFlags()...)
	enableCmd := bin.Argv(enableD9)
	dlog.Cmdf(tgtT.Preview(enableCmd))
	if err := tgtT.Exec(ctx, enableCmd, defaultStdio()); err != nil {
		// Cascade to drush 8: pm-enable.
		fmt.Fprintf(out, "drush pm:enable failed (%v); trying drush 8 pm-enable\n", err)
		enableD8 := augmentDrushContext(target, []string{"pm-enable", "stage_file_proxy", "-y"})
		enableD8 = append(enableD8, dlog.DrushFlags()...)
		enableCmd8 := bin.Argv(enableD8)
		dlog.Cmdf(tgtT.Preview(enableCmd8))
		if err := tgtT.Exec(ctx, enableCmd8, defaultStdio()); err != nil {
			return fmt.Errorf("enable stage_file_proxy on @%s.%s: %w", target.Site, target.Env, err)
		}
	}

	// Try drush 9+ config:set first; fall back to drush 8 vset.
	setD9 := augmentDrushContext(target, []string{"config:set", "stage_file_proxy.settings", "origin", source.URI, "-y"})
	setD9 = append(setD9, dlog.DrushFlags()...)
	setCmd := bin.Argv(setD9)
	dlog.Cmdf(tgtT.Preview(setCmd))
	if err := tgtT.Exec(ctx, setCmd, defaultStdio()); err != nil {
		fmt.Fprintf(out, "drush config:set failed (%v); trying drush 8 vset\n", err)
		setD8 := augmentDrushContext(target, []string{"vset", "stage_file_proxy_origin", source.URI, "-y"})
		setD8 = append(setD8, dlog.DrushFlags()...)
		setCmd8 := bin.Argv(setD8)
		dlog.Cmdf(tgtT.Preview(setCmd8))
		if err := tgtT.Exec(ctx, setCmd8, defaultStdio()); err != nil {
			return fmt.Errorf("set stage_file_proxy origin on @%s.%s: %w", target.Site, target.Env, err)
		}
	}

	fmt.Fprintf(out, "stage_file_proxy configured on @%s.%s with origin %s\n", target.Site, target.Env, source.URI)
	return nil
}

func defaultStdio() pkgtransport.Stdio {
	return pkgtransport.Stdio{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}

// readSide opens the source endpoint as an io.Reader carrying a tar
// stream of the source directory.
func readSide(ctx context.Context, e *Endpoint, side string) (absPath string, r io.Reader, cleanup func(), err error) {
	if e.Local != "" {
		info, statErr := os.Stat(e.Local)
		if statErr != nil {
			return "", nil, func() {}, fmt.Errorf("%s: %w", side, statErr)
		}
		if !info.IsDir() {
			return "", nil, func() {}, fmt.Errorf("%s %q is not a directory", side, e.Local)
		}
		buf, err := tarLocal(ctx, e.Local)
		if err != nil {
			return "", nil, func() {}, fmt.Errorf("%s tar: %w", side, err)
		}
		return e.Local, buf, func() {}, nil
	}

	// Provider override: pull files from provider to a local stage, then
	// tar that. Only meaningful for the `%files` token; for other paths
	// we'd need a more specific provider API.
	if p, _ := provider.For(e.Alias); p != nil && (e.PathSpec == "%files" || e.PathSpec == "") {
		stage, err := os.MkdirTemp("", "dconsole-provider-*")
		if err != nil {
			return "", nil, func() {}, err
		}
		cleanup := func() { os.RemoveAll(stage) }
		err = p.FilesDownload(ctx, e.Alias, stage)
		if err == nil {
			buf, err := tarLocal(ctx, stage)
			if err != nil {
				cleanup()
				return "", nil, func() {}, fmt.Errorf("%s tar of provider stage: %w", side, err)
			}
			return stage, buf, cleanup, nil
		}
		cleanup()
		if !errors.Is(err, provider.ErrNotSupported) {
			return "", nil, func() {}, fmt.Errorf("%s provider %s: %w", side, p.Name(), err)
		}
		// Fall through to generic transport tar.
	}

	t, abs, err := remoteAbsPath(ctx, e)
	if err != nil {
		return "", nil, func() {}, fmt.Errorf("%s: %w", side, err)
	}
	var buf bytes.Buffer
	cmd := []string{"tar", "cf", "-", "-C", abs, "."}
	if err := t.Pipe(ctx, cmd, nil, &buf); err != nil {
		return "", nil, func() {}, fmt.Errorf("%s tar via %s: %w", side, t.Name(), err)
	}
	return abs, &buf, func() {}, nil
}

// writeSide consumes a tar stream and unpacks it at the destination.
func writeSide(ctx context.Context, e *Endpoint, r io.Reader, side string) (absPath string, cleanup func(), err error) {
	if e.Local != "" {
		if err := os.MkdirAll(e.Local, 0o755); err != nil {
			return "", func() {}, fmt.Errorf("%s mkdir: %w", side, err)
		}
		if err := untarLocal(ctx, e.Local, r); err != nil {
			return "", func() {}, fmt.Errorf("%s untar: %w", side, err)
		}
		return e.Local, func() {}, nil
	}

	t, abs, err := remoteAbsPath(ctx, e)
	if err != nil {
		return "", func() {}, fmt.Errorf("%s: %w", side, err)
	}
	cmd := []string{"sh", "-c", fmt.Sprintf("mkdir -p %s && tar xf - -C %s", shellQuoteArg(abs), shellQuoteArg(abs))}
	if err := t.Pipe(ctx, cmd, r, io.Discard); err != nil {
		return "", func() {}, fmt.Errorf("%s untar via %s: %w", side, t.Name(), err)
	}
	return abs, func() {}, nil
}

func remoteAbsPath(ctx context.Context, e *Endpoint) (transport.Transport, string, error) {
	t, err := transport.For(e.Alias)
	if err != nil {
		return nil, "", err
	}
	if err := t.Available(); err != nil {
		return nil, "", err
	}
	bin, err := resolveBin(ctx, e.Alias, t)
	if err != nil {
		return nil, "", err
	}
	paths, err := sitepath.Resolve(ctx, e.Alias, bin, &transportProber{t: t})
	if err != nil {
		return nil, "", err
	}
	abs, err := paths.Expand(e.PathSpec)
	if err != nil {
		return nil, "", err
	}
	return t, abs, nil
}

func tarLocal(ctx context.Context, dir string) (*bytes.Buffer, error) {
	// Implemented via local `tar` for cross-platform behavior. macOS and
	// Linux both ship a compatible `tar`.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	cmd := []string{"tar", "cf", "-", "-C", abs, "."}
	if err := runLocal(ctx, cmd, nil, &buf); err != nil {
		return nil, err
	}
	return &buf, nil
}

func untarLocal(ctx context.Context, dir string, r io.Reader) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	cmd := []string{"tar", "xf", "-", "-C", abs}
	return runLocal(ctx, cmd, r, io.Discard)
}

// Local exec helper to avoid importing os/exec everywhere.
func runLocal(ctx context.Context, argv []string, in io.Reader, out io.Writer) error {
	// Reuse the exec transport's mechanics by constructing an in-memory
	// alias with transport: exec, then using Pipe. This keeps process
	// spawning logic in one place (the exec transport).
	a := &alias.Alias{Transport: alias.NewTransport("exec", nil)}
	t, err := transport.For(a)
	if err != nil {
		return err
	}
	return t.Pipe(ctx, argv, in, out)
}

// minimal shell-quote that's safe enough for path arguments.
func shellQuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '/':
			continue
		}
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

