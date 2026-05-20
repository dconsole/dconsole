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
	Description: "Sync assets between aliases — auto-mode picks the best rsync path; cross-site safety; Stage File Proxy fallback.",
	Usage: []string{
		"dconsole rsync <@source> <@target> [options]      # orchestrator (default for alias-to-alias %files/%private)",
		"dconsole rsync <src> <dst> [options]              # legacy tar-stream for freeform endpoints",
	},
	Args: []ArgSpec{
		{Name: "src", Description: "Source endpoint: @site.env (orchestrator) | @site.env:%files | @site.env:%private | @site.env:/abs/path | ./local/path."},
		{Name: "dst", Description: "Destination endpoint in the same form as src."},
	},
	Options: []OptionSpec{
		{Short: "v", Long: "verbose", Description: "Print progress as files transfer."},
		{Long: "force", Description: "Bypass per-env sync_policy / allow_sync_* checks."},
		{Long: "mode", ValueName: "MODE", Description: "auto (default) | rsync | diff | stage-file-proxy. See Help for details."},
		{Long: "proxy", Description: "Shortcut for --mode=stage-file-proxy."},
		{Long: "include-private", Description: "Also sync %private alongside %files (orchestrator mode only)."},
		{Long: "pathspec", ValueName: "LIST", Description: "Comma-separated pathspecs (overrides --include-private). e.g. %files,%private"},
		{Long: "delete", Description: "Remove files on target that don't exist on source (off by default)."},
		{Long: "confirm-cross-site", Description: "Bypass the interactive prompt when source.Site != target.Site (CI). Not aliased to --yes."},
		{Long: "refresh", Description: "Invalidate the cached provider-supplied bundle and re-fetch."},
		{Long: "no-cache", Description: "Bypass the provider-bundle cache for this run."},
		{Long: "cache-ttl", ValueName: "DUR", Description: "Override the provider-bundle cache TTL (default 24h or alias.assets.cache.ttl)."},
	},
	Help: `dconsole rsync has two branches:

  - Orchestrator (both endpoints alias-bound + pathspec is %files/%private):
    Cross-site safety, provider hooks, mode dispatch. See "Modes" below.

  - Legacy tar-stream (anything else — freeform paths, local endpoints):
    Tar-stream the source → tar-untar at the destination via transport.Pipe.
    The existing behaviour, unchanged.

Modes (orchestrator only):

  auto (default) — walks the rsync priority chain top to bottom; first
    match wins; falls through to diff+tar if none apply:
    1. same-host rsync (both ends share the same ssh endpoint).
    2. local-mediated rsync (laptop pulls from ssh source, pushes to target).
    3. source-driven rsync (source ssh's INTO target with agent forwarded).
    4. diff+tar fallback (find-on-both-ends; tar-pipe changed files only).
    Strategy chosen is logged at -v.

  rsync — force a real rsync invocation. Tries strategies 1-3; fails
    loudly if none of the rsync paths apply. Use when you want to know
    rsync was actually used (not falling through to diff).

  diff — skip rsync attempts entirely; use the universal diff+tar
    approach. Works through any transport (ssh, ddev, docker, …).

  stage-file-proxy — skip pulling entirely. Enable + configure the
    Stage File Proxy contributed module on the target so missing
    files lazy-fetch from the source's URI. Two drush commands fire
    on the target (drush 9+ pm:enable / config:set, with drush 8
    pm-enable / vset cascade). Source.uri must be set.

Provider hooks fire BEFORE mode dispatch:
  - provider.SyncFilesTo: end-to-end takeover (Skpr image-pull).
  - provider.FilesDownload: supplies an asset bundle; dconsole loads
    it via provider.LoadFilesFor → transport.FilesImporter
    (ddev import-files) → tar-stream push.

Cache: $XDG_CACHE_HOME/dconsole/assets/<key>.tar.gz holds provider-
supplied bundles only. rsync and diff modes don't cache (fresh per
run). Cache key includes pathspec so %files and %private cache
independently. Manage with:

  dconsole assets:cache list
  dconsole assets:cache clear            # purge all
  dconsole assets:cache clear @prod.live # purge one alias

Cross-site safety: when source.Site != target.Site, dconsole prompts
you to type the target site's name before proceeding. The check is
BEFORE the policy gate, so habitual --force use can't bypass it.

Examples:
  dconsole rsync @prod @local                           # auto mode, %files only
  dconsole rsync @prod @local --include-private         # %files + %private
  dconsole rsync @prod @local --mode=diff               # force diff+tar
  dconsole rsync @prod @local --mode=rsync              # force rsync, fail if impossible
  dconsole rsync @prod @local --mode=stage-file-proxy   # configure proxy, no transfer
  dconsole rsync @prod @local --delete --confirm-cross-site
  dconsole rsync @prod.live:%files ./local-files        # legacy freeform`,
}

