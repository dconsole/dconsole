package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/command"
	_ "github.com/heydon/dconsole/internal/provider" // register provider factories
	"github.com/heydon/dconsole/internal/remotebin"
	_ "github.com/heydon/dconsole/internal/transport" // register transport factories
)

const version = "0.1.0-dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "dconsole:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	// Top-level flags dconsole owns (anything before the @alias).
	switch args[0] {
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return nil
	case "-V", "--version", "version":
		fmt.Println("dconsole", version)
		return nil
	}

	loader := alias.NewLoader()
	command.WireProjectResolution(loader)

	// Built-in commands that don't need an alias.
	switch args[0] {
	case "site:alias", "sa":
		return command.SiteAliasList(os.Stdout, loader)
	case "transport:list":
		return command.TransportList(os.Stdout)
	case "sql:sync":
		return runSqlSync(ctx, loader, args[1:])
	case "rsync":
		return runRsync(ctx, loader, args[1:])
	case "alias:convert":
		return runAliasConvert(args[1:])
	case "project:register":
		return command.ProjectRegister(os.Stdout)
	case "project:list":
		return command.ProjectList(os.Stdout)
	case "project:forget":
		if len(args) < 2 {
			return fmt.Errorf("usage: dconsole project:forget <name>")
		}
		return command.ProjectForget(args[1], os.Stdout)
	case "inspect", "debug", "explain":
		return command.Inspect(ctx, loader, args[1:], os.Stdout)
	case "project:init":
		return command.ProjectInit(ctx, os.Stdout, os.Stdin, parseInitFlags(args[1:]))
	}

	// Resolve the alias contextually: @site.env, @env (site from cwd),
	// or no prefix at all (project default_env or local cwd).
	res, err := command.ResolveContextual(loader, args)
	if err != nil {
		return err
	}
	a := res.Alias
	rest := res.Rest
	if err := remotebin.ValidateKind(a.Bin.Kind); err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("no command supplied (resolved alias: %s); try `status`", res.Source)
	}

	// dconsole-side built-ins. Everything else falls through to Forward.
	switch rest[0] {
	case "dconsole:bin":
		// Debug: print the resolved bin and transport for this alias.
		return printResolution(os.Stdout, a)
	case "sh", "ssh":
		// Interactive shell on the remote, landing in the alias's root
		// (where drush would normally run). Doesn't load drush.
		workDir := ""
		if len(rest) > 1 {
			workDir = rest[1]
		}
		return command.Shell(ctx, a, workDir)
	case "auth":
		// Run the alias's transport/provider auth flow (e.g. `iron login`,
		// `skpr login`). No-op for transports/providers that don't need it.
		return command.Auth(ctx, a, os.Stdout)
	case "login":
		// Run `drush user:login` and open the resulting one-time login
		// URL in the local browser. Extra args (e.g. --name=admin, a
		// destination path) are forwarded to drush.
		return command.Login(ctx, a, rest[1:], os.Stdout)
	}

	return command.Forward(ctx, a, rest)
}

func printResolution(w *os.File, a *alias.Alias) error {
	bin, ok := remotebin.Resolve(a)
	binStr := "(auto, will probe on first command)"
	if ok {
		binStr = fmt.Sprintf("%s @ %s", bin.Kind, bin.Path)
	}
	fmt.Fprintf(w, "alias:     @%s.%s\n", a.Site, a.Env)
	fmt.Fprintf(w, "uri:       %s\n", a.URI)
	fmt.Fprintf(w, "root:      %s\n", a.Root)
	fmt.Fprintf(w, "bin:       %s\n", binStr)
	fmt.Fprintf(w, "transport: %s\n", a.Transport.Type)
	if a.Provider.Type != "" {
		fmt.Fprintf(w, "provider:  %s\n", a.Provider.Type)
	}
	return nil
}

