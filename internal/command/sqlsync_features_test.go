package command

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/dbcache"
)

// drushDumpScript writes a fake drush bin to dir that, on `sql:dump
// --gzip [flags]`, emits a gzipped dumpBody to stdout AND appends its
// argv to argvLog. For `sql:cli`, it cat-pipes stdin to importedFile so
// tests can assert the dump made it across.
// drushDumpScript writes a unique fake drush bin per call. binSuffix
// must differ across calls in the same test or a later call will
// overwrite an earlier one (since they share `dir`).
func drushDumpScript(t *testing.T, dir, binSuffix, dumpBody, argvLog, importedFile string) string {
	t.Helper()
	bin := filepath.Join(dir, "fake-drush-"+binSuffix)
	body := "#!/bin/sh\n" +
		`echo "$@" >> ` + shellEscape(argvLog) + "\n" +
		`if [ "$1" = "sql:dump" ]; then` + "\n" +
		`  printf %s ` + shellEscape(dumpBody) + ` | gzip` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`if [ "$1" = "sql:cli" ]; then` + "\n" +
		`  cat > ` + shellEscape(importedFile) + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`for a in "$@"; do` + "\n" +
		`  case "$a" in --database=*|--structure-tables-list=*|--structure-tables-key=*) ;; esac` + "\n" +
		`done` + "\n" +
		`echo "unexpected drush invocation: $@" >&2; exit 1` + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// drushSqlCliCounter writes a drush stub that just consumes stdin (for
// sql:cli) but tracks how many times it was invoked.
func drushSqlCliCounter(t *testing.T, dir, binSuffix, counterFile, importedFile string) string {
	t.Helper()
	bin := filepath.Join(dir, "fake-drush-cli-"+binSuffix)
	body := "#!/bin/sh\n" +
		`echo invoked >> ` + shellEscape(counterFile) + "\n" +
		`if [ "$1" = "sql:cli" ]; then cat > ` + shellEscape(importedFile) + "; exit 0; fi" + "\n" +
		`if [ "$1" = "sql:dump" ]; then` + "\n" +
		`  printf %s "" | gzip` + "\n" + // empty dump, signals it shouldn't have been called
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`echo unexpected drush: $@ >&2; exit 1` + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func newSourceWithFakeDrush(bin string) *alias.Alias {
	return &alias.Alias{
		Site:      "demo",
		Env:       "prod",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
}

func newTargetWithFakeDrush(bin string) *alias.Alias {
	return &alias.Alias{
		Site:      "demo",
		Env:       "local",
		Bin:       alias.RemoteBin{Kind: "drush", Path: bin},
		Transport: alias.NewTransport("exec", nil),
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// ───────── Caching ─────────

func TestSqlSync_CacheMissThenHit(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	const dumpBody = "DROPCREATE 1;"

	argvLog := filepath.Join(dir, "argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", dumpBody, argvLog, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", dumpBody, argvLog, importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)

	// First sync: miss → populates cache.
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatalf("first SqlSync: %v", err)
	}
	first := readLines(t, argvLog)
	dumps := 0
	for _, l := range first {
		if strings.HasPrefix(l, "sql:dump") {
			dumps++
		}
	}
	if dumps != 1 {
		t.Fatalf("first run: expected 1 sql:dump, got %d (argv log: %v)", dumps, first)
	}

	// Second sync against the same source: HIT — no new sql:dump.
	os.Truncate(argvLog, 0)
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatalf("second SqlSync: %v", err)
	}
	second := readLines(t, argvLog)
	for _, l := range second {
		if strings.HasPrefix(l, "sql:dump") {
			t.Errorf("cache should have prevented sql:dump on second run; got argv: %v", second)
			break
		}
	}
}

func TestSqlSync_RefreshInvalidatesCache(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", argvLog, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", argvLog, importedFile)
	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)

	// Populate cache.
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatal(err)
	}
	os.Truncate(argvLog, 0)

	// --refresh: should re-dump.
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	dumps := 0
	for _, l := range readLines(t, argvLog) {
		if strings.HasPrefix(l, "sql:dump") {
			dumps++
		}
	}
	if dumps != 1 {
		t.Errorf("--refresh should re-dump exactly once; got %d sql:dump calls", dumps)
	}
}

func TestSqlSync_NoCacheBypasses(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", argvLog, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", argvLog, importedFile)
	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)

	for i := 0; i < 2; i++ {
		if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{NoCache: true}); err != nil {
			t.Fatalf("SqlSync iteration %d: %v", i, err)
		}
	}
	dumps := 0
	for _, l := range readLines(t, argvLog) {
		if strings.HasPrefix(l, "sql:dump") {
			dumps++
		}
	}
	if dumps != 2 {
		t.Errorf("--no-cache should run sql:dump every time; got %d", dumps)
	}
}

