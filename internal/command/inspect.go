package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/remotebin"
	"github.com/heydon/dconsole/internal/transport"
)

// Inspect prints what dconsole *would* do for a given argv, without
// executing anything. Examples:
//
//   dconsole inspect @demo.dev cr
//   dconsole inspect sql:sync @demo.prod @demo.dev
//   dconsole inspect cr               # uses default alias resolution
//
// Treats the remainder of args exactly the way the real dispatcher would,
// so the report reflects the actual planned behavior — including alias
// resolution, transport choice, sync policy, and provider strategy.
func Inspect(ctx context.Context, loader *alias.Loader, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(out, "(no args — nothing to inspect)")
		return nil
	}

	// Special-case the multi-alias built-ins: their planning logic is
	// richer than a single forward.
	switch args[0] {
	case "sql:sync":
		return inspectSqlSync(ctx, loader, args[1:], out)
	case "rsync":
		return inspectRsync(ctx, loader, args[1:], out)
	case "site:alias", "transport:list", "project:list", "project:register":
		fmt.Fprintf(out, "command: %s  (built-in, no transport involved)\n", args[0])
		return nil
	case "alias:convert":
		fmt.Fprintln(out, "command: alias:convert  (local YAML transformation, no transport)")
		return nil
	case "-h", "--help", "help":
		return inspectHelp(ctx, loader, out)
	}

	// Default path: contextual resolution then forward.
	res, err := ResolveContextual(loader, args)
	if err != nil {
		return err
	}
	a := res.Alias
	rest := res.Rest

	fmt.Fprintln(out, "─── alias ─────────────────────────────────────────────")
	fmt.Fprintf(out, "  ref:       @%s.%s\n", a.Site, a.Env)
	fmt.Fprintf(out, "  source:    %s\n", res.Source)
	if m, _ := findProjectManifest(); m != nil && m.OverridePath != "" {
		fmt.Fprintf(out, "  override:  %s\n", m.OverridePath)
	}
	if a.URI != "" {
		fmt.Fprintf(out, "  uri:       %s\n", a.URI)
	}
	if a.Root != "" {
		fmt.Fprintf(out, "  root:      %s\n", a.Root)
	}
	fmt.Fprintf(out, "  bin:       %s\n", describeBin(a))
	fmt.Fprintf(out, "  transport: %s\n", a.Transport.Type)
	if a.Provider.Type != "" {
		fmt.Fprintf(out, "  provider:  %s\n", a.Provider.Type)
	}
	if hasPolicy(a) {
		fmt.Fprintf(out, "  policy:    %s\n", describePolicy(a.Policy))
	}

	if len(rest) == 0 {
		fmt.Fprintln(out, "\n(no subcommand — would print usage error)")
		return nil
	}

	fmt.Fprintln(out, "\n─── plan ──────────────────────────────────────────────")
	switch rest[0] {
	case "sh", "ssh":
		fmt.Fprintf(out, "  open interactive shell on @%s.%s (workDir=%s)\n", a.Site, a.Env, firstNonEmpty(rest, 1, a.Root))
		return nil
	case "auth":
		fmt.Fprintf(out, "  run auth flow for transport=%s", a.Transport.Type)
		if a.Provider.Type != "" {
			fmt.Fprintf(out, " and provider=%s", a.Provider.Type)
		}
		fmt.Fprintln(out)
		return nil
	case "login":
		drushArgs := []string{"user:login"}
		if !hasURIArg(rest[1:]) {
			if a.URI != "" {
				drushArgs = append([]string{"--uri=" + a.URI}, drushArgs...)
			} else {
				fmt.Fprintln(out, "  ⚠ alias has no uri — drush will emit http://default/…")
			}
		}
		drushArgs = append(drushArgs, rest[1:]...)
		bin, ok := remotebin.Resolve(a)
		if !ok {
			fmt.Fprintf(out, "  drush user:login (bin: auto — would probe first)\n")
			fmt.Fprintf(out, "  planned args: %s\n", quoteJoin(drushArgs))
			fmt.Fprintf(out, "  then open the printed URL in the default browser\n")
			return nil
		}
		t, err := transport.For(a)
		if err != nil {
			return fmt.Errorf("transport: %w", err)
		}
		remoteArgv := bin.Argv(drushArgs)
		localArgv := t.Preview(remoteArgv)
		fmt.Fprintf(out, "  drush user:login on @%s.%s, capture URL, open in browser\n", a.Site, a.Env)
		fmt.Fprintf(out, "  remote argv: %s\n", quoteJoin(remoteArgv))
		fmt.Fprintf(out, "  would run:   %s\n", quoteJoin(localArgv))
		return nil
	case "dconsole:bin":
		fmt.Fprintln(out, "  built-in: print resolved bin/transport (no transport invocation)")
		return nil
	}

	// Forward: build argv as the real dispatcher would.
	bin, ok := remotebin.Resolve(a)
	if !ok {
		fmt.Fprintf(out, "  forward %q to remote CLI (bin: auto — would probe `vendor/bin/drupal --version` then `drush --version` first)\n", strings.Join(rest, " "))
		return nil
	}
	t, err := transport.For(a)
	if err != nil {
		return fmt.Errorf("transport: %w", err)
	}
	remoteArgv := bin.Argv(rest)
	localArgv := t.Preview(remoteArgv)
	if err := t.Available(); err != nil {
		fmt.Fprintf(out, "  ⚠ transport NOT available: %v\n", err)
	}
	fmt.Fprintf(out, "  forward %q to remote %s\n", strings.Join(rest, " "), bin.Kind)
	fmt.Fprintf(out, "  remote argv: %s\n", quoteJoin(remoteArgv))
	fmt.Fprintf(out, "  would run:   %s\n", quoteJoin(localArgv))
	return nil
}

