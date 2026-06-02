package command

import (
	"context"
	"fmt"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/pkg/handler"
)

// Shell drops the user into an interactive shell on the alias's remote.
// The starting directory is the alias's `root` (where drush would
// normally run); pass workDirOverride to start somewhere else.
//
// Aliased on the CLI as both `sh` and `ssh` to match `drush ssh` muscle
// memory while making it clear the remote drush isn't loaded.
func Shell(ctx context.Context, a *alias.Alias, workDirOverride string) error {
	h, err := handler.For(a)
	if err != nil {
		return err
	}
	if err := h.Available(); err != nil {
		return err
	}
	workDir := workDirOverride
	if workDir == "" {
		workDir = a.Root
	}
	if workDir == "" {
		fmt.Printf("(no root set on @%s.%s — shell will land in the container/host default dir)\n", a.Site, a.Env)
	}
	return h.Shell(ctx, workDir)
}
