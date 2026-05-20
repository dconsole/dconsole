package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/dlog"
	"github.com/heydon/dconsole/internal/transport"
	pkgtransport "github.com/heydon/dconsole/pkg/transport"
)

// errStrategyNotApplicable means "this strategy doesn't fit the
// current topology — cascade to the next." Returned errors that
// AREN'T this sentinel are real failures (auth denied mid-transfer,
// network drop, …) and abort the whole sync.
var errStrategyNotApplicable = errors.New("strategy not applicable")

// rsyncStrategy is the auto-mode contract: each strategy reports
// applicability with a returned error of errStrategyNotApplicable or
// runs the sync and returns nil/real-error.
type rsyncStrategy func(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, opts RsyncOpts) error

// autoRsync walks the strategy chain in priority order; the first one
// that doesn't return errStrategyNotApplicable wins. After the rsync
// strategies, the diff+tar fallback runs unconditionally.
func autoRsync(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, opts RsyncOpts, out io.Writer) error {
	for i, s := range []struct {
		name string
		fn   rsyncStrategy
	}{
		{"same-host rsync", trySameHostRsync},
		{"local-mediated rsync", tryLocalMediatedRsync},
		{"source-driven rsync", trySourceDrivenRsync},
	} {
		dlog.Infof("auto: trying strategy %d (%s)", i+1, s.name)
		err := s.fn(ctx, source, target, srcAbs, dstAbs, opts)
		if err == nil {
			fmt.Fprintf(out, "rsync via %s: complete\n", s.name)
			return nil
		}
		if !errors.Is(err, errStrategyNotApplicable) {
			return err
		}
	}
	// Diff+tar fallback. The orchestrator passes in a separate helper
	// for this; rsync_strategies.go owns only the rsync paths.
	return errStrategyNotApplicable
}

// rsyncOnly is the body of --mode=rsync. Walks the rsync strategies
// like autoRsync but does NOT fall through to diff+tar; returns a
// composite error listing what topologies would have worked.
func rsyncOnly(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, opts RsyncOpts, out io.Writer) error {
	for i, s := range []struct {
		name string
		fn   rsyncStrategy
	}{
		{"same-host rsync", trySameHostRsync},
		{"local-mediated rsync", tryLocalMediatedRsync},
		{"source-driven rsync", trySourceDrivenRsync},
	} {
		dlog.Infof("rsync mode: trying strategy %d (%s)", i+1, s.name)
		err := s.fn(ctx, source, target, srcAbs, dstAbs, opts)
		if err == nil {
			fmt.Fprintf(out, "rsync via %s: complete\n", s.name)
			return nil
		}
		if !errors.Is(err, errStrategyNotApplicable) {
			return err
		}
	}
	return fmt.Errorf("--mode=rsync: no rsync strategy applies for source transport %q → target transport %q "+
		"(works when both are ssh on the same host, or source is ssh, or both are ssh with source→target reachability). "+
		"Try --mode=diff if rsync isn't an option here.",
		source.Transport.Type, target.Transport.Type)
}

// trySameHostRsync runs `ssh user@host -- rsync -avz [--delete] src/ dst/`
// when source and target share the same ssh endpoint. Strategy 1.
func trySameHostRsync(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, opts RsyncOpts) error {
	srcT, err := transport.For(source)
	if err != nil {
		return errStrategyNotApplicable
	}
	tgtT, err := transport.For(target)
	if err != nil {
		return errStrategyNotApplicable
	}
	srcSSH, sok := srcT.(pkgtransport.RsyncSSH)
	tgtSSH, tok := tgtT.(pkgtransport.RsyncSSH)
	if !sok || !tok {
		return errStrategyNotApplicable
	}
	srcRemote, srcOpts := srcSSH.RsyncRemote()
	tgtRemote, _ := tgtSSH.RsyncRemote()
	if srcRemote != tgtRemote {
		return errStrategyNotApplicable
	}

	args := append([]string(nil), srcOpts...)
	args = append(args, srcRemote, "--",
		"rsync", "-avz",
	)
	if opts.Delete {
		args = append(args, "--delete")
	}
	args = append(args, srcAbs+"/", dstAbs+"/")

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	dlog.Cmdf(cmd.Args)
	return cmd.Run()
}