// inspectHelp describes what `dconsole --help` would do: which alias
// it'd resolve, whether the transport is reachable, and the exact
// `<bin> list --format=json` invocation that would be merged with
// dconsole's built-ins. If anything would force a fallback, it says so.
func inspectHelp(ctx context.Context, loader *alias.Loader, out io.Writer) error {
	fmt.Fprintln(out, "─── help plan ─────────────────────────────────────────")
	a, err := HelpContext(loader)
	if err != nil {
		fmt.Fprintf(out, "  context resolution error: %v\n", err)
		fmt.Fprintln(out, "  → would fall back to dconsole-only usage")
		return nil
	}
	if a == nil {
		fmt.Fprintln(out, "  no project context (no dconsole.yml with default_env in cwd or any parent)")
		fmt.Fprintln(out, "  → would fall back to dconsole-only usage")
		return nil
	}
	fmt.Fprintf(out, "  context:    @%s.%s\n", a.Site, a.Env)
	fmt.Fprintf(out, "  uri:        %s\n", a.URI)
	fmt.Fprintf(out, "  transport:  %s\n", a.Transport.Type)

	t, err := transport.For(a)
	if err != nil {
		fmt.Fprintf(out, "  ⚠ transport.For: %v → would fall back\n", err)
		return nil
	}
	if err := t.Available(); err != nil {
		fmt.Fprintf(out, "  ⚠ transport not available: %v → would fall back\n", err)
		return nil
	}
	bin, ok := remotebin.Resolve(a)
	if !ok {
		fmt.Fprintln(out, "  bin:        auto (would probe on invocation — failure here forces fallback)")
		fmt.Fprintln(out, "  planned:    <probed-bin> list --format=json")
	} else {
		argv := bin.Argv([]string{"list", "--format=json"})
		fmt.Fprintf(out, "  bin:        %s @ %s\n", bin.Kind, bin.Path)
		fmt.Fprintf(out, "  remote argv: %s\n", quoteJoin(argv))
		fmt.Fprintf(out, "  would run:   %s\n", quoteJoin(t.Preview(argv)))
	}
	fmt.Fprintln(out, "  on success: parse JSON, merge dconsole built-ins by namespace, render drush-style listing")
	fmt.Fprintln(out, "  on failure: fall back to dconsole-only usage (set DCONSOLE_HELP_DEBUG=1 to see why)")
	return nil
}

func inspectSqlSync(ctx context.Context, loader *alias.Loader, args []string, out io.Writer) error {
	// Strip flags to find positionals.
	var positional []string
	force := false
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "--keep-dump":
			// no-op for inspect purposes
		default:
			if !strings.HasPrefix(a, "--") {
				positional = append(positional, a)
			}
		}
	}
	if len(positional) != 2 {
		return fmt.Errorf("inspect sql:sync expects 2 aliases (got %d)", len(positional))
	}
	source, err := loader.ResolveRef(positional[0])
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	target, err := loader.ResolveRef(positional[1])
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}

	fmt.Fprintln(out, "─── sql:sync plan ─────────────────────────────────────")
	fmt.Fprintf(out, "  source: @%s.%s  →  target: @%s.%s\n\n", source.Site, source.Env, target.Site, target.Env)

	fmt.Fprintln(out, "  policy:")
	if err := alias.CheckSync(source, target, false); err != nil {
		fmt.Fprintf(out, "    ✗ REFUSED: %v\n", err)
		if !force {
			fmt.Fprintln(out, "    (pass --force to bypass)")
		}
	} else {
		fmt.Fprintln(out, "    ✓ allowed")
	}
	if force {
		fmt.Fprintln(out, "    (--force given: would bypass any refusal)")
	}

	fmt.Fprintln(out, "\n  source strategy (how the dump is obtained):")
	describeSourceStrategy(out, source)

	fmt.Fprintln(out, "\n  target strategy (how the dump is loaded):")
	describeTargetStrategy(out, target)

	return nil
}

