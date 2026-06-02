package command

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/dlog"
	"github.com/dconsole/dconsole/internal/remotebin"
	"github.com/dconsole/dconsole/internal/run"
	"github.com/dconsole/dconsole/internal/transport"
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
	args = augmentDrushContext(a, args)
	args = appendDrushFlags(args)
	cmd := bin.Argv(args)
	dlog.Cmdf(t.Preview(cmd))
	return t.Exec(ctx, cmd, run.DefaultStdio())
}

// augmentDrushContext prepends --root and --uri to args so drush can
// bootstrap a Drupal site regardless of the remote CWD. Drush 8 in
// particular won't find database credentials without --root, and the
// user-visible "Unable to find a matching SQL Class" failure is the
// symptom. Drush 9+ is more forgiving but the flags are still
// authoritative. We skip whichever flag the caller already supplied
// (so manual `dconsole @x cr --root=/other` still works).
//
// drush accepts the long forms (--root, --uri) and short -r/-l on
// every supported version; we use the long forms for readability.
func augmentDrushContext(a *alias.Alias, args []string) []string {
	if a.Root != "" && !hasDrushFlag(args, "--root", "-r") {
		args = append([]string{"--root=" + a.Root}, args...)
	}
	if a.URI != "" && !hasDrushFlag(args, "--uri", "-l") {
		args = append([]string{"--uri=" + a.URI}, args...)
	}
	return args
}

func hasDrushFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, name := range names {
			if a == name || strings.HasPrefix(a, name+"=") {
				return true
			}
		}
	}
	return false
}

// appendDrushFlags adds -v/-vv/-vvv to args if dlog is active AND the
// user hasn't already typed an explicit verbose flag of their own. The
// check is intentionally trivial — duplicates like "drush -v cr -v" are
// harmless to drush, so we just avoid the obvious overlap.
func appendDrushFlags(args []string) []string {
	flags := dlog.DrushFlags()
	if len(flags) == 0 {
		return args
	}
	for _, a := range args {
		switch a {
		case "-v", "-vv", "-vvv", "--verbose", "--debug":
			return args
		}
	}
	return append(args, flags...)
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

// Run executes a probe (no stdin, captured stdout) and SUPPRESSES the
// remote command's stderr on the user's terminal. Auto-probe routinely
// tries candidates that don't exist (e.g. `drupal --version` on a
// drush-only site) — letting the remote shell's "command not found"
// noise leak through would make every successful run look like an
// error. On failure we wrap the captured stderr into the returned
// error so -v / -vv users can still diagnose what happened.
//
// Uses Exec (which lets us thread a stderr writer) rather than Pipe
// (which hardcodes stderr to os.Stderr).
func (p *transportProber) Run(ctx context.Context, cmd []string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	err := p.t.Exec(ctx, cmd, transport.Stdio{Out: &stdout, Err: &stderr})
	if err != nil && stderr.Len() > 0 {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), err
}
