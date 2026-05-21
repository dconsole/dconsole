package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/dlog"
	"github.com/dconsole/dconsole/internal/transport"
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

	// Drush needs --uri to know which URL to embed in the one-time-login
	// link; without it, drush falls back to http://default/… which fails
	// in the browser. Prepend it as a global option unless the caller
	// already passed their own --uri.
	drushArgs := []string{"user:login"}
	if !hasURIArg(args) {
		if a.URI != "" {
			drushArgs = append([]string{"--uri=" + a.URI}, drushArgs...)
		} else {
			fmt.Fprintf(out, "warning: @%s.%s has no uri configured; drush will emit http://default/…\n", a.Site, a.Env)
		}
	}
	drushArgs = append(drushArgs, args...)
	drushArgs = append(drushArgs, dlog.DrushFlags()...)
	cmd := bin.Argv(drushArgs)
	dlog.Cmdf(t.Preview(cmd))
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

// hasURIArg reports whether the caller already supplied a --uri flag,
// so we don't override their explicit choice.
func hasURIArg(args []string) bool {
	for _, a := range args {
		if a == "--uri" || a == "-l" || strings.HasPrefix(a, "--uri=") || strings.HasPrefix(a, "-l=") {
			return true
		}
	}
	return false
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
