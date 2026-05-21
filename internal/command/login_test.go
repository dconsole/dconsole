package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	_ "github.com/dconsole/dconsole/internal/transport"
)

func TestExtractLoginURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "https://example.com/user/reset/1/abc\n", "https://example.com/user/reset/1/abc"},
		{"http", "http://localhost:8080/user/reset/2/xyz", "http://localhost:8080/user/reset/2/xyz"},
		{"prefix text", "[notice] Created one-time login link:\nhttps://example.com/user/reset/1/abc\n", "https://example.com/user/reset/1/abc"},
		{"with query", "https://example.com/user/reset/1/abc?destination=/node/42\n", "https://example.com/user/reset/1/abc?destination=/node/42"},
		{"no url", "Something went wrong\n", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractLoginURL(c.in); got != c.want {
				t.Errorf("extractLoginURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestLogin_OpensExtractedURL builds a fake drush bin that prints a uli
// URL, stubs the browser opener, and asserts the URL flowed through.
func TestLogin_OpensExtractedURL(t *testing.T) {
	dir := t.TempDir()
	wantURL := "https://example.com/user/reset/1/abc/login"

	fakeBin := filepath.Join(dir, "fake-drush")
	script := "#!/bin/sh\n" +
		`if [ "$1" = "user:login" ]; then echo "` + wantURL + `"; exit 0; fi` + "\n" +
		`echo "unexpected: $@" >&2; exit 1` + "\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var openedWith string
	restore := stubOpenBrowser(func(u string) error {
		openedWith = u
		return nil
	})
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	if err := Login(context.Background(), a, nil, &out); err != nil {
		t.Fatalf("Login: %v\noutput:\n%s", err, out.String())
	}
	if openedWith != wantURL {
		t.Errorf("openBrowser called with %q, want %q", openedWith, wantURL)
	}
	if !strings.Contains(out.String(), wantURL) {
		t.Errorf("Login output missing URL; got:\n%s", out.String())
	}
}

// TestLogin_NoURLReturnsError verifies we fail loudly when drush prints
// nothing matchable.
func TestLogin_NoURLReturnsError(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-drush")
	script := "#!/bin/sh\necho 'no link for you'\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := stubOpenBrowser(func(u string) error {
		t.Fatalf("openBrowser should not be called; got %q", u)
		return nil
	})
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	err := Login(context.Background(), a, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when drush emits no URL")
	}
	if !strings.Contains(err.Error(), "no URL") {
		t.Errorf("unexpected error %v", err)
	}
}

// TestLogin_ForwardsExtraArgs ensures dconsole passes through extra args
// (e.g. --name=admin) to drush user:login.
func TestLogin_ForwardsExtraArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")

	fakeBin := filepath.Join(dir, "fake-drush")
	script := "#!/bin/sh\n" +
		`echo "$@" > ` + argsFile + "\n" +
		`echo "https://example.com/user/reset/1/abc/login"` + "\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := stubOpenBrowser(func(string) error { return nil })
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	if err := Login(context.Background(), a, []string{"--name=admin", "/node/42"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "user:login --name=admin /node/42\n"
	if string(got) != want {
		t.Errorf("drush called with %q, want %q", string(got), want)
	}
}

// TestLogin_InjectsAliasURI ensures dconsole passes the alias's URI to
// drush as a global --uri= flag so drush doesn't emit http://default/…
func TestLogin_InjectsAliasURI(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")

	fakeBin := filepath.Join(dir, "fake-drush")
	script := "#!/bin/sh\n" +
		`echo "$@" > ` + argsFile + "\n" +
		`echo "https://app.example.com/user/reset/1/abc/login"` + "\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := stubOpenBrowser(func(string) error { return nil })
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "dev",
		URI:       "https://app.example.com",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	if err := Login(context.Background(), a, nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "--uri=https://app.example.com user:login\n"
	if string(got) != want {
		t.Errorf("drush called with %q, want %q", string(got), want)
	}
}

// TestLogin_UserURIWins confirms a caller-supplied --uri is preserved
// and dconsole does not add its own (no duplicate flags).
func TestLogin_UserURIWins(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fakeBin := filepath.Join(dir, "fake-drush")
	script := "#!/bin/sh\n" +
		`echo "$@" > ` + argsFile + "\n" +
		`echo "https://override.example.com/user/reset/1/abc/login"` + "\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := stubOpenBrowser(func(string) error { return nil })
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "dev",
		URI:       "https://app.example.com",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	if err := Login(context.Background(), a, []string{"--uri=https://override.example.com"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "user:login --uri=https://override.example.com\n"
	if string(got) != want {
		t.Errorf("drush called with %q, want %q", string(got), want)
	}
}

// TestLogin_NoURIWarns confirms we proceed but emit a warning when the
// alias has no URI configured.
func TestLogin_NoURIWarns(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-drush")
	script := "#!/bin/sh\n" +
		`echo "http://default/user/reset/1/abc/login"` + "\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := stubOpenBrowser(func(string) error { return nil })
	defer restore()

	a := &alias.Alias{
		Site: "ex", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: fakeBin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	if err := Login(context.Background(), a, nil, &out); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !strings.Contains(out.String(), "no uri configured") {
		t.Errorf("expected URI warning; got:\n%s", out.String())
	}
}

func stubOpenBrowser(fn func(string) error) func() {
	prev := openBrowser
	openBrowser = fn
	return func() { openBrowser = prev }
}