func TestSqlSync_PerAliasTTLExpires(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", argvLog, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", argvLog, importedFile)

	// 1ms TTL — by the time we run a second sync the entry's expired.
	source := newSourceWithFakeDrush(srcDrush)
	source.SQL.Cache.TTL = "1ms"
	target := newTargetWithFakeDrush(tgtDrush)

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	os.Truncate(argvLog, 0)
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatal(err)
	}
	dumps := 0
	for _, l := range readLines(t, argvLog) {
		if strings.HasPrefix(l, "sql:dump") {
			dumps++
		}
	}
	if dumps != 1 {
		t.Errorf("alias TTL=1ms: second sync should re-dump; got %d", dumps)
	}
}

// ───────── Provider SyncTo end-to-end takeover ─────────

func TestSqlSync_ProviderSyncToTakesOver(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()

	// Marker script that the fake provider's SyncToCmd runs. Existence
	// of the marker file proves SyncTo fired; absence of any
	// imported.sql file proves the dump+load chain was skipped.
	marker := filepath.Join(dir, "sync-to-fired")
	script := filepath.Join(dir, "syncto.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	source := &alias.Alias{
		Site:      "demo",
		Env:       "prod",
		Provider:  alias.NewProvider("fake", fakeProviderConfig{SyncToCmd: []string{script}}),
		Transport: alias.NewTransport("exec", nil),
		Bin:       alias.RemoteBin{Kind: "drush", Path: "/does/not/exist"},
	}
	target := newTargetWithFakeDrush("/also/does/not/exist") // would error if dispatched

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("provider.SyncTo should have run (marker missing): %v", err)
	}
}

// ───────── DBImporter (ddev-style) path ─────────

func TestSqlSync_DBImporterPreferredOverDrushSqlCli(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	const dumpBody = "PIPE"

	importerLog := filepath.Join(dir, "importer.log")
	cliCounter := filepath.Join(dir, "cli-counter")
	importedFile := filepath.Join(dir, "imported.sql")

	srcDrush := drushDumpScript(t, dir, "src", dumpBody, filepath.Join(dir, "src-argv"), importedFile)
	tgtDrush := drushSqlCliCounter(t, dir, "tgt", cliCounter, importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	target := &alias.Alias{
		Site: "demo", Env: "local",
		Bin: alias.RemoteBin{Kind: "drush", Path: tgtDrush},
		Transport: alias.NewTransport("fake-importer", map[string]any{
			"log_file": importerLog,
		}),
	}

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v", err)
	}
	if lines := readLines(t, importerLog); len(lines) != 1 {
		t.Errorf("expected 1 ImportDB call, got %d (log: %v)", len(lines), lines)
	}
	// drush sql:cli MUST NOT have been called.
	if cli := readLines(t, cliCounter); len(cli) != 0 {
		t.Errorf("drush sql:cli should not have been called; got %d invocations", len(cli))
	}
}

// ───────── In-flight compression (file strategy) ─────────

