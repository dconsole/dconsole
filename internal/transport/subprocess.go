package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/pkg/plugin"
	pkgtransport "github.com/dconsole/dconsole/pkg/transport"
)

// init wires the subprocess runner into pkg/transport as the fallback
// for unknown transport types. When the dispatcher sees a type that
// isn't in the in-tree registry, it lands here.
func init() {
	pkgtransport.SetUnknownTypeHandler(buildPluginTransport)
}

// buildPluginTransport is the Factory installed as the unknown-type
// handler. Locates the plugin binary, runs plugin-info, and returns a
// Transport shim.
func buildPluginTransport(a *alias.Alias) (Transport, error) {
	t := a.Transport.Type
	bin, err := FindPlugin(t)
	if err != nil {
		return nil, fmt.Errorf("transport type %q: %w (looked in ~/.dconsole/plugins/ and $PATH for dconsole-%s)", t, err, t)
	}
	info, err := PluginInfo(bin)
	if err != nil {
		return nil, fmt.Errorf("plugin dconsole-%s at %s: %w", t, bin, err)
	}
	if info.ProtocolVersion != plugin.ProtocolV1 {
		return nil, fmt.Errorf("plugin dconsole-%s speaks protocol v%d; this dconsole supports v%d", t, info.ProtocolVersion, plugin.ProtocolV1)
	}
	// Find the CLI dependency this plugin declared (if any) for the
	// requested type, so RequiredCLI can be reported via the runner.
	var requiredCLI string
	for _, spec := range info.Transports {
		if spec.Type == t {
			requiredCLI = spec.RequiredCLI
			break
		}
	}
	return &subprocessTransport{
		typ:         t,
		bin:         bin,
		alias:       a,
		requiredCLI: requiredCLI,
	}, nil
}

// subprocessTransport implements pkg/transport.Transport by spawning
// the plugin binary with one verb per call.
type subprocessTransport struct {
	typ         string
	bin         string
	alias       *alias.Alias
	requiredCLI string
}

func (s *subprocessTransport) Name() string { return s.typ }

func (s *subprocessTransport) Available() error {
	if s.requiredCLI != "" {
		if err := pkgtransport.CLIAvailable(s.requiredCLI); err != nil {
			return err
		}
	}
	envelopePath, cleanup, err := s.writeAliasEnvelope()
	if err != nil {
		return err
	}
	defer cleanup()
	c := s.command(context.Background(), plugin.VerbAvailable, envelopePath)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("plugin dconsole-%s available: %w\n%s", s.typ, err, stderr.String())
	}
	return nil
}

func (s *subprocessTransport) Exec(ctx context.Context, cmd []string, stdio Stdio) error {
	envelopePath, cleanup, err := s.writeAliasEnvelope()
	if err != nil {
		return err
	}
	defer cleanup()
	c := s.command(ctx, plugin.VerbExec, envelopePath, cmd...)
	c.Stdin = stdio.In
	c.Stdout = stdio.Out
	c.Stderr = stdio.Err
	return c.Run()
}

func (s *subprocessTransport) Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error {
	envelopePath, cleanup, err := s.writeAliasEnvelope()
	if err != nil {
		return err
	}
	defer cleanup()
	c := s.command(ctx, plugin.VerbPipe, envelopePath, cmd...)
	c.Stdin = in
	c.Stdout = out
	c.Stderr = os.Stderr
	return c.Run()
}

func (s *subprocessTransport) Shell(ctx context.Context, workDir string) error {
	envelopePath, cleanup, err := s.writeAliasEnvelope()
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{plugin.VerbShell, "--alias-json=" + envelopePath}
	if workDir != "" {
		args = append(args, "--workdir="+workDir)
	}
	c := exec.CommandContext(ctx, s.bin, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == plugin.ExitVerbUnsupported {
			return fmt.Errorf("plugin dconsole-%s does not support `shell`", s.typ)
		}
		return err
	}
	return nil
}