func describeSourceStrategy(out io.Writer, a *alias.Alias) {
	if a.Provider.Type != "" {
		fmt.Fprintf(out, "    1. provider %s.DumpFor (e.g. iron backup download --latest)\n", a.Provider.Type)
		fmt.Fprintln(out, "       (falls through if the provider returns ErrNotSupported)")
	}
	switch a.SQL.Source.Type {
	case "file":
		fmt.Fprintf(out, "    → file: cat %s via %s transport (gzipped: %v)\n",
			a.SQL.Source.Path, a.Transport.Type, a.SQL.Source.SourceGzipped())
	case "docker_cp":
		fmt.Fprintf(out, "    → docker_cp: `docker cp %s:%s <local-tempfile>` (gzipped: %v)\n",
			a.SQL.Source.Container, a.SQL.Source.Path, a.SQL.Source.SourceGzipped())
	case "", "drush":
		fmt.Fprintf(out, "    → drush: `%s sql:dump --gzip` via %s transport\n", binPath(a), a.Transport.Type)
	default:
		fmt.Fprintf(out, "    → UNKNOWN strategy %q (sql.source.type)\n", a.SQL.Source.Type)
	}
}

func describeTargetStrategy(out io.Writer, a *alias.Alias) {
	if a.Provider.Type != "" {
		fmt.Fprintf(out, "    1. provider %s.LoadFor (most providers return ErrNotSupported → fall through)\n", a.Provider.Type)
	}
	fmt.Fprintf(out, "    → drush: gunzip <dump> | `%s sql:cli` via %s transport\n", binPath(a), a.Transport.Type)
}

// binPath returns just the resolved binary path (or a hint for auto)
// without the "kind @ path" decoration produced by describeBin.
func binPath(a *alias.Alias) string {
	r, ok := remotebin.Resolve(a)
	if !ok {
		return "<auto-probed bin>"
	}
	return r.Path
}

func inspectRsync(ctx context.Context, loader *alias.Loader, args []string, out io.Writer) error {
	var positional []string
	force := false
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "-v", "--verbose":
			// no-op
		default:
			if !strings.HasPrefix(a, "-") {
				positional = append(positional, a)
			}
		}
	}
	if len(positional) != 2 {
		return fmt.Errorf("inspect rsync expects src and dst")
	}
	src, err := ParseEndpoint(positional[0], loader)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	dst, err := ParseEndpoint(positional[1], loader)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	fmt.Fprintln(out, "─── rsync plan ────────────────────────────────────────")
	fmt.Fprintf(out, "  source: %s\n", describeEndpoint(src))
	fmt.Fprintf(out, "  destination: %s\n", describeEndpoint(dst))
	if src.Alias != nil && dst.Alias != nil {
		fmt.Fprintln(out, "\n  policy:")
		if err := alias.CheckSync(src.Alias, dst.Alias, false); err != nil {
			fmt.Fprintf(out, "    ✗ REFUSED: %v\n", err)
			if !force {
				fmt.Fprintln(out, "    (pass --force to bypass)")
			}
		} else {
			fmt.Fprintln(out, "    ✓ allowed")
		}
	}
	fmt.Fprintln(out, "\n  strategy: tar-stream via transport.Pipe (always — ssh→ssh rsync optimization is not implemented yet)")
	return nil
}

func describeEndpoint(e *Endpoint) string {
	if e.Local != "" {
		return "local: " + e.Local
	}
	return fmt.Sprintf("@%s.%s:%s  (transport: %s)", e.Alias.Site, e.Alias.Env, e.PathSpec, e.Alias.Transport.Type)
}

func describeBin(a *alias.Alias) string {
	r, ok := remotebin.Resolve(a)
	if !ok {
		return "auto (would probe on first command)"
	}
	return fmt.Sprintf("%s @ %s", r.Kind, r.Path)
}

func hasPolicy(a *alias.Alias) bool {
	p := a.Policy
	return p.SyncPolicy != "" || len(p.AllowSyncFrom) > 0 || len(p.AllowSyncTo) > 0
}

func describePolicy(p alias.Policy) string {
	var parts []string
	if p.SyncPolicy != "" {
		parts = append(parts, "sync_policy: "+p.SyncPolicy)
	}
	if len(p.AllowSyncFrom) > 0 {
		parts = append(parts, "allow_sync_from: ["+strings.Join(p.AllowSyncFrom, ", ")+"]")
	}
	if len(p.AllowSyncTo) > 0 {
		parts = append(parts, "allow_sync_to: ["+strings.Join(p.AllowSyncTo, ", ")+"]")
	}
	return strings.Join(parts, "; ")
}

func firstNonEmpty(s []string, i int, fallback string) string {
	if i < len(s) && s[i] != "" {
		return s[i]
	}
	return fallback
}

func quoteJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsAny(a, " \t\"'\\$&|;<>()") || a == "" {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