func TestSqlSync_FileStrategyCompressesAtSource(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	const sqlBody = "INSERT INTO t VALUES (7);"
	plainPath := filepath.Join(dir, "backup.sql")
	mustWriteSqlSync(t, plainPath, sqlBody)

	// A "remote" transport stub: it's the exec transport but logs
	// whichever command it receives. We compare against `gzip -c <path>`.
	argvLog := filepath.Join(dir, "src-argv")
	wrapperDir := t.TempDir()
	gzipWrapper := filepath.Join(wrapperDir, "gzip")
	if err := os.WriteFile(gzipWrapper, []byte("#!/bin/sh\necho \"gzip $@\" >> "+argvLog+"\nexec /usr/bin/gzip \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	catWrapper := filepath.Join(wrapperDir, "cat")
	if err := os.WriteFile(catWrapper, []byte("#!/bin/sh\necho \"cat $@\" >> "+argvLog+"\nexec /bin/cat \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+":"+os.Getenv("PATH"))

	gzNo := false
	source := &alias.Alias{
		Site: "demo", Env: "prod",
		Root: dir,
		SQL: alias.SQL{Source: alias.SQLSource{
			Type:    "file",
			Path:    plainPath,
			Gzipped: &gzNo,
		}},
		Transport: alias.NewTransport("exec", nil),
		Bin:       alias.RemoteBin{Kind: "drush", Path: "/no"},
	}
	// Target: any drush stub that swallows sql:cli.
	tgtDrush := drushDumpScript(t, dir, "tgt", "noop", filepath.Join(dir, "tgt-argv"), filepath.Join(dir, "tgt-imported"))
	target := newTargetWithFakeDrush(tgtDrush)

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v", err)
	}
	argv := readLines(t, argvLog)
	sawGzip := false
	sawCat := false
	for _, l := range argv {
		if strings.HasPrefix(l, "gzip -c "+plainPath) {
			sawGzip = true
		}
		if strings.HasPrefix(l, "cat "+plainPath) {
			sawCat = true
		}
	}
	if !sawGzip {
		t.Errorf("expected `gzip -c %s` on the wire (in-flight compression); argv log:\n%v", plainPath, argv)
	}
	if sawCat {
		t.Errorf("`cat %s` ran locally — that defeats in-flight compression; argv log:\n%v", plainPath, argv)
	}
}

func mustWriteSqlSync(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ───────── Structure-tables ─────────

func TestSqlSync_StructureTablesFlagsForwardedToDrush(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", argvLog, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt-argv"), importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{
		StructureTables:    []string{"cache", "sessions", "watchdog"},
		StructureTablesKey: "common",
	}); err != nil {
		t.Fatal(err)
	}
	argv := readLines(t, argvLog)
	sawList := false
	sawKey := false
	for _, l := range argv {
		if strings.Contains(l, "--structure-tables-list=cache,sessions,watchdog") {
			sawList = true
		}
		if strings.Contains(l, "--structure-tables-key=common") {
			sawKey = true
		}
	}
	if !sawList {
		t.Errorf("--structure-tables-list= not in drush argv:\n%v", argv)
	}
	if !sawKey {
		t.Errorf("--structure-tables-key= not in drush argv:\n%v", argv)
	}
}

func TestSqlSync_StructureTablesFromAliasConfig(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", argvLog, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt-argv"), importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	source.SQL.Source.StructureTables = []string{"cache_render", "cache_data"}
	target := newTargetWithFakeDrush(tgtDrush)

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if !containsLineWith(readLines(t, argvLog), "--structure-tables-list=cache_render,cache_data") {
		t.Errorf("alias config structure_tables not forwarded; argv: %v", readLines(t, argvLog))
	}
}

func containsLineWith(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// ───────── Cross-DB-key sync ─────────

func TestSqlSync_CrossDBKey_ForwardsFlags(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	tgtArgv := filepath.Join(dir, "tgt-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", tgtArgv, importedFile)
	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{
		SourceDatabase: "default",
		TargetDatabase: "migrate",
	}); err != nil {
		t.Fatal(err)
	}
	// Default source DB is `default` — the orchestrator omits the flag
	// in that case to keep argv minimal. Only the explicit non-default
	// (target=migrate) should appear.
	if containsLineWith(readLines(t, srcArgv), "--database=default") {
		t.Errorf("--database=default should be omitted on source argv: %v", readLines(t, srcArgv))
	}
	if !containsLineWith(readLines(t, tgtArgv), "--database=migrate") {
		t.Errorf("--database=migrate not on target argv: %v", readLines(t, tgtArgv))
	}
}

func TestSqlSync_CrossDBKey_CacheKeysDiffer(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt"), importedFile)
	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)

	// Populate with source-database=default.
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{}); err != nil {
		t.Fatal(err)
	}
	os.Truncate(srcArgv, 0)

	// Run with source-database=migrate — should NOT hit the prior cache.
	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{SourceDatabase: "migrate"}); err != nil {
		t.Fatal(err)
	}
	dumps := 0
	for _, l := range readLines(t, srcArgv) {
		if strings.HasPrefix(l, "sql:dump") {
			dumps++
		}
	}
	if dumps != 1 {
		t.Errorf("different source DB should produce different cache key; expected 1 fresh sql:dump, got %d", dumps)
	}
}

// ───────── Cross-site safety check ─────────

