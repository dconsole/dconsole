package command

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/provider"
)

// fakeprovider is a test-only Provider registered at init() so we can
// exercise dconsole's auth + sql:sync routing logic without depending on
// any real hosting integration. All behavior is parameterised through
// the alias's `fake:` config block so each test injects what it needs.
type fakeprovider struct {
	cfg fakeProviderConfig
}

// fakeProviderConfig is the YAML block decoded from `provider.fake:`.
type fakeProviderConfig struct {
	// LoginCmd is what Login exec's, split as argv[0]=bin, [1:]=args.
	// Tests point it at a script that records its invocation so the
	// test can assert the routing fired.
	LoginCmd []string `yaml:"login_cmd"`
	// DumpPath is the local file DumpFor reports as the source dump.
	// Tests pre-create a gzipped SQL file there.
	DumpPath string `yaml:"dump_path"`
	// SyncToCmd, when non-empty, makes SyncTo exec this command (instead
	// of returning ErrNotSupported). Tests use it to assert the
	// end-to-end takeover branch fires and the dump+load chain doesn't.
	SyncToCmd []string `yaml:"sync_to_cmd"`
	// SyncFilesToCmd is the rsync sibling of SyncToCmd. Tests use it to
	// assert the dconsole rsync end-to-end takeover branch fires.
	SyncFilesToCmd []string `yaml:"sync_files_to_cmd"`
	// LoadFilesCmd, when non-empty, runs from LoadFilesFor (--bundle-path
	// is appended). Tests use it to assert provider.LoadFilesFor took
	// priority over the transport / FilesImporter chain.
	LoadFilesCmd []string `yaml:"load_files_cmd"`
}

func init() {
	provider.Register("fake", provider.Registration{
		Build: func(a *alias.Alias) (provider.Provider, error) {
			var w struct {
				Fake fakeProviderConfig `yaml:"fake"`
			}
			if err := a.Provider.Decode(&w); err != nil {
				return nil, fmt.Errorf("provider type fake: %w", err)
			}
			return &fakeprovider{cfg: w.Fake}, nil
		},
	})
}

func (f *fakeprovider) Name() string { return "fake" }

// Login satisfies the LoginCapable duck-typed interface. Runs the
// configured LoginCmd (typically a fake shell script the test wrote)
// so tests can assert Auth's routing actually invoked it.
func (f *fakeprovider) Login(ctx context.Context, a *alias.Alias) error {
	if len(f.cfg.LoginCmd) == 0 {
		return nil
	}
	return exec.CommandContext(ctx, f.cfg.LoginCmd[0], f.cfg.LoginCmd[1:]...).Run()
}

// SyncTo runs SyncToCmd if configured (so tests can assert the
// end-to-end takeover); otherwise returns ErrNotSupported so the
// orchestrator falls through to DumpFor + LoadFor.
func (f *fakeprovider) SyncTo(ctx context.Context, source, target *alias.Alias) error {
	if len(f.cfg.SyncToCmd) == 0 {
		return provider.ErrNotSupported
	}
	return exec.CommandContext(ctx, f.cfg.SyncToCmd[0], f.cfg.SyncToCmd[1:]...).Run()
}

// SyncFilesTo runs SyncFilesToCmd if configured; tests use it to
// confirm the rsync end-to-end takeover branch fires.
func (f *fakeprovider) SyncFilesTo(ctx context.Context, source, target *alias.Alias) error {
	if len(f.cfg.SyncFilesToCmd) == 0 {
		return provider.ErrNotSupported
	}
	return exec.CommandContext(ctx, f.cfg.SyncFilesToCmd[0], f.cfg.SyncFilesToCmd[1:]...).Run()
}

// LoadFilesFor runs LoadFilesCmd with the bundle path appended; tests
// can capture the argv via the command script to assert routing.
func (f *fakeprovider) LoadFilesFor(ctx context.Context, a *alias.Alias, bundlePath string) error {
	if len(f.cfg.LoadFilesCmd) == 0 {
		return provider.ErrNotSupported
	}
	args := append([]string(nil), f.cfg.LoadFilesCmd[1:]...)
	args = append(args, bundlePath)
	return exec.CommandContext(ctx, f.cfg.LoadFilesCmd[0], args...).Run()
}

func (f *fakeprovider) DumpFor(ctx context.Context, a *alias.Alias) (string, func(), error) {
	if f.cfg.DumpPath == "" {
		return "", nil, provider.ErrNotSupported
	}
	return f.cfg.DumpPath, func() {}, nil
}

func (f *fakeprovider) LoadFor(ctx context.Context, a *alias.Alias, dumpPath string) error {
	return provider.ErrNotSupported
}

func (f *fakeprovider) FilesDownload(ctx context.Context, a *alias.Alias, targetDir string) error {
	return provider.ErrNotSupported
}
