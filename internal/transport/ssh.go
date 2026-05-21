package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/run"
)

type sshTransport struct {
	cfg *alias.SSHTransport
}

func init() {
	Register("ssh", Registration{
		RequiredCLI: "ssh",
		Build: func(a *alias.Alias) (Transport, error) {
			var w struct {
				SSH alias.SSHTransport `yaml:"ssh"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("transport type ssh: %w", err)
			}
			if w.SSH.Host == "" {
				return nil, fmt.Errorf("transport type ssh requires an ssh: block with host")
			}
			cfg := w.SSH
			return &sshTransport{cfg: &cfg}, nil
		},
	})
}

func (s *sshTransport) Name() string { return "ssh" }

func (s *sshTransport) Available() error { return CLIAvailable("ssh") }

func (s *sshTransport) Exec(ctx context.Context, remoteCmd []string, stdio run.Stdio) error {
	cmd := exec.CommandContext(ctx, "ssh", s.sshArgs(remoteCmd)...)
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	return cmd.Run()
}

func (s *sshTransport) Pipe(ctx context.Context, remoteCmd []string, in io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "ssh", s.sshArgs(remoteCmd)...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *sshTransport) Preview(remoteCmd []string) []string {
	return append([]string{"ssh"}, s.sshArgs(remoteCmd)...)
}

func (s *sshTransport) Shell(ctx context.Context, workDir string) error {
	// `ssh -t` forces TTY allocation; we cd into workDir then exec a
	// login shell so the user lands in their normal environment.
	target := s.cfg.Host
	if s.cfg.User != "" {
		target = s.cfg.User + "@" + s.cfg.Host
	}
	args := []string{"-t"}
	if s.cfg.Port != 0 {
		args = append(args, "-p", strconv.Itoa(s.cfg.Port))
	}
	if path := expandIdentityPath(s.cfg.IdentityFile); path != "" {
		args = append(args, "-i", path, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, s.cfg.Options...)
	cdCmd := ""
	if workDir != "" {
		cdCmd = "cd " + singleQuote(workDir) + " && "
	}
	args = append(args, target, "--", cdCmd+`exec "$SHELL" -l`)
	c := exec.CommandContext(ctx, "ssh", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func (s *sshTransport) sshArgs(remoteCmd []string) []string {
	args := []string{}
	if s.cfg.Port != 0 {
		args = append(args, "-p", strconv.Itoa(s.cfg.Port))
	}
	if path := expandIdentityPath(s.cfg.IdentityFile); path != "" {
		// `IdentitiesOnly=yes` makes ssh actually USE this key instead of
		// trying agent keys first and getting locked out after too many
		// attempts. Without it, ssh treats -i as additive.
		args = append(args, "-i", path, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, s.cfg.Options...)
	target := s.cfg.Host
	if s.cfg.User != "" {
		target = s.cfg.User + "@" + s.cfg.Host
	}
	args = append(args, target, "--", shellQuote(remoteCmd))
	return args
}

// RsyncRemote satisfies pkg/transport.RsyncSSH by exposing the
// ssh-connection bits in a form rsync's `-e "ssh <opts>"` can consume.
//
// remote: the user@host (or just host) argument rsync expects on the
// remote side of its argument list, e.g. "deploy@example.com" — rsync
// concatenates this with ":<path>" itself.
//
// sshOpts: the args to splice in to `-e ssh <opts>` so rsync's ssh
// invocation uses the same identity file / port / extra options the
// transport itself uses for Exec/Pipe. The slice does NOT include the
// `ssh` literal and does NOT include the remote / `--` / command.
func (s *sshTransport) RsyncRemote() (string, []string) {
	var opts []string
	if s.cfg.Port != 0 {
		opts = append(opts, "-p", strconv.Itoa(s.cfg.Port))
	}
	if path := expandIdentityPath(s.cfg.IdentityFile); path != "" {
		opts = append(opts, "-i", path, "-o", "IdentitiesOnly=yes")
	}
	opts = append(opts, s.cfg.Options...)
	target := s.cfg.Host
	if s.cfg.User != "" {
		target = s.cfg.User + "@" + s.cfg.Host
	}
	return target, opts
}

// expandIdentityPath turns "~" and "~/" prefixes into absolute paths so
// the ssh client (which doesn't expand ~ itself for -i) gets a usable
// path. Empty input returns empty.
func expandIdentityPath(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return strings.Replace(p, "~", home, 1)
		}
	}
	return p
}

// shellQuote turns argv into a single shell-safe string for the remote.
// SSH executes the command via the user's login shell, so we have to
// reconstruct a shell-quoted line.
func shellQuote(argv []string) string {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(singleQuote(a))
	}
	return b.String()
}

func singleQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !needsQuote(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func needsQuote(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '/', r == ':', r == '=', r == '@', r == '+', r == ',':
			continue
		}
		return true
	}
	return false
}