func TestSqlSync_CrossSite_InteractivePromptCorrectName(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	tgtArgv := filepath.Join(dir, "tgt-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", tgtArgv, importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	source.Site = "alpha"
	target := newTargetWithFakeDrush(tgtDrush)
	target.Site = "beta"

	// Typing the correct target site name proceeds.
	var out bytes.Buffer
	if err := SqlSync(context.Background(), source, target, &out, strings.NewReader("beta\n"), SqlSyncOpts{}); err != nil {
		t.Fatalf("SqlSync: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "CROSS-SITE") {
		t.Errorf("expected CROSS-SITE banner in output:\n%s", out.String())
	}
}

func TestSqlSync_CrossSite_WrongNameAborts(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt"), importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	source.Site = "alpha"
	target := newTargetWithFakeDrush(tgtDrush)
	target.Site = "beta"

	err := SqlSync(context.Background(), source, target, io.Discard, strings.NewReader("wrong\n"), SqlSyncOpts{})
	if err == nil {
		t.Fatal("expected abort when wrong site name typed")
	}
	if !strings.Contains(err.Error(), "cross-site confirmation aborted") {
		t.Errorf("unexpected error %q", err.Error())
	}
	// And the source's sql:dump must not have been called.
	if len(readLines(t, srcArgv)) > 0 {
		t.Errorf("dump should not have run after abort; argv: %v", readLines(t, srcArgv))
	}
}

func TestSqlSync_CrossSite_NonInteractiveRequiresFlag(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt"), importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	source.Site = "alpha"
	target := newTargetWithFakeDrush(tgtDrush)
	target.Site = "beta"

	err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{})
	if err == nil {
		t.Fatal("expected non-interactive cross-site to require --confirm-cross-site")
	}
	if !strings.Contains(err.Error(), "--confirm-cross-site") {
		t.Errorf("error message should mention the flag; got %q", err.Error())
	}
}

func TestSqlSync_CrossSite_ConfirmFlagBypasses(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt"), importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	source.Site = "alpha"
	target := newTargetWithFakeDrush(tgtDrush)
	target.Site = "beta"

	if err := SqlSync(context.Background(), source, target, io.Discard, nil, SqlSyncOpts{ConfirmCrossSite: true}); err != nil {
		t.Fatalf("--confirm-cross-site should proceed: %v", err)
	}
}

func TestSqlSync_SameSiteNeverPrompts(t *testing.T) {
	isolateSqlCache(t)
	dir := t.TempDir()
	srcArgv := filepath.Join(dir, "src-argv")
	importedFile := filepath.Join(dir, "imported.sql")
	srcDrush := drushDumpScript(t, dir, "src", "X", srcArgv, importedFile)
	tgtDrush := drushDumpScript(t, dir, "tgt", "X", filepath.Join(dir, "tgt"), importedFile)

	source := newSourceWithFakeDrush(srcDrush)
	target := newTargetWithFakeDrush(tgtDrush)
	// Both Site="demo" by helper default.

	var out bytes.Buffer
	if err := SqlSync(context.Background(), source, target, &out, strings.NewReader("anything\n"), SqlSyncOpts{}); err != nil {
		t.Fatalf("same-site SqlSync: %v", err)
	}
	if strings.Contains(out.String(), "CROSS-SITE") {
		t.Errorf("same-site sync should not show CROSS-SITE banner:\n%s", out.String())
	}
}

// ───────── sql:cache list/clear ─────────

func TestSqlCache_ListAndClear(t *testing.T) {
	isolateSqlCache(t)

	srcPath := filepath.Join(t.TempDir(), "src.sql.gz")
	if err := os.WriteFile(srcPath, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dbcache.Put("alpha-key", srcPath, dbcache.Metadata{Site: "alpha", Env: "prod", Strategy: "drush"}); err != nil {
		t.Fatal(err)
	}
	if _, err := dbcache.Put("beta-key", srcPath, dbcache.Metadata{Site: "beta", Env: "stage", Strategy: "drush"}); err != nil {
		t.Fatal(err)
	}

	// list shows both.
	var out bytes.Buffer
	if err := SqlCacheList(&out); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"@alpha.prod", "@beta.stage", "alpha-key", "beta-key"} {
		if !strings.Contains(out.String(), needle) {
			t.Errorf("list output missing %q:\n%s", needle, out.String())
		}
	}

	// clear @alpha.prod removes only that entry.
	out.Reset()
	if err := SqlCacheClear("@alpha.prod", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "removed 1") {
		t.Errorf("clear output should say 'removed 1':\n%s", out.String())
	}
	remaining, err := dbcache.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Meta.Site != "beta" {
		t.Errorf("after clear @alpha.prod, expected only beta to remain; got %v", remaining)
	}

	// clear with no query removes everything.
	out.Reset()
	if err := SqlCacheClear("", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "removed 1") {
		t.Errorf("clear-all should report 'removed 1' (one left): %s", out.String())
	}
	remaining, _ = dbcache.List()
	if len(remaining) != 0 {
		t.Errorf("cache should be empty after clear; got %d entries", len(remaining))
	}
}

func TestSqlCache_ListEmpty(t *testing.T) {
	isolateSqlCache(t)
	var out bytes.Buffer
	if err := SqlCacheList(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no cached dumps") {
		t.Errorf("empty cache should report 'no cached dumps':\n%s", out.String())
	}
}

func TestSqlCache_ClearInvalidQuery(t *testing.T) {
	isolateSqlCache(t)
	err := SqlCacheClear("not-an-alias", io.Discard)
	if err == nil {
		t.Fatal("expected error for invalid query")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error should mention usage: %q", err.Error())
	}
}
