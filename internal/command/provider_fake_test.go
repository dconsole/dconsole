package command

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/provider"
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
