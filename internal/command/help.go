package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/dlog"
	"github.com/heydon/dconsole/internal/transport"
)

func helpDebug(format string, args ...any) {
	if os.Getenv("DCONSOLE_HELP_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[help] "+format+"\n", args...)
	}
}

// builtinCommand describes a dconsole built-in for help output.
type builtinCommand struct {
	Name        string
	Description string
	// Aliases shown in the description suffix (e.g. ["sa"] → "(also: sa)").
	Aliases []string
}

// dconsoleBuiltins is the canonical list of dconsole commands shown in
// --help. Source-of-truth lives next to the dispatcher in cmd/dconsole.
var dconsoleBuiltins = []builtinCommand{
	{Name: "site:alias", Description: "List known dconsole aliases", Aliases: []string{"sa"}},
	{Name: "transport:list", Description: "Report which transports are usable on this machine"},
	{Name: "sql:sync", Description: "Dump source DB, import into target DB (transport-agnostic)"},
	{Name: "rsync", Description: "Copy %files / %root / %private / abs paths between aliases"},
	{Name: "alias:convert", Description: "Convert a Drush alias file to dconsole format"},
	{Name: "project:init", Description: "Detect project sources and generate dconsole.yml"},
	{Name: "project:register", Description: "Register the dconsole.yml at-or-above cwd"},
	{Name: "project:list", Description: "List registered projects"},
	{Name: "project:forget", Description: "Remove a project from the registry"},
	{Name: "inspect", Description: "Print what dconsole would do without running it", Aliases: []string{"debug", "explain"}},
	{Name: "sh", Description: "Interactive shell on the remote (no drush loaded)", Aliases: []string{"ssh"}},
	{Name: "auth", Description: "Run transport/provider auth flow (e.g. iron login)"},
	{Name: "login", Description: "drush user:login → open the one-time URL in your browser"},
	{Name: "dconsole:bin", Description: "Debug: show resolved bin/transport for this alias"},
}

