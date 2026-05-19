package command

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
	_ "github.com/heydon/dconsole/internal/transport"
)

const fakeDrushListJSON = `{
  "application": {"name": "Drush Commandline Tool", "version": "12.4.3"},
  "commands": [
    {"name": "list",         "description": "List commands.",            "hidden": false},
    {"name": "help",         "description": "Display help for a command.", "hidden": false},
    {"name": "cache:clear",  "description": "Clear caches.",             "hidden": false},
    {"name": "cache:rebuild","description": "Rebuild a Drupal site.",    "hidden": false},
    {"name": "sql:dump",     "description": "Export the Drupal DB.",     "hidden": false},
    {"name": "sql:sync",     "description": "Drush native sql:sync.",    "hidden": false},
    {"name": "user:login",   "description": "Display a one-time login URL.", "hidden": false}
  ],
  "namespaces": [
    {"id": "_global", "commands": ["help", "list"]},
    {"id": "cache",   "commands": ["cache:clear", "cache:rebuild"]},
    {"id": "sql",     "commands": ["sql:dump", "sql:sync"]},
    {"id": "user",    "commands": ["user:login"]}
  ]
}`

// fakeDrushBin writes a shell script that emits the supplied JSON when
// called with `list --format=json` (matching what dconsole asks for).
// Returns the path to the script.
func fakeDrushBin(t *testing.T, dir, json string) string {
	t.Helper()
	bin := filepath.Join(dir, "fake-drush")
	body := "#!/bin/sh\n" +
		`if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then` + "\n" +
		`  cat <<'JSON'` + "\n" +
		json + "\n" +
		`JSON` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`echo "unexpected: $@" >&2; exit 1` + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestHelp_MergesDrushAndDconsoleCommands(t *testing.T) {
	dir := t.TempDir()
	bin := fakeDrushBin(t, dir, fakeDrushListJSON)

	a := &alias.Alias{
		Site: "demo", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	fallbackCalled := false
	err := Help(context.Background(), a, &out, func(w io.Writer) { fallbackCalled = true })
	_ = err
	if fallbackCalled {
		t.Fatalf("expected merged help, got fallback. output:\n%s", out.String())
	}

	got := out.String()
	checks := []struct {
		needle string
		why    string
	}{
		{"dconsole — proxy to Drush Commandline Tool 12.4.3 on @demo.dev", "header includes drush version"},
		{"cache:clear", "drush command listed"},
		{"cache:rebuild", "drush command listed"},
		{"sql:dump", "drush sql:dump listed"},
		{"user:login", "drush user:login listed (drush owns the entry, dconsole intercepts via `login`)"},
		// dconsole built-ins:
		{"site:alias", "dconsole built-in listed"},
		{"project:list", "dconsole project built-in listed"},
		{"login ", "dconsole login built-in present (with trailing space to not match user:login)"},
		{"auth ", "dconsole auth built-in present"},
		{"[dconsole]", "dconsole entries are marked"},
		// Namespace headers:
		{" cache:", "drush cache namespace header"},
		{" sql:", "sql namespace header"},
		{" project:", "dconsole project namespace header"},
		{"Available commands:", "global section header"},
	}
	// Skipped: " user:" since drush owns that namespace and the suffix
	// stripping (Login dconsole built-in is global, not user:login).
	for _, c := range checks {
		if !strings.Contains(got, c.needle) {
			t.Errorf("missing %q (%s) in output:\n%s", c.needle, c.why, got)
			break
		}
	}
}

func TestHelp_DconsoleWinsOnNameClash(t *testing.T) {
	// fakeDrushListJSON includes sql:sync — dconsole also intercepts
	// sql:sync. The merged output should show dconsole's description and
	// the [dconsole] marker, not drush's "Drush native sql:sync."
	dir := t.TempDir()
	bin := fakeDrushBin(t, dir, fakeDrushListJSON)
	a := &alias.Alias{
		Site: "demo", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	if err := Help(context.Background(), a, &out, func(w io.Writer) {}); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	if strings.Contains(got, "Drush native sql:sync.") {
		t.Errorf("drush description for sql:sync leaked through; should be replaced by dconsole's. Output:\n%s", got)
	}
	// dconsole's sql:sync line should be the visible one — confirm marker.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "sql:sync ") || strings.Contains(line, "sql:sync\t") {
			if !strings.Contains(line, "[dconsole]") {
				t.Errorf("sql:sync line missing [dconsole] marker: %q", line)
			}
		}
	}
}

func TestHelp_FallsBackOnBadJSON(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-drush")
	body := "#!/bin/sh\necho 'this is not json'\nexit 0\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{
		Site: "demo", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	called := false
	var out bytes.Buffer
	if err := Help(context.Background(), a, &out, func(w io.Writer) {
		called = true
		w.Write([]byte("FALLBACK\n"))
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected fallback to be invoked on bad JSON")
	}
	if !strings.Contains(out.String(), "FALLBACK") {
		t.Errorf("fallback output not propagated: %q", out.String())
	}
}

func TestHelp_FallsBackOnNilAlias(t *testing.T) {
	called := false
	var out bytes.Buffer
	if err := Help(context.Background(), nil, &out, func(w io.Writer) { called = true }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected fallback when alias is nil")
	}
}

func TestHelp_TolerantOfPrefixGarbage(t *testing.T) {
	// Some drush configurations print deprecation warnings before JSON.
	noisy := "PHP Deprecated: foo\n[notice] something\n" + fakeDrushListJSON
	dir := t.TempDir()
	bin := fakeDrushBin(t, dir, noisy)
	// fakeDrushBin emits via heredoc so the leading lines from `noisy`
	// land before the JSON object on stdout — exactly the scenario we
	// want to tolerate.
	a := &alias.Alias{
		Site: "demo", Env: "dev",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	called := false
	var out bytes.Buffer
	if err := Help(context.Background(), a, &out, func(w io.Writer) { called = true }); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Errorf("expected merge despite prefix garbage; got fallback. output:\n%s", out.String())
	}
}
