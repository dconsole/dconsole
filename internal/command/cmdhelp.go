package command

import (
	"fmt"
	"io"
	"strings"
)

// CommandSpec describes a dconsole subcommand for the per-command help
// renderer. Mirrors the Symfony Console / drush 13 layout so a user
// seeing `dconsole rsync --help` gets the same shape they'd see for
// any drush command on the remote.
type CommandSpec struct {
	// Name is the canonical subcommand as the user types it (e.g. "rsync").
	Name string
	// Description is the one-line summary shown under "Description:".
	Description string
	// Usage holds one or more usage lines shown under "Usage:".
	Usage []string
	// Aliases are alternate command names (shown at the bottom).
	Aliases []string
	// Args describes positional arguments.
	Args []ArgSpec
	// Options describes flags.
	Options []OptionSpec
	// Help is an optional multi-line long-form explanation shown under
	// "Help:" at the very bottom.
	Help string
}

// ArgSpec describes a positional argument.
type ArgSpec struct {
	Name        string
	Description string
}

// OptionSpec describes a command-line flag.
type OptionSpec struct {
	// Short is the short flag name without the leading dash, e.g. "v" → "-v".
	// Empty means no short form.
	Short string
	// Long is the long flag name without the leading dashes, e.g. "verbose" → "--verbose".
	Long string
	// ValueName makes the flag take a value; rendered as "--long=VALUE".
	// Empty means it's a bare boolean flag.
	ValueName string
	// Description is the help line.
	Description string
}

// IsHelpFlag reports whether s is one of the flags users type to ask
// for command-level help (-h / --help / help).
func IsHelpFlag(s string) bool {
	switch s {
	case "-h", "--help", "help":
		return true
	}
	return false
}

// ArgsHaveHelpFlag scans args for any help-style flag.
func ArgsHaveHelpFlag(args []string) bool {
	for _, a := range args {
		if IsHelpFlag(a) {
			return true
		}
	}
	return false
}

// RenderCommandHelp emits the spec in drush 13 / Symfony Console style.
func RenderCommandHelp(out io.Writer, s CommandSpec) {
	fmt.Fprintln(out, "Description:")
	fmt.Fprintf(out, "  %s\n\n", s.Description)

	if len(s.Usage) > 0 {
		fmt.Fprintln(out, "Usage:")
		for _, u := range s.Usage {
			fmt.Fprintf(out, "  %s\n", u)
		}
		fmt.Fprintln(out)
	}

	width := columnWidth(s)
	if len(s.Args) > 0 {
		fmt.Fprintln(out, "Arguments:")
		for _, a := range s.Args {
			fmt.Fprintf(out, "  %-*s  %s\n", width, a.Name, a.Description)
		}
		fmt.Fprintln(out)
	}

	if len(s.Options) > 0 {
		fmt.Fprintln(out, "Options:")
		for _, o := range s.Options {
			tag := optionTag(o)
			fmt.Fprintf(out, "  %-*s  %s\n", width, tag, o.Description)
		}
		fmt.Fprintln(out)
	}

	if len(s.Aliases) > 0 {
		fmt.Fprintf(out, "Aliases: %s\n", strings.Join(s.Aliases, ", "))
	}

	if s.Help != "" {
		fmt.Fprintln(out, "\nHelp:")
		for _, line := range strings.Split(strings.TrimRight(s.Help, "\n"), "\n") {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
}

func optionTag(o OptionSpec) string {
	long := "--" + o.Long
	if o.ValueName != "" {
		long += "=" + o.ValueName
	}
	if o.Short == "" {
		return "    " + long
	}
	return "-" + o.Short + ", " + long
}

func columnWidth(s CommandSpec) int {
	width := 20
	for _, a := range s.Args {
		if l := len(a.Name); l > width {
			width = l
		}
	}
	for _, o := range s.Options {
		if l := len(optionTag(o)); l > width {
			width = l
		}
	}
	if width > 36 {
		return 36
	}
	return width
}

// rsyncSpec describes `dconsole rsync`.
var rsyncSpec = CommandSpec{
	Name:        "rsync",
	Description: "Copy %files / %root / %private / abs paths between aliases via a transport-agnostic tar stream.",
	Usage: []string{
		"dconsole rsync <src> <dst> [options]",
	},
	Args: []ArgSpec{
		{Name: "src", Description: "Source endpoint: @site.env:%files, @site.env:%root, @site.env:%private, @site.env:/abs/path, or ./local/path."},
		{Name: "dst", Description: "Destination endpoint in the same form as src."},
	},
	Options: []OptionSpec{
		{Short: "v", Long: "verbose", Description: "Print progress as files transfer."},
		{Long: "force", Description: "Bypass per-env sync_policy / allow_sync_* checks."},
	},
	Help: `Each endpoint is resolved independently via its transport, so source and destination
don't need to share a transport (e.g. ssh source → ddev destination is fine). The
copy streams a tar archive end-to-end and never writes a temporary file locally.

Tokens (must be the first thing after the colon in an alias endpoint):
  %files     The site's files directory.
  %root      The Drupal docroot.
  %private   The site's private files directory (if configured).

Examples:
  dconsole rsync @prod.live:%files ./local-files
  dconsole rsync ./local-files @stage.test:%files --force
  dconsole rsync @prod.live:%root/sites/default/files @dev.local:%files`,
}

// sqlSyncSpec describes `dconsole sql:sync`.
var sqlSyncSpec = CommandSpec{
	Name:        "sql:sync",
	Description: "Dump a source database, then import it into a target database (transport-agnostic).",
	Usage: []string{
		"dconsole sql:sync <@source.env> <@target.env> [options]",
	},
	Args: []ArgSpec{
		{Name: "source", Description: "Source alias to dump from."},
		{Name: "target", Description: "Target alias to import into."},
	},
	Options: []OptionSpec{
		{Long: "force", Description: "Bypass per-env sync_policy / allow_sync_* checks."},
		{Long: "keep-dump", Description: "Keep the temporary dump file after import (for debugging)."},
		{Long: "dump-path", ValueName: "PATH", Description: "Write the dump to PATH instead of an OS temp file (implies --keep-dump-style behaviour)."},
	},
	Help: `dconsole obtains the dump using (in priority order):
  1. The source's configured provider, if it implements DumpFor.
  2. The source's alias.sql.source.type (drush | file | docker_cp).
  3. The default — drush sql:dump --gzip via the source's transport.

The dump is then imported into the target via drush sql:cli (or the target
provider's LoadFor, if implemented). Per-env policies (sync_policy,
allow_sync_from / allow_sync_to) are enforced before the dump starts.

Examples:
  dconsole sql:sync @prod.live @dev.local
  dconsole sql:sync @prod.live @stage.test --force --keep-dump
  dconsole sql:sync @prod.live @dev.local --dump-path=/tmp/prod-dump.sql.gz`,
}

// RsyncSpec exposes the rsync help spec for the dispatcher.
func RsyncSpec() CommandSpec { return rsyncSpec }

// SqlSyncSpec exposes the sql:sync help spec for the dispatcher.
func SqlSyncSpec() CommandSpec { return sqlSyncSpec }
