package command

import (
	"context"
	"fmt"
	"io"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/provider"
	"github.com/heydon/dconsole/internal/transport"
)

// LoginCapable is the duck-typed interface for transports and providers
// that have a login step (interactive auth, MFA, token refresh, etc.).
// Both provider.Provider and transport.Transport may implement it; any
// that don't are silently skipped.
type LoginCapable interface {
	Login(ctx context.Context, a *alias.Alias) error
}

// Auth runs the transport/provider login flow for an alias. If both the
// alias's provider AND transport implement LoginCapable, both are
// invoked (provider first — providers typically own the credential
// store). If neither does, a clear "nothing to log into" message is
// printed.
func Auth(ctx context.Context, a *alias.Alias, out io.Writer) error {
	ran := 0

	if p, _ := provider.For(a); p != nil {
		if lc, ok := p.(LoginCapable); ok {
			fmt.Fprintf(out, "logging in via provider %s\n", p.Name())
			if err := lc.Login(ctx, a); err != nil {
				return fmt.Errorf("%s login: %w", p.Name(), err)
			}
			ran++
		}
	}

	if t, err := transport.For(a); err == nil {
		if lc, ok := t.(LoginCapable); ok {
			fmt.Fprintf(out, "logging in via transport %s\n", t.Name())
			if err := lc.Login(ctx, a); err != nil {
				return fmt.Errorf("%s login: %w", t.Name(), err)
			}
			ran++
		}
	}

	if ran == 0 {
		fmt.Fprintf(out, "@%s.%s has no login step (transport=%s", a.Site, a.Env, a.Transport.Type)
		if a.Provider.Type != "" {
			fmt.Fprintf(out, ", provider=%s", a.Provider.Type)
		}
		fmt.Fprintln(out, ")")
	}
	return nil
}
