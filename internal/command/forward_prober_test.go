package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/pkg/transport"
)

// proberFakeTransport records the stdio handed to Exec and emits the
// configured stdout/stderr/exit. Used to prove transportProber doesn't
// leak the probe-candidate stderr to the user's terminal.
type proberFakeTransport struct {
	stdout  string
	stderr  string
	exitErr error
	sawErr  io.Writer // captured stdio.Err from the most recent Exec
}

func (f *proberFakeTransport) Name() string     { return "prober-fake" }
func (f *proberFakeTransport) Available() error { return nil }

func (f *proberFakeTransport) Exec(ctx context.Context, cmd []string, stdio transport.Stdio) error {
	f.sawErr = stdio.Err
	if f.stdout != "" && stdio.Out != nil {
		_, _ = stdio.Out.Write([]byte(f.stdout))
	}
	if f.stderr != "" && stdio.Err != nil {
		_, _ = stdio.Err.Write([]byte(f.stderr))
	}
	return f.exitErr
}

// Pipe should NEVER be called by the prober — Pipe hardcodes
// cmd.Stderr = os.Stderr, which is the bug we're guarding against.
// If anyone routes the prober through Pipe again, this test fails loudly.
func (f *proberFakeTransport) Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error {
	return fmt.Errorf("transportProber.Run must not use Pipe (leaks remote stderr to user's terminal)")
}
func (f *proberFakeTransport) Shell(ctx context.Context, workDir string) error { return nil }
func (f *proberFakeTransport) Preview(cmd []string) []string                   { return cmd }
func (f *proberFakeTransport) Wrap(inner []string) []string                    { return inner }

func TestTransportProberSuppressesStderrOnSuccess(t *testing.T) {
	f := &proberFakeTransport{stdout: "Drush 13.0\n"}
	p := &transportProber{h: f}

	out, err := p.Run(context.Background(), []string{"drush", "--version"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if string(out) != "Drush 13.0\n" {
		t.Errorf("stdout = %q, want %q", out, "Drush 13.0\n")
	}
	if f.sawErr == nil {
		t.Errorf("stdio.Err was nil — probe wouldn't have captured any stderr")
	}
	if f.sawErr == os.Stderr {
		t.Errorf("stdio.Err pointed at os.Stderr — that's the leak the fix is supposed to prevent")
	}
}

func TestTransportProberCapturesStderrIntoError(t *testing.T) {
	// Simulates the typical "drupal: command not found" path that bit
	// the Heydon Consulting site (drush-only remote, drupal probed first).
	f := &proberFakeTransport{
		stderr:  "bash: line 1: drupal: command not found\n",
		exitErr: errors.New("exit status 127"),
	}
	p := &transportProber{h: f}

	_, err := p.Run(context.Background(), []string{"drupal", "--version"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "command not found") {
		t.Errorf("expected stderr to be wrapped into the returned error, got %v", err)
	}
	// And — critically — the user's terminal saw nothing. (The stderr
	// went to the prober's internal buffer, not os.Stderr.)
	if f.sawErr == os.Stderr {
		t.Errorf("stdio.Err pointed at os.Stderr — leak regression")
	}
}
