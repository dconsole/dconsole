package command

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
	_ "github.com/dconsole/dconsole/internal/transport"
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

// Real drush 8 `drush help` output fixture (trimmed). Contains the
// shape we need to parse: prologue, global options, category headers,
// columnar commands with paren-wrapped aliases spanning multiple lines,
// and trailing whitespace on the right column.
const drush8HelpFixture = `Execute a drush command. Run ` + "`" + `drush help [command]` + "`" + ` to view command-specific
help.  Run ` + "`" + `drush topic` + "`" + ` to read even more documentation.

Global options (see ` + "`" + `drush topic core-global-options` + "`" + ` for the full list):
 -d, --debug                               Display even more information,
                                           including internal messages.
 -h, --help                                This help system.

Core Drush commands: (core)
 archive-dump (ard,    Backup your code, files, and database into a single
 archive-backup, arb,  file.
 archive:dump)
 archive-restore       Expand a site archive into a Drupal web site.
 (arr,
 archive:restore)
 core-status (status,  Provides a birds-eye view of the current Drupal
 st, core:status)      installation, if any.

Cache commands: (cache)
 cache-clear (cc,      Clear a specific cache, or all drupal caches.
 cache:clear)
 cache-get (cg,        Fetch a cached object and display it.
 cache:get)

SQL commands: (sql)
 sql-cli (sqlc,        Open a SQL command-line interface using Drupal's
 sql:cli)              credentials.
 sql-dump (sql:dump)   Exports the Drupal DB as SQL using mysqldump.
`

func TestParseDrush8Help_ExtractsCommandsAndCategories(t *testing.T) {
	help, ok := parseDrush8Help(drush8HelpFixture)
	if !ok {
		t.Fatal("parser returned ok=false on valid drush 8 output")
	}

	// Verify each expected (name, category, description-prefix) tuple.
	want := []struct {
		name, category, descPrefix string
	}{
		{"archive-dump", "core", "Backup your code"},
		{"archive-restore", "core", "Expand a site archive"},
		{"core-status", "core", "Provides a birds-eye view"},
		{"cache-clear", "cache", "Clear a specific cache"},
		{"cache-get", "cache", "Fetch a cached object"},
		{"sql-cli", "sql", "Open a SQL command-line interface"},
		{"sql-dump", "sql", "Exports the Drupal DB"},
	}

	cmdByName := map[string]string{}
	for _, c := range help.Commands {
		cmdByName[c.Name] = c.Description
	}
	categoryByCmd := map[string]string{}
	for _, ns := range help.Namespaces {
		for _, cmd := range ns.Commands {
			categoryByCmd[cmd] = ns.ID
		}
	}

	for _, w := range want {
		desc, found := cmdByName[w.name]
		if !found {
			t.Errorf("missing command %q (have: %v)", w.name, mapKeys(cmdByName))
			continue
		}
		if !strings.HasPrefix(desc, w.descPrefix) {
			t.Errorf("command %q description = %q, want prefix %q", w.name, desc, w.descPrefix)
		}
		if got := categoryByCmd[w.name]; got != w.category {
			t.Errorf("command %q category = %q, want %q", w.name, got, w.category)
		}
	}

	// Make sure no "global option" lines leaked into commands.
	for n := range cmdByName {
		if strings.HasPrefix(n, "-") {
			t.Errorf("global option leaked as command: %q", n)
		}
	}
	// And no alias continuation lines became phantom commands.
	if _, found := cmdByName["arr"]; found {
		t.Errorf("alias 'arr' leaked as a command (probably the alias-continuation tracker failed)")
	}
}

func TestParseDrush8Help_RejectsNonDrushOutput(t *testing.T) {
	cases := []string{
		"",
		"not drush help",
		"Just some random output\nwith no category headers",
	}
	for _, in := range cases {
		if _, ok := parseDrush8Help(in); ok {
			t.Errorf("parser accepted invalid input %q", in)
		}
	}
}

// TestHelp_FallsBackToDrush8 confirms that when drush list --format=json
// fails but `drush help` succeeds with parseable text, the parser kicks
// in and the user gets a real merged listing instead of static fallback.
func TestHelp_FallsBackToDrush8(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-drush8")
	body := "#!/bin/sh\n" +
		`if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then` + "\n" +
		`  echo "drush: list: command not found" >&2; exit 1` + "\n" +
		`fi` + "\n" +
		`if [ "$1" = "help" ]; then` + "\n" +
		`  cat <<'TXT'` + "\n" +
		drush8HelpFixture + "\n" +
		`TXT` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`echo "unexpected: $@" >&2; exit 1` + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &alias.Alias{
		Site: "demo", Env: "prod",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	if err := Help(context.Background(), a, &out, func(w io.Writer) {
		w.Write([]byte("FALLBACK\n"))
	}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "FALLBACK") {
		t.Errorf("expected drush 8 plain-text parser to handle this, got fallback:\n%s", got)
	}
	for _, needle := range []string{
		"archive-dump",
		"cache-clear",
		"sql-dump",
		" core:",  // namespace section header (drush 8 category)
		" cache:", // ditto
		" sql:",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("merged output missing %q:\n%s", needle, got)
		}
	}
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
