package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/pkg/transport"
)

// fakeImportTransport satisfies both transport.Transport AND
// transport.DBImporter so we can prove loadDump prefers the
// DBImporter path over drush sql:cli.
type fakeImportTransport struct {
	cfg fakeImportConfig
}

type fakeImportConfig struct {
	// LogFile is where ImportDB appends one line per call: "<dumpPath>\n".
	// Tests inspect it to assert the importer was chosen and got the
	// right path.
	LogFile string `yaml:"log_file"`
}

func init() {
	transport.Register("fake-importer", transport.Registration{
		Build: func(a *alias.Alias) (transport.Transport, error) {
			var w struct {
				FakeImporter fakeImportConfig `yaml:"fake-importer"`
			}
			if err := a.Transport.Decode(&w); err != nil {
				return nil, fmt.Errorf("fake-importer transport: %w", err)
			}
			return &fakeImportTransport{cfg: w.FakeImporter}, nil
		},
	})
}

func (f *fakeImportTransport) Name() string     { return "fake-importer" }
func (f *fakeImportTransport) Available() error { return nil }

// Exec/Pipe/Shell/Preview don't matter for the importer test path —
// they should never be called when DBImporter is preferred. Surface
// loud errors if they are.
func (f *fakeImportTransport) Exec(ctx context.Context, cmd []string, stdio transport.Stdio) error {
	return fmt.Errorf("fake-importer.Exec called with %v (should have used ImportDB)", cmd)
}
func (f *fakeImportTransport) Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error {
	return fmt.Errorf("fake-importer.Pipe called with %v (should have used ImportDB)", cmd)
}
func (f *fakeImportTransport) Shell(ctx context.Context, workDir string) error {
	return fmt.Errorf("fake-importer.Shell called (should have used ImportDB)")
}
func (f *fakeImportTransport) Preview(cmd []string) []string {
	return append([]string{"fake-importer"}, cmd...)
}

// ImportDB satisfies pkg/transport.DBImporter. Appends a log line so
// tests can assert it was invoked with the expected dump path.
func (f *fakeImportTransport) ImportDB(ctx context.Context, a *alias.Alias, dumpPath string) error {
	if f.cfg.LogFile == "" {
		return fmt.Errorf("fake-importer needs a log_file in its alias config")
	}
	fakeImporterMu.Lock()
	defer fakeImporterMu.Unlock()
	out, err := os.OpenFile(f.cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = fmt.Fprintf(out, "%s\tdb=%s\n", dumpPath, a.SQL.Target.Database)
	return err
}

var fakeImporterMu sync.Mutex
