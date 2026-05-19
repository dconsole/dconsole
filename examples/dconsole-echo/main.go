// dconsole-echo is the canonical reference plugin for dconsole. It
// implements the full subprocess plugin protocol (pkg/plugin) but does
// no real work — every verb prints what it would have done to stderr
// and streams stdin to stdout. Useful as:
//
//   - integration-test fixture for the subprocess runner,
//   - copy-paste starting point for new transport plugins,
//   - quick smoke test that dconsole's plugin discovery is working.
//
// Build:   go build -o dconsole-echo ./examples/dconsole-echo
// Install: ln -s "$PWD/dconsole-echo" ~/.dconsole/plugins/
// Use:     drop an alias with `transport: { type: echo }` and run any
//          dconsole command against it; the plugin's stderr will show
//          which verb fired with what args.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/heydon/dconsole/pkg/plugin"
)

const (
	pluginName    = "echo"
	pluginVersion = "0.1.0"
)

func main() {
	if len(os.Args) < 2 {
		fail(plugin.ExitProtocolError, "dconsole-echo: missing verb (got %d args)", len(os.Args))
	}
	verb := os.Args[1]
	switch verb {
	case plugin.VerbPluginInfo:
		emitPluginInfo()
	case plugin.VerbAvailable:
		// Always available — no external CLI required.
		os.Exit(plugin.ExitOK)
	case plugin.VerbExec, plugin.VerbPipe:
		runEcho(verb)
	case plugin.VerbPreview:
		runPreview()
	case plugin.VerbShell:
		// Echo doesn't speak shell.
		fail(plugin.ExitVerbUnsupported, "dconsole-echo: shell verb not supported")
	default:
		fail(plugin.ExitProtocolError, "dconsole-echo: unknown verb %q", verb)
	}
}

func emitPluginInfo() {
	info := plugin.PluginInfo{
		ProtocolVersion: plugin.ProtocolV1,
		Name:            pluginName,
		Version:         pluginVersion,
		Description:     "Reference plugin that echoes the verb + alias to stderr (no remote action).",
		Homepage:        "https://github.com/heydon-consulting/dconsole/tree/main/examples/dconsole-echo",
		Transports: []plugin.TransportSpec{
			{Type: "echo", Description: "No-op transport that echoes the requested command."},
		},
	}
	body, err := json.Marshal(info)
	if err != nil {
		fail(plugin.ExitGeneric, "marshal plugin-info: %v", err)
	}
	os.Stdout.Write(body)
	os.Stdout.WriteString("\n")
}

// runEcho handles both exec and pipe — they're the same operation as
// far as echo is concerned (one streams TTY, one streams in/out, both
// land at this no-op).
func runEcho(verb string) {
	envelope, remoteCmd := parseStandardArgs()
	fmt.Fprintf(os.Stderr, "[dconsole-echo] verb=%s alias=@%s.%s remoteCmd=%v\n", verb, envelope.Site, envelope.Env, remoteCmd)
	fmt.Fprintf(os.Stderr, "[dconsole-echo] alias envelope: %s\n", envelopeSummary(envelope))
	// Stream stdin to stdout so callers can verify byte-for-byte that
	// data flowed through (used by sql:sync style streaming tests).
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		fail(plugin.ExitGeneric, "copy: %v", err)
	}
}

// runPreview emits the planned remote argv as a JSON string array.
// dconsole's inspect command consumes this.
func runPreview() {
	_, remoteCmd := parseStandardArgs()
	// Echo's "plan" is just to print the command itself.
	planned := append([]string{"echo:"}, remoteCmd...)
	body, _ := json.Marshal(planned)
	os.Stdout.Write(body)
	os.Stdout.WriteString("\n")
}

// parseStandardArgs reads --alias-json=<path>, loads the envelope, and
// returns it along with the args following the literal "--" separator.
func parseStandardArgs() (plugin.AliasEnvelope, []string) {
	var (
		envelopePath string
		remoteCmd    []string
		sawSep       bool
	)
	for _, a := range os.Args[2:] {
		if sawSep {
			remoteCmd = append(remoteCmd, a)
			continue
		}
		if a == "--" {
			sawSep = true
			continue
		}
		if strings.HasPrefix(a, "--alias-json=") {
			envelopePath = strings.TrimPrefix(a, "--alias-json=")
			continue
		}
		// Tolerate other flags we don't care about.
	}
	if envelopePath == "" {
		fail(plugin.ExitProtocolError, "dconsole-echo: missing --alias-json")
	}
	body, err := os.ReadFile(envelopePath)
	if err != nil {
		fail(plugin.ExitProtocolError, "dconsole-echo: read alias-json: %v", err)
	}
	var env plugin.AliasEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		fail(plugin.ExitProtocolError, "dconsole-echo: parse alias-json: %v", err)
	}
	return env, remoteCmd
}

func envelopeSummary(env plugin.AliasEnvelope) string {
	parts := []string{
		"site=" + env.Site,
		"env=" + env.Env,
	}
	if env.URI != "" {
		parts = append(parts, "uri="+env.URI)
	}
	if env.Root != "" {
		parts = append(parts, "root="+env.Root)
	}
	return strings.Join(parts, " ")
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
