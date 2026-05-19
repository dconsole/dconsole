package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/provider"
	"github.com/heydon/dconsole/internal/sitepath"
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

// RsyncOpts are the supported flags. Mode and SSHOptions are honored
// only when both endpoints are SSH and dconsole picks the real-rsync path
// (not implemented in MVP; tar-streaming is always used).
type RsyncOpts struct {
	Verbose bool
	Force   bool // bypass per-env sync_policy / allow_sync_* checks
}

// Rsync copies files between two endpoints. The default strategy is
// tar-streaming via Transport.Pipe, which works for all transports at the
// cost of staging the bytes through the local machine.
//
// When both endpoints are alias-bound, per-env Policy is consulted before
// any bytes move. --force bypasses the check.
func Rsync(ctx context.Context, src, dst *Endpoint, out io.Writer, opts RsyncOpts) error {
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
	a := &alias.Alias{Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}}}
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