// drushListJSON mirrors the `drush list --format=json` payload (Symfony
// Console). We only decode the fields we render.
type drushListJSON struct {
	Application struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"application"`
	Commands []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Hidden      bool   `json:"hidden"`
	} `json:"commands"`
	Namespaces []struct {
		ID       string   `json:"id"`
		Commands []string `json:"commands"`
	} `json:"namespaces"`
}

// HelpContext returns the alias to render help against, or (nil, nil) if
// there's no usable context. Non-interactive: never prompts the user
// (unlike ResolveContextual). Suitable for `dconsole --help` when no
// alias was given.
func HelpContext(loader *alias.Loader) (*alias.Alias, error) {
	m, err := findProjectManifest()
	if err != nil {
		return nil, err
	}
	if m == nil || m.DefaultEnv == "" {
		return nil, nil
	}
	return m.ResolveEnv(m.DefaultEnv)
}

// Help runs `<bin> list --format=json` on the alias, parses it, and
// renders a merged help screen. If anything goes wrong before we have
// usable JSON, fallback is called and Help returns nil (the user still
// gets help, just the dconsole-only version).
func Help(ctx context.Context, a *alias.Alias, out io.Writer, fallback func(io.Writer)) error {
	if a == nil {
		helpDebug("no alias context — using fallback")
		fallback(out)
		return nil
	}
	t, err := transport.For(a)
	if err != nil {
		helpDebug("transport.For: %v", err)
		fallback(out)
		return nil
	}
	if err := t.Available(); err != nil {
		helpDebug("transport %s not available: %v", t.Name(), err)
		fallback(out)
		return nil
	}
	bin, err := resolveBin(ctx, a, t)
	if err != nil {
		helpDebug("resolveBin: %v", err)
		fallback(out)
		return nil
	}

	// Try drush 9+ first: list --format=json.
	var stdout bytes.Buffer
	cmd := bin.Argv(augmentDrushContext(a, []string{"list", "--format=json"}))
	dlog.Cmdf(t.Preview(cmd))
	jsonErr := t.Pipe(ctx, cmd, nil, &stdout)
	if jsonErr == nil {
		if help, ok := tryJSON(stdout.Bytes()); ok {
			renderMergedHelp(out, a, help)
			return nil
		}
		helpDebug("list --format=json: invalid JSON, will try plain-text fallback")
	} else {
		helpDebug("list --format=json failed: %v (out: %d bytes); will try plain-text fallback", jsonErr, stdout.Len())
	}

	// Drush 8 fallback: parse `drush help` plain text.
	stdout.Reset()
	cmd = bin.Argv(augmentDrushContext(a, []string{"help"}))
	dlog.Cmdf(t.Preview(cmd))
	if err := t.Pipe(ctx, cmd, nil, &stdout); err != nil {
		helpDebug("drush help (plain text) failed: %v", err)
		fmt.Fprintf(out, "Note: couldn't reach drush on @%s.%s (neither JSON nor plain-text help). Showing dconsole built-ins only.\n\n", a.Site, a.Env)
		fallback(out)
		return nil
	}
	if help, ok := parseDrush8Help(stdout.String()); ok {
		renderMergedHelp(out, a, help)
		return nil
	}
	helpDebug("plain-text parse found no commands; falling back to dconsole-only listing")
	fmt.Fprintf(out, "Note: drush on @%s.%s emitted help in a format dconsole doesn't recognise. Showing dconsole built-ins only.\n\n", a.Site, a.Env)
	fallback(out)
	return nil
}

// tryJSON parses drush 9+ list --format=json output. Returns ok=false
// if the body doesn't look like JSON or doesn't contain any commands.
func tryJSON(body []byte) (*drushListJSON, bool) {
	// Trim leading non-JSON noise (banner lines, deprecation notices).
	if i := bytes.IndexByte(body, '{'); i > 0 {
		body = body[i:]
	}
	if len(body) == 0 || body[0] != '{' {
		return nil, false
	}
	var help drushListJSON
	if err := json.Unmarshal(body, &help); err != nil {
		return nil, false
	}
	if len(help.Commands) == 0 {
		return nil, false
	}
	return &help, true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// drush 8 category header: "Core Drush commands: (core)".
var drush8CategoryRE = regexp.MustCompile(`^([A-Z].*?):\s*\(([a-z][a-z0-9_-]*)\)\s*$`)

// parseDrush8Help parses drush 8's textual `drush help` output into the
// same drushListJSON shape our JSON path produces. Returns ok=false if
// the input doesn't look like drush 8 help (no recognised commands).
//
// Drush 8 formats the listing as two columns: name + parenthesised
// aliases on the left (~22 chars), description on the right. Both sides
// can wrap across multiple lines. We track paren balance so alias
// continuation lines don't get mistaken for new commands.
func parseDrush8Help(text string) (*drushListJSON, bool) {
	help := &drushListJSON{}
	help.Application.Name = "Drush"
	// Drush 8 doesn't print a version in `drush help` output; users
	// can run `drush --version` if they need it.

	type cmdEntry struct {
		Name, Description string
	}
	var (
		currentCategory string
		inParen         int // unmatched open parens from previous lines
		nsCommands      = map[string][]string{}
		categoryOrder   []string
	)

	for _, line := range strings.Split(text, "\n") {
		// Category header?
		if m := drush8CategoryRE.FindStringSubmatch(strings.TrimRight(line, " \t\r")); m != nil {
			currentCategory = m[2]
			if _, seen := nsCommands[currentCategory]; !seen {
				categoryOrder = append(categoryOrder, currentCategory)
				nsCommands[currentCategory] = nil
			}
			inParen = 0
			continue
		}

		// Need a leading space (command lines and continuations start indented).
		if len(line) == 0 || line[0] != ' ' {
			inParen = 0
			continue
		}

		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" {
			continue
		}

		// Continuation of a wrapped alias list — first non-space char
		// is an alias-continuation token or a closing paren.
		if inParen > 0 {
			inParen += strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
			if inParen < 0 {
				inParen = 0
			}
			continue
		}

		// Skip global option lines (start with '-').
		if trimmed[0] == '-' {
			continue
		}

		// Alias continuation that opens a paren on a new line, e.g.
		//   archive-restore   Expand a site archive…
		//   (arr,
		//   archive:restore)
		// The "(arr," line starts after a command with no open parens
		// but is itself the start of a wrapped alias group.
		if trimmed[0] == '(' {
			inParen += strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
			if inParen < 0 {
				inParen = 0
			}
			continue
		}
		name, desc, openParens, ok := parseDrush8CommandLine(trimmed)
		if !ok || currentCategory == "" {
			continue
		}
		help.Commands = append(help.Commands, struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Hidden      bool   `json:"hidden"`
		}{Name: name, Description: desc})
		nsCommands[currentCategory] = append(nsCommands[currentCategory], name)
		inParen += openParens
		_ = cmdEntry{}
	}

	if len(help.Commands) == 0 {
		return nil, false
	}

	for _, id := range categoryOrder {
		help.Namespaces = append(help.Namespaces, struct {
			ID       string   `json:"id"`
			Commands []string `json:"commands"`
		}{ID: id, Commands: nsCommands[id]})
	}
	return help, true
}

// parseDrush8CommandLine pulls (name, description, openParens) from the
// first line of a drush 8 command entry. Returns ok=false if the line
// doesn't look like a command line.
//
// Example inputs:
//
//	"archive-dump (ard,    Backup your code, files, …"
//	"core-status (status,  Provides a birds-eye view …"
//	"do:sanitize           Performs database sanitization."
func parseDrush8CommandLine(s string) (name, desc string, openParens int, ok bool) {
	// The name is the first token. drush 8 command names allow lowercase
	// letters, digits, hyphens, colons, underscores.
	end := 0
	for end < len(s) {
		c := s[end]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == ':' || c == '_' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return "", "", 0, false
	}
	name = s[:end]
	rest := s[end:]

	// Split the remainder on 2+ spaces — the left column ends at the
	// gap between aliases and description. If there's no such gap, the
	// line continues with no description (aliases-only line); we still
	// consider it a valid command line and use an empty description.
	gap := regexp.MustCompile(`\s{2,}`).FindStringIndex(rest)
	if gap == nil {
		desc = ""
	} else {
		// Anything before the gap is alias material (or empty).
		left := rest[:gap[0]]
		openParens = strings.Count(left, "(") - strings.Count(left, ")")
		if openParens < 0 {
			openParens = 0
		}
		desc = strings.TrimSpace(rest[gap[1]:])
	}
	return name, desc, openParens, true
}

// renderMergedHelp writes a drush-like grouped help listing. dconsole
// built-ins are merged into drush's namespaces by prefix; conflicts on
// command name resolve in favour of dconsole (since dconsole intercepts).
// Each dconsole-owned entry is suffixed with "(dconsole)".
func renderMergedHelp(out io.Writer, a *alias.Alias, help *drushListJSON) {
	appName := help.Application.Name
	appVer := help.Application.Version
	header := fmt.Sprintf("dconsole — on @%s.%s", a.Site, a.Env)
	switch {
	case appName != "" && appVer != "":
		header = fmt.Sprintf("dconsole — proxy to %s %s on @%s.%s", appName, appVer, a.Site, a.Env)
	case appName != "":
		header = fmt.Sprintf("dconsole — proxy to %s on @%s.%s", appName, a.Site, a.Env)
	}
	fmt.Fprintln(out, header)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  dconsole [@site.env] <command> [args…]")
	fmt.Fprintln(out)

	// Aggregate: command name → helpEntry. dconsole wins on conflict.
	byName := make(map[string]helpEntry)
	for _, c := range help.Commands {
		if c.Hidden {
			continue
		}
		byName[c.Name] = helpEntry{Name: c.Name, Description: c.Description}
	}
	for _, b := range dconsoleBuiltins {
		desc := b.Description
		if len(b.Aliases) > 0 {
			desc += " (also: " + strings.Join(b.Aliases, ", ") + ")"
		}
		byName[b.Name] = helpEntry{Name: b.Name, Description: desc, Dconsole: true}
	}

	// Bucket by namespace. drush ships an explicit namespaces list, so
	// prefer that (it handles drush 8 categories whose command names
	// don't use colons). Fall back to prefix-inference for any command
	// not listed in help.Namespaces, which also handles dconsole's
	// own built-ins (sql:sync, project:list, …).
	explicitNS := make(map[string]string)
	for _, ns := range help.Namespaces {
		for _, cmd := range ns.Commands {
			explicitNS[cmd] = ns.ID
		}
	}
	buckets := make(map[string][]helpEntry)
	for _, e := range byName {
		ns, ok := explicitNS[e.Name]
		if !ok {
			ns = "_global"
			if i := strings.Index(e.Name, ":"); i > 0 {
				ns = e.Name[:i]
			}
		}
		buckets[ns] = append(buckets[ns], e)
	}
	for k := range buckets {
		sort.Slice(buckets[k], func(i, j int) bool { return buckets[k][i].Name < buckets[k][j].Name })
	}

	// Render: global first, then namespaces alphabetically.
	width := commandColumnWidth(byName)
	renderEntry := func(e helpEntry) {
		suffix := ""
		if e.Dconsole {
			suffix = " [dconsole]"
		}
		desc := e.Description + suffix
		if len(e.Name) >= width {
			fmt.Fprintf(out, "  %s\n  %s%s\n", e.Name, strings.Repeat(" ", width), desc)
		} else {
			fmt.Fprintf(out, "  %-*s%s\n", width, e.Name, desc)
		}
	}

	if g, ok := buckets["_global"]; ok && len(g) > 0 {
		fmt.Fprintln(out, "Available commands:")
		for _, e := range g {
			renderEntry(e)
		}
		fmt.Fprintln(out)
	}

	var nsNames []string
	for k := range buckets {
		if k != "_global" {
			nsNames = append(nsNames, k)
		}
	}
	sort.Strings(nsNames)
	for _, ns := range nsNames {
		fmt.Fprintf(out, " %s:\n", ns)
		for _, e := range buckets[ns] {
			renderEntry(e)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, "Commands marked [dconsole] are intercepted locally; everything else forwards to the remote CLI.")
}

func commandColumnWidth(byName map[string]helpEntry) int {
	width := 24
	for name := range byName {
		if l := len(name) + 2; l > width {
			width = l
		}
	}
	if width > 40 {
		return 40
	}
	return width
}

type helpEntry struct {
	Name        string
	Description string
	Dconsole    bool
}
