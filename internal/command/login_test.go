package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/provider"
	_ "github.com/heydon/dconsole/internal/provider"
	_ "github.com/heydon/dconsole/internal/transport"
)

// TestLogin_RoutesToProvider verifies that `dconsole @alias login`
// finds the alias's provider and invokes its Login method via the
// LoginCapable interface.
func TestLogin_RoutesToProvider(t *testing.T) {
	dir := t.TempDir()

	// Fake iron binary that records its argv to a marker file so we can
	// assert it was invoked with the `login` subcommand.
	marker := filepath.Join(dir, "iron-called")
	fakeIron := filepath.Join(dir, "iron")
	body := "#!/bin/sh\necho \"$@\" > " + marker + "\necho 'logged in (fake)'\n"
	if err := os.WriteFile(fakeIron, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// Put the fake on PATH so the ironstar provider's exec.Command("iron",
	// ...) picks it up.
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	// Redirect login I/O to a buffer so we don't grab the real TTY.
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	outFile, err := os.CreateTemp(dir, "login-out-*")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	restore := provider.SetLoginIOForTest(devnull, outFile, outFile)
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "prod",
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
		Provider: alias.Provider{
			Type: "ironstar",
			Ironstar: &alias.IronstarProvider{
				Subscription: "example-prod",
				Environment:  "production",
			},
		},
	}

	var dconsoleOut bytes.Buffer
	if err := Login(context.Background(), a, &dconsoleOut); err != nil {
		t.Fatalf("Login: %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("fake iron was not invoked: %v", err)
	}
	if string(got) != "login\n" {
		t.Errorf("iron called with %q; want \"login\\n\"", string(got))
	}
	if !bytes.Contains(dconsoleOut.Bytes(), []byte("logging in via provider ironstar")) {
		t.Errorf("dconsole output missing provider announcement; got:\n%s", dconsoleOut.String())
	}
}

// TestLogin_NoOpWhenNoLoginCapable confirms a clean no-op message when
// neither the transport nor provider implement LoginCapable.
func TestLogin_NoOpWhenNoLoginCapable(t *testing.T) {
	a := &alias.Alias{
		Site: "ex", Env: "dev",
		Transport: alias.Transport{Type: "exec", Exec: &alias.ExecTransport{}},
	}
	var out bytes.Buffer
	if err := Login(context.Background(), a, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("no login step")) {
		t.Errorf("expected no-op message; got:\n%s", out.String())
	}
}
