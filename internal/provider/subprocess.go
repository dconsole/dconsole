package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/transport"
	"github.com/heydon/dconsole/pkg/plugin"
	pkgprovider "github.com/heydon/dconsole/pkg/provider"
)

// init wires the subprocess runner into pkg/provider as the fallback
// for unknown provider types. The actual call-site plumbing through
// sql:sync's DumpFor/LoadFor and rsync's FilesDownload is Phase 2;
// for now this enables `dconsole inspect`/`auth` to discover plugin
// providers without erroring.
func init() {
	pkgprovider.SetUnknownTypeHandler(buildPluginProvider)
}

func buildPluginProvider(a *alias.Alias) (Provider, error) {
	t := a.Provider.Type
	bin, err := transport.FindPlugin(t)
	if err != nil {
		return nil, fmt.Errorf("provider type %q: %w (looked in ~/.dconsole/plugins/ and $PATH for dconsole-%s)", t, err, t)
	}
	info, err := transport.PluginInfo(bin)
	if err != nil {
		return nil, fmt.Errorf("plugin dconsole-%s: %w", t, err)
	}
	if info.ProtocolVersion != plugin.ProtocolV1 {
		return nil, fmt.Errorf("plugin dconsole-%s speaks protocol v%d; this dconsole supports v%d", t, info.ProtocolVersion, plugin.ProtocolV1)
	}
	return &subprocessProvider{typ: t, bin: bin, alias: a}, nil
}

// subprocessProvider shells out to dconsole-<type> for each Provider
// method. dump-for emits the dump path on stdout; load-for and
// files-download stream stdin/stdout through to the plugin.
type subprocessProvider struct {
	typ   string
	bin   string
	alias *alias.Alias
}

func (s *subprocessProvider) Name() string { return s.typ }

// Login is the LoginCapable hook. Wired via duck-typing so dconsole's
// auth command picks it up automatically.
func (s *subprocessProvider) Login(ctx context.Context, a *alias.Alias) error {
	envelopePath, cleanup, err := writeProviderEnvelope(a)
	if err != nil {
		return err
	}
	defer cleanup()
	c := exec.CommandContext(ctx, s.bin, plugin.VerbLogin, "--alias-json="+envelopePath)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == plugin.ExitVerbUnsupported {
			// Login is optional; surface the canonical "no login step" path.
			return nil
		}
		return err
	}
	return nil
}

// SyncTo dispatches the optional sync-to verb so plugin providers can
// take over the whole sql:sync (e.g. Skpr's "pull a pre-built image and
// rebuild" pattern). The target alias is plumbed via a second
// --target-alias-json= file alongside the usual --alias-json=<source>.
// Plugins that don't implement this exit with 64/66 → ErrNotSupported.
func (s *subprocessProvider) SyncTo(ctx context.Context, source, target *alias.Alias) error {
	srcPath, srcCleanup, err := writeProviderEnvelope(source)
	if err != nil {
		return err
	}
	defer srcCleanup()
	tgtPath, tgtCleanup, err := writeProviderEnvelope(target)
	if err != nil {
		return err
	}
	defer tgtCleanup()

	c := exec.CommandContext(ctx, s.bin,
		plugin.VerbSyncTo,
		"--alias-json="+srcPath,
		"--target-alias-json="+tgtPath,
	)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case plugin.ExitVerbUnsupported, plugin.ExitNotSupported:
				return ErrNotSupported
			}
		}
		return fmt.Errorf("plugin dconsole-%s sync-to: %w", s.typ, err)
	}
	return nil
}

func (s *subprocessProvider) DumpFor(ctx context.Context, a *alias.Alias) (string, func(), error) {
	envelopePath, cleanup, err := writeProviderEnvelope(a)
	if err != nil {
		return "", nil, err
	}
	defer cleanup()
	c := exec.CommandContext(ctx, s.bin, plugin.VerbDumpFor, "--alias-json="+envelopePath)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case plugin.ExitVerbUnsupported, plugin.ExitNotSupported:
				return "", nil, ErrNotSupported
			}
		}
		return "", nil, fmt.Errorf("plugin dconsole-%s dump-for: %w\n%s", s.typ, err, stderr.String())
	}
	path := bytes.TrimSpace(stdout.Bytes())
	if len(path) == 0 {
		return "", nil, fmt.Errorf("plugin dconsole-%s dump-for: empty dump path", s.typ)
	}
	// Cleanup is the plugin's responsibility; orchestrator just removes
	// the path when done.
	cleanupDump := func() { os.Remove(string(path)) }
	return string(path), cleanupDump, nil
}

func (s *subprocessProvider) LoadFor(ctx context.Context, a *alias.Alias, dumpPath string) error {
	envelopePath, cleanup, err := writeProviderEnvelope(a)
	if err != nil {
		return err
	}
	defer cleanup()
	c := exec.CommandContext(ctx, s.bin, plugin.VerbLoadFor, "--alias-json="+envelopePath, "--dump-path="+dumpPath)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case plugin.ExitVerbUnsupported, plugin.ExitNotSupported:
				return ErrNotSupported
			}
		}
		return err
	}
	return nil
}

func (s *subprocessProvider) FilesDownload(ctx context.Context, a *alias.Alias, targetDir string) error {
	envelopePath, cleanup, err := writeProviderEnvelope(a)
	if err != nil {
		return err
	}
	defer cleanup()
	c := exec.CommandContext(ctx, s.bin, plugin.VerbFilesDownload, "--alias-json="+envelopePath, "--target-dir="+targetDir)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case plugin.ExitVerbUnsupported, plugin.ExitNotSupported:
				return ErrNotSupported
			}
		}
		return err
	}
	return nil
}

// writeProviderEnvelope is a near-clone of the transport equivalent;
// the alias contents are identical, only the call site differs. Kept
// local to avoid a runtime dependency from internal/transport on
// internal/provider (the other direction is fine — provider already
// imports transport for plugin discovery).
func writeProviderEnvelope(a *alias.Alias) (string, func(), error) {
	env := plugin.AliasEnvelope{
		Site: a.Site,
		Env:  a.Env,
		URI:  a.URI,
		Root: a.Root,
		Bin:  plugin.BinSpec{Kind: a.Bin.Kind, Path: a.Bin.Path},
	}
	if a.Transport.Raw.Kind != 0 {
		var m map[string]any
		if err := a.Transport.Decode(&m); err == nil {
			env.Config = m
		}
	}
	if a.Provider.Raw.Kind != 0 {
		var m map[string]any
		if err := a.Provider.Decode(&m); err == nil {
			env.Provider = m
		}
	}
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp("", "dconsole-alias-*.json")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}
