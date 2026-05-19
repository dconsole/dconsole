package transport

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/heydon/dconsole/internal/alias"
	"gopkg.in/yaml.v3"
)

// buildEchoBin compiles examples/dconsole-echo once per test binary
// into a temp dir and returns the directory. Subsequent tests reuse it.
var (
	echoBuildOnce sync.Once
	echoBuildDir  string
	echoBuildErr  error
)

func buildEchoBin(t *testing.T) string {
	t.Helper()
	echoBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dconsole-echo-build-")
		if err != nil {
			echoBuildErr = err
			return
		}
		echoBuildDir = dir
		// Walk up from this test file to find the repo root, then build.
		cwd, _ := os.Getwd()
		// internal/transport/ → repo root is two levels up.
		repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "dconsole-echo"), "./examples/dconsole-echo")
		cmd.Dir = repoRoot
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			echoBuildErr = err
			t.Logf("go build stderr:\n%s", stderr.String())
		}
	})
	if echoBuildErr != nil {
		t.Skipf("failed to build dconsole-echo (skipping): %v", echoBuildErr)
	}
	return echoBuildDir
}

// installEchoForTest points the plugin discovery layer at a temp dir
// containing the freshly-built echo binary, isolated from the user's
// real ~/.dconsole/plugins/.
func installEchoForTest(t *testing.T) {
	t.Helper()
	binDir := buildEchoBin(t)
	pluginsDir := t.TempDir()
	src := filepath.Join(binDir, "dconsole-echo")
	dst := filepath.Join(pluginsDir, "dconsole-echo")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy echo bin: %v", err)
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Dir(pluginsDir))
	// Move dconsole/plugins under XDG_DATA_HOME, since PluginDir
	// returns $XDG_DATA_HOME/dconsole/plugins. Mimic that layout:
	relocated := filepath.Join(filepath.Dir(pluginsDir), "dconsole", "plugins")
	if err := os.MkdirAll(relocated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dst, filepath.Join(relocated, "dconsole-echo")); err != nil {
		t.Fatal(err)
	}
	ResetPluginCacheForTests()
	ResetPluginInfoCacheForTests()
}

func copyFile(src, dst string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o755)
}

func echoAlias(t *testing.T) *alias.Alias {
	t.Helper()
	// Build an alias with an `echo` transport whose Raw mapping is
	// non-empty — mimics what yaml.Unmarshal would produce.
	yamlBody := []byte(`
type: echo
echo:
  tag: test
`)
	var tr alias.Transport
	if err := yaml.Unmarshal(yamlBody, &tr); err != nil {
		t.Fatal(err)
	}
	return &alias.Alias{
		Site:      "demo",
		Env:       "test",
		URI:       "https://demo.test",
		Root:      "/var/www/demo",
		Bin:       alias.RemoteBin{Kind: "drush", Path: "drush"},
		Transport: tr,
	}
}

func TestSubprocess_PluginInfoAndDispatch(t *testing.T) {
	installEchoForTest(t)
	a := echoAlias(t)

	tr, err := For(a)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if tr.Name() != "echo" {
		t.Errorf("Name = %q, want echo", tr.Name())
	}
	if err := tr.Available(); err != nil {
		t.Errorf("Available: %v", err)
	}
}

func TestSubprocess_ExecStreamsStdio(t *testing.T) {
	installEchoForTest(t)
	a := echoAlias(t)
	tr, err := For(a)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	stdio := Stdio{In: strings.NewReader("hello plugin\n"), Out: &stdout, Err: &stderr}
	if err := tr.Exec(context.Background(), []string{"drush", "status"}, stdio); err != nil {
		t.Fatalf("Exec: %v\nstderr:\n%s", err, stderr.String())
	}
	if stdout.String() != "hello plugin\n" {
		t.Errorf("stdout = %q, want streaming pass-through", stdout.String())
	}
	if !strings.Contains(stderr.String(), "verb=exec") || !strings.Contains(stderr.String(), "remoteCmd=[drush status]") {
		t.Errorf("stderr missing expected verb/remoteCmd hints:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "alias=@demo.test") {
		t.Errorf("stderr missing alias envelope info:\n%s", stderr.String())
	}
}

func TestSubprocess_PipeStreamsStdio(t *testing.T) {
	installEchoForTest(t)
	a := echoAlias(t)
	tr, err := For(a)
	if err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader("PIPE PAYLOAD\n")
	var out bytes.Buffer
	if err := tr.Pipe(context.Background(), []string{"sql:dump"}, in, &out); err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if out.String() != "PIPE PAYLOAD\n" {
		t.Errorf("Pipe stdout = %q, want passthrough", out.String())
	}
}

func TestSubprocess_PreviewReturnsPluginArgv(t *testing.T) {
	installEchoForTest(t)
	a := echoAlias(t)
	tr, err := For(a)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Preview([]string{"drush", "cr"})
	want := []string{"echo:", "drush", "cr"}
	if !equalStrings(got, want) {
		t.Errorf("Preview = %v, want %v", got, want)
	}
}

func TestSubprocess_ShellUnsupportedReturnsClearError(t *testing.T) {
	installEchoForTest(t)
	a := echoAlias(t)
	tr, err := For(a)
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Shell(context.Background(), "/tmp")
	if err == nil {
		t.Fatal("expected error for unsupported shell")
	}
	if !strings.Contains(err.Error(), "does not support `shell`") {
		t.Errorf("error message %q doesn't mention unsupported shell", err.Error())
	}
}

func TestSubprocess_UnknownPluginReturnsClearError(t *testing.T) {
	// No echo install — empty plugin dir, empty PATH for dconsole-foo.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Drop a PATH that doesn't have any dconsole-* binaries.
	t.Setenv("PATH", t.TempDir())
	ResetPluginCacheForTests()
	ResetPluginInfoCacheForTests()

	var tr alias.Transport
	if err := yaml.Unmarshal([]byte("type: nonexistent-plugin\n"), &tr); err != nil {
		t.Fatal(err)
	}
	a := &alias.Alias{Site: "x", Env: "y", Transport: tr}
	_, err := For(a)
	if err == nil {
		t.Fatal("expected error for missing plugin")
	}
	if !strings.Contains(err.Error(), "nonexistent-plugin") {
		t.Errorf("error %q should mention the type", err.Error())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
