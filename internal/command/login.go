package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/transport"
)

// Login runs `drush user:login` on the alias and opens the resulting
// one-time login URL in the local browser. Extra args (e.g. --name=admin,
// --uri=…, a destination path) are forwarded to drush.
func Login(ctx context.Context, a *alias.Alias, args []string, out io.Writer) error {
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

	cmd := bin.Argv(append([]string{"user:login"}, args...))
	var stdout, stderr bytes.Buffer
	pipeErr := t.Pipe(ctx, cmd, nil, &stdout)

	url := extractLoginURL(stdout.String())
	if url == "" {
		if pipeErr != nil {
			return fmt.Errorf("drush user:login: %w\noutput:\n%s%s", pipeErr, stdout.String(), stderr.String())
		}
		return fmt.Errorf("drush user:login produced no URL\noutput:\n%s", stdout.String())
	}
	if pipeErr != nil {
		// drush printed a URL but exited non-zero — surface the warning but
		// still try to open the URL.
		fmt.Fprintf(out, "warning: drush user:login exited with error: %v\n", pipeErr)
	}

	fmt.Fprintln(out, url)
	if err := openBrowser(url); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// loginURLRE matches a URL in drush user:login output. Drush prints the
// one-time-login URL on its own line; we accept it anywhere in stdout to
// tolerate prefix text from older drush versions.
var loginURLRE = regexp.MustCompile(`https?://[^\s'"<>]+`)

func extractLoginURL(s string) string {
	return loginURLRE.FindString(s)
}

// openBrowser is a package var so tests can stub it. The default uses
// the OS's URL handler.
var openBrowser = openBrowserDefault

func openBrowserDefault(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
		args = []string{url}
	case "windows":
		name = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		// linux, freebsd, openbsd, netbsd — xdg-open is the de-facto
		// portable launcher.
		name = "xdg-open"
		args = []string{url}
	}
	c := exec.Command(name, args...)
	return c.Start()
}
