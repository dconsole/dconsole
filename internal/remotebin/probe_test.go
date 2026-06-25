package remotebin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
)

type fakeProber struct {
	calls []string
	respond func(cmd []string) ([]byte, error)
}

func (f *fakeProber) Run(ctx context.Context, cmd []string) ([]byte, error) {
	f.calls = append(f.calls, cmd[0])
	return f.respond(cmd)
}

func TestProbePrefersDrupalWhenBothPresent(t *testing.T) {
	withTempCache(t)
	a := &alias.Alias{
		Site: "x", Env: "dev", Root: "/var/www/html",
		Bin: alias.RemoteBin{Kind: KindAuto},
	}
	p := &fakeProber{respond: func(cmd []string) ([]byte, error) {
		// Both binaries exist and respond with a fake version line.
		return []byte("Drupal CLI 12.0\n"), nil
	}}
	r, err := Probe(context.Background(), a, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindDrupal {
		t.Errorf("kind = %q, want %q", r.Kind, KindDrupal)
	}
	// Sibling-vendor is the modern composer layout default.
	if r.Path != "/var/www/vendor/bin/drupal" {
		t.Errorf("path = %q (expected sibling-of-root vendor)", r.Path)
	}
	if got := len(p.calls); got != 1 {
		t.Errorf("expected one probe call (drupal succeeded first), got %d (%v)", got, p.calls)
	}
}

func TestProbeFallsBackToDrush(t *testing.T) {
	withTempCache(t)
	a := &alias.Alias{
		Site: "x", Env: "dev", Root: "/var/www/html",
		Bin: alias.RemoteBin{Kind: KindAuto},
	}
	p := &fakeProber{respond: func(cmd []string) ([]byte, error) {
		if filepath.Base(cmd[0]) == "drupal" {
			return nil, errors.New("not found")
		}
		return []byte("Drush 13.0.0\n"), nil
	}}
	r, err := Probe(context.Background(), a, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindDrush {
		t.Errorf("kind = %q, want %q", r.Kind, KindDrush)
	}
	if len(p.calls) < 2 {
		t.Errorf("expected at least 2 probe calls when drupal failed, got %d", len(p.calls))
	}
}

func TestProbeAllFail(t *testing.T) {
	withTempCache(t)
	a := &alias.Alias{
		Site: "x", Env: "dev", Root: "/var/www/html",
		Bin: alias.RemoteBin{Kind: KindAuto},
	}
	p := &fakeProber{respond: func(cmd []string) ([]byte, error) {
		return nil, errors.New("not found")
	}}
	if _, err := Probe(context.Background(), a, p); err == nil {
		t.Error("expected error when no candidate succeeds")
	}
}

// TestProbeFailMessageFormat — when probing fails, the error must be
// the new multi-line format (one path per line, deduped, with a short
// classification per attempt). Regression guard for the previous
// bracketed-blob format that surfaced as a 20-line wall of fork/exec
// noise on synthetic local aliases.
func TestProbeFailMessageFormat(t *testing.T) {
	withTempCache(t)
	a := &alias.Alias{
		Site: "x", Env: "dev", Root: "/var/www/html",
		Bin: alias.RemoteBin{Kind: KindAuto},
	}
	p := &fakeProber{respond: func(cmd []string) ([]byte, error) {
		return nil, errors.New("fork/exec " + cmd[0] + ": no such file or directory")
	}}
	_, err := Probe(context.Background(), a, p)
	if err == nil {
		t.Fatal("expected probe failure")
	}
	msg := err.Error()
	// Header
	if !strings.Contains(msg, "no Drupal CLI") {
		t.Errorf("expected 'no Drupal CLI' header; got: %s", msg)
	}
	// Per-path lines collapse the raw fork/exec text to "no such file"
	if !strings.Contains(msg, "no such file") {
		t.Errorf("expected classified 'no such file'; got: %s", msg)
	}
	if strings.Contains(msg, "fork/exec") {
		t.Errorf("classifier should hide raw 'fork/exec' string; got: %s", msg)
	}
	// Remote alias (Site=x.dev) should NOT get the local-only "cd into"
	// hint — that's specific to @self.local
	if strings.Contains(msg, "cd into a Drupal project") {
		t.Errorf("remote alias must not receive @self.local hint; got: %s", msg)
	}
}

// TestProbeFailLocalHint — the synthetic @self.local case (user ran
// dconsole from a directory with no project) gets the "Fixes:" block
// pointing at cd/remote/global-install.
func TestProbeFailLocalHint(t *testing.T) {
	withTempCache(t)
	a := &alias.Alias{
		Site: "self", Env: "local", Root: "/tmp",
		Bin: alias.RemoteBin{Kind: KindAuto},
	}
	p := &fakeProber{respond: func(cmd []string) ([]byte, error) {
		return nil, errors.New("no such file or directory")
	}}
	_, err := Probe(context.Background(), a, p)
	if err == nil {
		t.Fatal("expected probe failure")
	}
	for _, want := range []string{"Fixes:", "cd into a Drupal project", "@site.env", "composer global require drush/drush"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("local hint missing %q in: %s", want, err.Error())
		}
	}
}

func TestProbeCacheReusesResult(t *testing.T) {
	withTempCache(t)
	a := &alias.Alias{
		Site: "x", Env: "dev", Root: "/var/www/html",
		Bin: alias.RemoteBin{Kind: KindAuto},
	}
	p := &fakeProber{respond: func(cmd []string) ([]byte, error) {
		return []byte("Drupal CLI 12.0\n"), nil
	}}
	if _, err := Probe(context.Background(), a, p); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), a, p); err != nil {
		t.Fatal(err)
	}
	// First Probe should hit the prober; second should be cached.
	if len(p.calls) != 1 {
		t.Errorf("cache miss: prober called %d times, want 1", len(p.calls))
	}
}

func withTempCache(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	old, _ := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatal(err)
	}
	memoCache = &cache{}
	t.Cleanup(func() {
		os.Setenv("HOME", old)
		memoCache = &cache{}
	})
}
