package command

import (
	"context"
	"fmt"
	"io"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/pkg/handler"
)

// Auth runs the handler chain's login flow for an alias. Each layer
// that implements handler.LoginCapable is invoked in inner-to-outer
// order — inner layers typically own credentials (Skpr's CLI, ddev
// launch), outer layers might add bastion-style auth (rare).
//
// If no layer implements Login, a clear "nothing to log into" message
// is printed rather than failing.
func Auth(ctx context.Context, a *alias.Alias, out io.Writer) error {
	h, err := handler.For(a)
	if err != nil {
		return err
	}

	layers := flattenForLogin(h)
	ran := 0
	for _, l := range layers {
		lc, ok := l.(handler.LoginCapable)
		if !ok {
			continue
		}
		fmt.Fprintf(out, "logging in via %s\n", l.Name())
		if err := lc.Login(ctx, a); err != nil {
			return fmt.Errorf("%s login: %w", l.Name(), err)
		}
		ran++
	}

	if ran == 0 {
		fmt.Fprintf(out, "@%s.%s has no login step (handler=%s)\n", a.Site, a.Env, h.Name())
	}
	return nil
}

// flattenForLogin returns the chain layers in inner-to-outer order so
// credential-owning layers (the innermost, where the platform CLI
// lives) run first. For a single handler the slice contains just that
// handler.
func flattenForLogin(h handler.Handler) []handler.Handler {
	if c, ok := h.(*handler.Chain); ok {
		ls := c.Layers()
		out := make([]handler.Handler, 0, len(ls))
		for i := len(ls) - 1; i >= 0; i-- {
			out = append(out, ls[i])
		}
		return out
	}
	return []handler.Handler{h}
}