// Preview returns the local argv the runner would spawn. Plugins emit
// their planned remote argv to stdout as a JSON string array; if the
// plugin doesn't implement preview we fall back to "<bin> exec …".
func (s *subprocessTransport) Preview(remoteCmd []string) []string {
	envelopePath, cleanup, err := s.writeAliasEnvelope()
	if err != nil {
		return s.fallbackPreview(remoteCmd)
	}
	defer cleanup()
	c := s.command(context.Background(), plugin.VerbPreview, envelopePath, remoteCmd...)
	var stdout bytes.Buffer
	c.Stdout = &stdout
	if err := c.Run(); err != nil {
		return s.fallbackPreview(remoteCmd)
	}
	var argv []string
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &argv); err != nil || len(argv) == 0 {
		return s.fallbackPreview(remoteCmd)
	}
	return argv
}

func (s *subprocessTransport) fallbackPreview(remoteCmd []string) []string {
	out := []string{s.bin, plugin.VerbExec, "--alias-json=<envelope>", "--"}
	out = append(out, remoteCmd...)
	return out
}

// command builds an *exec.Cmd for the given verb + remote command.
// alias-json is passed as --alias-json=<path>; remoteCmd follows after
// a literal "--" separator so the plugin can distinguish its own flags
// from passthrough args.
func (s *subprocessTransport) command(ctx context.Context, verb, envelopePath string, remoteCmd ...string) *exec.Cmd {
	args := []string{verb, "--alias-json=" + envelopePath}
	if len(remoteCmd) > 0 {
		args = append(args, "--")
		args = append(args, remoteCmd...)
	}
	return exec.CommandContext(ctx, s.bin, args...)
}

// writeAliasEnvelope marshals the alias to a temp JSON file and returns
// its path plus a cleanup function the caller MUST defer.
func (s *subprocessTransport) writeAliasEnvelope() (string, func(), error) {
	env := plugin.AliasEnvelope{
		Site: s.alias.Site,
		Env:  s.alias.Env,
		URI:  s.alias.URI,
		Root: s.alias.Root,
		Bin:  plugin.BinSpec{Kind: s.alias.Bin.Kind, Path: s.alias.Bin.Path},
	}
	// Decode the transport and provider Raw mappings into map[string]any
	// so we can JSON-marshal them.
	if s.alias.Transport.Raw.Kind != 0 {
		var m map[string]any
		if err := s.alias.Transport.Decode(&m); err == nil {
			env.Config = m
		}
	}
	if s.alias.Provider.Raw.Kind != 0 {
		var m map[string]any
		if err := s.alias.Provider.Decode(&m); err == nil {
			env.Provider = m
		}
	}
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("marshal alias envelope: %w", err)
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

// PluginInfo returns the cached plugin-info document for a plugin
// binary, invoking the plugin once per process to populate the cache.
func PluginInfo(bin string) (plugin.PluginInfo, error) {
	pluginInfoMu.Lock()
	defer pluginInfoMu.Unlock()
	if pluginInfoCache == nil {
		pluginInfoCache = map[string]pluginInfoEntry{}
	}
	if e, ok := pluginInfoCache[bin]; ok {
		return e.info, e.err
	}
	c := exec.Command(bin, plugin.VerbPluginInfo)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		e := pluginInfoEntry{err: fmt.Errorf("plugin-info: %w\n%s", err, strings.TrimSpace(stderr.String()))}
		pluginInfoCache[bin] = e
		return plugin.PluginInfo{}, e.err
	}
	var info plugin.PluginInfo
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info); err != nil {
		e := pluginInfoEntry{err: fmt.Errorf("plugin-info: malformed JSON: %w", err)}
		pluginInfoCache[bin] = e
		return plugin.PluginInfo{}, e.err
	}
	pluginInfoCache[bin] = pluginInfoEntry{info: info}
	return info, nil
}

// ResetPluginInfoCacheForTests clears the cache between tests.
func ResetPluginInfoCacheForTests() {
	pluginInfoMu.Lock()
	defer pluginInfoMu.Unlock()
	pluginInfoCache = nil
}

type pluginInfoEntry struct {
	info plugin.PluginInfo
	err  error
}

var (
	pluginInfoMu    sync.Mutex
	pluginInfoCache map[string]pluginInfoEntry
)