// tryLocalMediatedRsync pulls source via the local rsync binary, then
// pushes to the target via the target's transport. Strategy 2 — the
// standard dev-laptop case (prod ssh → local ddev or ssh).
func tryLocalMediatedRsync(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, opts RsyncOpts) error {
	srcT, err := transport.For(source)
	if err != nil {
		return errStrategyNotApplicable
	}
	srcSSH, ok := srcT.(pkgtransport.RsyncSSH)
	if !ok {
		return errStrategyNotApplicable
	}

	staging, err := os.MkdirTemp("", "dconsole-rsync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	srcRemote, srcOpts := srcSSH.RsyncRemote()
	rshArg := buildRshArg(srcOpts)

	pullArgs := []string{"-avz"}
	if opts.Delete {
		pullArgs = append(pullArgs, "--delete")
	}
	pullArgs = append(pullArgs, "-e", rshArg, srcRemote+":"+srcAbs+"/", staging+"/")

	cmd := exec.CommandContext(ctx, "rsync", pullArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	dlog.Cmdf(cmd.Args)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rsync pull: %w", err)
	}

	// Now push staging → target.
	return pushStagingToTarget(ctx, target, staging, dstAbs, opts)
}

// trySourceDrivenRsync ssh's into source and runs rsync FROM source
// TO target. Strategy 3 — for the prod→test cross-host case where
// source has outbound network and credentials (agent forwarded).
func trySourceDrivenRsync(ctx context.Context, source, target *alias.Alias, srcAbs, dstAbs string, opts RsyncOpts) error {
	srcT, err := transport.For(source)
	if err != nil {
		return errStrategyNotApplicable
	}
	tgtT, err := transport.For(target)
	if err != nil {
		return errStrategyNotApplicable
	}
	srcSSH, sok := srcT.(pkgtransport.RsyncSSH)
	tgtSSH, tok := tgtT.(pkgtransport.RsyncSSH)
	if !sok || !tok {
		return errStrategyNotApplicable
	}
	srcRemote, srcOpts := srcSSH.RsyncRemote()
	tgtRemote, _ := tgtSSH.RsyncRemote()
	if srcRemote == tgtRemote {
		// Same-host case is strategy 1; skip here so we don't
		// accidentally double-run.
		return errStrategyNotApplicable
	}

	args := append([]string{"-A"}, srcOpts...) // -A forwards local ssh-agent
	args = append(args, srcRemote, "--",
		"rsync", "-avz",
	)
	if opts.Delete {
		args = append(args, "--delete")
	}
	args = append(args, srcAbs+"/", tgtRemote+":"+dstAbs+"/")

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	dlog.Cmdf(cmd.Args)
	return cmd.Run()
}

// pushStagingToTarget moves bytes from a local staging directory into
// the target. Tries (in order):
//
//   - target ssh? → rsync push from staging.
//   - target implements FilesImporter (ddev)? → tarball staging, hand
//     to ImportFiles (ddev import-files unpacks into sites/default/files).
//   - else → tar-stream staging via target.Pipe.
func pushStagingToTarget(ctx context.Context, target *alias.Alias, staging, dstAbs string, opts RsyncOpts) error {
	tgtT, err := transport.For(target)
	if err != nil {
		return err
	}
	if tgtSSH, ok := tgtT.(pkgtransport.RsyncSSH); ok {
		tgtRemote, tgtOpts := tgtSSH.RsyncRemote()
		args := []string{"-avz"}
		if opts.Delete {
			args = append(args, "--delete")
		}
		args = append(args, "-e", buildRshArg(tgtOpts), staging+"/", tgtRemote+":"+dstAbs+"/")
		cmd := exec.CommandContext(ctx, "rsync", args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		dlog.Cmdf(cmd.Args)
		return cmd.Run()
	}
	if imp, ok := tgtT.(pkgtransport.FilesImporter); ok {
		// Tarball the staging dir and hand to ddev import-files.
		bundle, err := tarGzipDir(staging)
		if err != nil {
			return err
		}
		defer os.Remove(bundle)
		return imp.ImportFiles(ctx, target, bundle)
	}
	// Plain tar-stream push.
	return tarStreamPushDir(ctx, tgtT, staging, dstAbs)
}

// buildRshArg returns the `ssh <opts>` string rsync's `-e` flag wants
// (single argument; spaces are how rsync parses ssh options).
func buildRshArg(sshOpts []string) string {
	parts := append([]string{"ssh"}, sshOpts...)
	return joinShellArg(parts)
}

// joinShellArg is a minimal shell-quoter: spaces become quoted, single
// quotes are escaped. Sufficient for the option args we emit.
func joinShellArg(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// tarGzipDir produces a gzipped tarball of dir at a temp path and
// returns the path. Caller is responsible for removing the file.
func tarGzipDir(dir string) (string, error) {
	f, err := os.CreateTemp("", "dconsole-assets-*.tar.gz")
	if err != nil {
		return "", err
	}
	f.Close()
	cmd := exec.Command("tar", "czf", f.Name(), "-C", dir, ".")
	cmd.Stderr = os.Stderr
	dlog.Cmdf(cmd.Args)
	if err := cmd.Run(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("tar dir %s: %w", dir, err)
	}
	return f.Name(), nil
}

// tarStreamPushDir streams the contents of local `dir` into `dstAbs`
// on the target via tar pipe — the fallback when target has neither
// ssh-rsync nor FilesImporter (e.g. plain docker/kubectl).
func tarStreamPushDir(ctx context.Context, t transport.Transport, dir, dstAbs string) error {
	srcCmd := exec.Command("tar", "czf", "-", "-C", dir, ".")
	srcOut, err := srcCmd.StdoutPipe()
	if err != nil {
		return err
	}
	srcCmd.Stderr = os.Stderr
	if err := srcCmd.Start(); err != nil {
		return err
	}
	dstCmd := []string{"tar", "xzf", "-", "-C", dstAbs}
	dlog.Cmdf(append([]string{"tar", "czf", "-", "-C", dir, "."}, "|", "(relay)", "|"))
	dlog.Cmdf(t.Preview(dstCmd))
	tgtErr := t.Pipe(ctx, dstCmd, srcOut, io.Discard)
	if waitErr := srcCmd.Wait(); waitErr != nil && tgtErr == nil {
		return fmt.Errorf("local tar: %w", waitErr)
	}
	return tgtErr
}
