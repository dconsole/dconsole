package command

import (
	"bytes"
	"context"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/remotebin"
	"github.com/heydon/dconsole/internal/run"
	"github.com/heydon/dconsole/internal/transport"
)

// Forward shells the remaining command-line through the alias's transport
// to its remote CLI. This is the default for any command dconsole doesn't
// implement itself.
func Forward(ctx context.Context, a *alias.Alias, args []string) error {
	t, err := transport.For(a)
	if err != nil {
		return err
	}
	if err := t.Available(); err != nil {
		return err
	}
	bin, err := resolveBin(ctx, a, t)
	if err != nil {
		return err
	}
	return t.Exec(ctx, bin.Argv(args), run.DefaultStdio())
}

// resolveBin returns the concrete RemoteBin for an alias, auto-probing if
// kind: auto. Used by Forward and built-in commands that need to run the
// remote CLI (sql:sync, rsync, status).
func resolveBin(ctx context.Context, a *alias.Alias, t transport.Transport) (*remotebin.Resolved, error) {
	if r, ok := remotebin.Resolve(a); ok {
		return r, nil
	}
	return remotebin.Probe(ctx, a, &transportProber{t: t})
}

// transportProber adapts a Transport to remotebin.Prober without creating
// a cyclic dependency between transport and remotebin.
type transportProber struct{ t transport.Transport }

func (p *transportProber) Run(ctx context.Context, cmd []string) ([]byte, error) {
	var stdout bytes.Buffer
	err := p.t.Pipe(ctx, cmd, nil, &stdout)
	return stdout.Bytes(), err
}