// sqlSyncSpec describes `dconsole sql:sync`.
var sqlSyncSpec = CommandSpec{
	Name:        "sql:sync",
	Description: "Dump a source database, then import it into a target database (transport-agnostic, cached, transport/provider-aware).",
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
		{Long: "dump-path", ValueName: "PATH", Description: "Write the dump to PATH instead of an OS temp file. Disables caching for the run."},
		{Long: "refresh", Description: "Invalidate the cached dump and re-fetch (then re-cache)."},
		{Long: "no-cache", Description: "Bypass the cache entirely: don't read, don't write."},
		{Long: "cache-ttl", ValueName: "DUR", Description: "Override the cache freshness window (default 24h or alias.sql.cache.ttl). Go duration string, e.g. 6h, 30m."},
		{Long: "structure-tables", ValueName: "LIST", Description: "Comma-separated table names (globs OK) to dump schema-only. Overrides alias.sql.source.structure_tables."},
		{Long: "structure-tables-key", ValueName: "KEY", Description: "Reference a named structure-tables array from drush config (e.g. common). Overrides alias.sql.source.structure_tables_key."},
		{Long: "source-database", ValueName: "KEY", Description: "Drush DB connection key to dump FROM. Defaults to default (or alias.sql.source.database)."},
		{Long: "target-database", ValueName: "KEY", Description: "Drush DB connection key to load INTO. Defaults to default (or alias.sql.target.database). Forwarded to drush sql:cli and ddev import-db."},
		{Long: "confirm-cross-site", Description: "Bypass the interactive prompt when source.Site != target.Site. Intended for CI; deliberately NOT aliased to --yes/-y."},
	},
	Help: `dconsole obtains the dump using (in priority order):
  1. provider.SyncTo on the source (full end-to-end takeover, e.g. Skpr image-pull).
  2. The cached dump for this (alias, strategy, DB, structure-tables) tuple,
     if fresh under the effective TTL.
  3. provider.DumpFor on the source.
  4. alias.sql.source.type (drush | file | docker_cp).
  5. Default — drush sql:dump --gzip [--database=…] [--structure-tables-*] via
     the source's transport. Compression happens AT THE SOURCE so the wire
     only carries gzipped bytes.

Load priority:
  1. provider.LoadFor on the target.
  2. Target transport's native DBImporter (ddev import-db; faster than drush).
  3. drush sql:cli pipe with --database=<target-db> if configured.

Cache: $XDG_CACHE_HOME/dconsole/sql/<key>.sql.gz keyed by site+env+strategy
+source-database+structure-tables. Default TTL is 24h. Per-alias override
via alias.sql.cache.ttl; --cache-ttl=DUR overrides for a single run.

Cross-site safety: when source.Site != target.Site, dconsole prompts you
to type the target site name before proceeding (or aborts in scripts
without --confirm-cross-site). The check runs BEFORE the policy gate,
so habitual --force use cannot bypass it.

Manage the cache with:
  dconsole sql:cache list             # show all cached dumps
  dconsole sql:cache clear            # purge the entire cache
  dconsole sql:cache clear @prod.live # purge only entries for one alias

Examples:
  dconsole sql:sync @prod.live @dev.local
  dconsole sql:sync @prod.live @dev.local --refresh
  dconsole sql:sync @prod.live @dev.local --cache-ttl=6h
  dconsole sql:sync @prod.live @stage.test --force --confirm-cross-site
  dconsole sql:sync @prod.live @dev.local --structure-tables=cache,sessions,watchdog
  dconsole sql:sync @prod.live @dev.local --structure-tables-key=common
  dconsole sql:sync @naa.prod @naa.local --target-database=migrate
  dconsole sql:sync @prod.live @dev.local --dump-path=/tmp/dump.sql.gz --no-cache`,
}

// RsyncSpec exposes the rsync help spec for the dispatcher.
func RsyncSpec() CommandSpec { return rsyncSpec }

// SqlSyncSpec exposes the sql:sync help spec for the dispatcher.
func SqlSyncSpec() CommandSpec { return sqlSyncSpec }
