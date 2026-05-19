package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/heydon/dconsole/internal/alias"
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

	var stdout bytes.Buffer
	cmd := bin.Argv([]string{"list", "--format=json"})
	if err := t.Pipe(ctx, cmd, nil, &stdout); err != nil {
		helpDebug("list --format=json failed: %v (out: %d bytes)", err, stdout.Len())
		fallback(out)
		return nil
	}

	var help drushListJSON
	// drush sometimes prints status/banner lines before the JSON; trim to
	// the first '{' to be tolerant.
	body := stdout.Bytes()
	if i := bytes.IndexByte(body, '{'); i > 0 {
		body = body[i:]
	}
	if err := json.Unmarshal(body, &help); err != nil {
		helpDebug("json unmarshal failed: %v (body: %s)", err, truncate(string(body), 200))
		fallback(out)
		return nil
	}

	renderMergedHelp(out, a, &help)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// renderMergedHelp writes a drush-like grouped help listing. dconsole
// built-ins are merged into drush's namespaces by prefix; conflicts on
// command name resolve in favour of dconsole (since dconsole intercepts).
// Each dconsole-owned entry is suffixed with "(dconsole)".
func renderMergedHelp(out io.Writer, a *alias.Alias, help *drushListJSON) {
	appName := help.Application.Name
	appVer := help.Application.Version
	header := "dconsole"
	if appName != "" {
		header = fmt.Sprintf("dconsole — proxy to %s %s on @%s.%s", appName, appVer, a.Site, a.Env)
	} else {
		header = fmt.Sprintf("dconsole — on @%s.%s", a.Site, a.Env)
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

	// Bucket by namespace (prefix before ':', else "_global").
	buckets := make(map[string][]helpEntry)
	for _, e := range byName {
		ns := "_global"
		if i := strings.Index(e.Name, ":"); i > 0 {
			ns = e.Name[:i]
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
