package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
	_ "github.com/heydon/dconsole/internal/provider"
	_ "github.com/heydon/dconsole/internal/transport"
)

// TestAuth_RoutesToProvider verifies that `dconsole @alias auth`
// finds the alias's provider and invokes its Login method via the
// LoginCapable duck-typed interface. Uses the test-only `fake`
// provider (provider_fake_test.go) which exec's a configurable
// LoginCmd; the test plants a shell script there that records its
// argv to a marker file.
func TestAuth_RoutesToProvider(t *testing.T) {
	dir := t.TempDir()

	marker := filepath.Join(dir, "login-called")
	loginScript := filepath.Join(dir, "login.sh")
	body := "#!/bin/sh\necho \"$@\" > " + marker + "\n"
	if err := os.WriteFile(loginScript, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &alias.Alias{
		Site: "ex", Env: "prod",
		Transport: alias.NewTransport("exec", nil),
		Provider: alias.NewProvider("fake", fakeProviderConfig{
			LoginCmd: []string{loginScript, "login"},
		}),
	}

	var out bytes.Buffer
	if err := Auth(context.Background(), a, &out); err != nil {
		t.Fatalf("Auth: %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("login script was not invoked: %v", err)
	}
	if string(got) != "login\n" {
		t.Errorf("script called with %q; want \"login\\n\"", string(got))
	}
	if !bytes.Contains(out.Bytes(), []byte("logging in via provider fake")) {
		t.Errorf("dconsole output missing provider announcement; got:\n%s", out.String())
	}
}

// TestAuth_NoOpWhenNoLoginCapable confirms a clean no-op message when
// neither the transport nor provider implement LoginCapable.
func TestAuth_NoOpWhenNoLoginCapable(t *testing.T) {
	a := &alias.Alias{
		Site: "ex", Env: "dev",
		Transport: alias.NewTransport("exec", nil),
	}
	var out bytes.Buffer
	if err := Auth(context.Background(), a, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("no login step")) {
		t.Errorf("expected no-op message; got:\n%s", out.String())
	}
}