func parseInitFlags(args []string) command.InitOpts {
	opts := command.InitOpts{}
	for _, a := range args {
		switch a {
		case "--force", "-f":
			opts.Force = true
		case "--yes", "-y":
			opts.YesAll = true
		case "--dry-run", "-n":
			opts.DryRun = true
		}
	}
	return opts
}

func runAliasConvert(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: dconsole alias:convert <path/to/foo.site.yml>\n(writes the dconsole equivalent to stdout)")
	}
	warnings, err := alias.ConvertDrushFile(args[0], os.Stdout)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return err
}

func runRsync(ctx context.Context, loader *alias.Loader, args []string) error {
	opts := command.RsyncOpts{}
	var positional []string
	for _, arg := range args {
		switch arg {
		case "-v", "--verbose":
			opts.Verbose = true
		case "--force":
			opts.Force = true
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		return fmt.Errorf("usage: dconsole rsync <src> <dst> [--force]\n  src/dst = @site.env:%%files (or %%root/%%private/abs-path), or ./local/path")
	}
	src, err := command.ParseEndpoint(positional[0], loader)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	dst, err := command.ParseEndpoint(positional[1], loader)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	return command.Rsync(ctx, src, dst, os.Stdout, opts)
}

func runSqlSync(ctx context.Context, loader *alias.Loader, args []string) error {
	opts := command.SqlSyncOpts{}
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--keep-dump":
			opts.KeepDump = true
		case "--force":
			opts.Force = true
		case "--dump-path":
			if i+1 >= len(args) {
				return fmt.Errorf("--dump-path requires an argument")
			}
			i++
			opts.DumpPath = args[i]
		default:
			if strings.HasPrefix(args[i], "--dump-path=") {
				opts.DumpPath = strings.TrimPrefix(args[i], "--dump-path=")
				continue
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 2 {
		return fmt.Errorf("usage: dconsole sql:sync @source.env @target.env [--keep-dump] [--dump-path PATH] [--force]")
	}
	source, err := loader.ResolveRef(positional[0])
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	target, err := loader.ResolveRef(positional[1])
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	return command.SqlSync(ctx, source, target, os.Stdout, opts)
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, strings.TrimSpace(`
dconsole — a transport-agnostic Drush/Drupal CLI client.

Usage:
  dconsole @site.env <command> [args…]      # forward to remote drush/drupal
  dconsole @env <command>                   # short form: site inferred from local dconsole.yml
  dconsole <command>                        # no alias: uses dconsole.yml default_env, else local cwd
  dconsole @site.env sh    (or ssh)         # interactive shell on the remote, no drush loaded
  dconsole @site.env auth                   # run provider/transport auth (e.g. iron login)
  dconsole @site.env login [drush uli args] # drush user:login → open one-time URL in your browser
  dconsole site:alias                       # list known aliases
  dconsole transport:list                   # report which transports are usable on this machine
  dconsole sql:sync @src.env @dst.env [--force]  # dump source DB, import into target (transport-agnostic)
  dconsole rsync @src.env:%files @dst.env:%files [--force]  # copy %files / %root / %private / abs paths
  dconsole alias:convert drush.site.yml > dconsole.site.yml  # port a Drush 13 alias file
  dconsole project:init [--yes] [--force] [--dry-run]  # detect project sources and generate dconsole.yml
  dconsole project:register                 # register the dconsole.yml at-or-above cwd
  dconsole project:list                     # list registered projects
  dconsole project:forget <name>            # remove a project from the registry
  dconsole inspect <invocation>             # print what dconsole would do (also aliased as debug, explain)
  dconsole @site.env dconsole:bin           # debug: show resolved bin/transport
  dconsole --version

Aliases come from (in priority order):
  $PWD/dconsole/sites/*.site.yml
  $XDG_CONFIG_HOME/dconsole/sites/*.site.yml
  ~/.dconsole/sites/*.site.yml
  ~/.dconsole/projects.yml  (auto-managed via project:register)
`))
}
