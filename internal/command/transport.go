package command

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/dconsole/dconsole/internal/pluginmgr"
	"github.com/dconsole/dconsole/internal/transport"
)

// TransportList prints each registered transport (built-in + installed
// plugins) with a check for whether its underlying CLI is on PATH.
func TransportList(out io.Writer) error {
	// In-tree first, alphabetically. ListableNames already drops
	// HideWhenMissing handlers whose CLI is absent.
	names := transport.ListableNames()
	sort.Strings(names)
	for _, n := range names {
		err := transport.ProbeAvailable(n)
		mark := "ok"
		detail := ""
		if err != nil {
			mark = "missing"
			detail = " (" + err.Error() + ")"
		}
		fmt.Fprintf(out, "  %-12s %-8s %s\n", n, mark, detail)
	}

	// Then installed plugins. Each contributes one or more transport
	// types via its plugin-info; we list each type separately so users
	// can see exactly what they can `transport: { type: X }` against.
	installed, err := pluginmgr.ListInstalled()
	if err != nil || len(installed) == 0 {
		return nil
	}
	dir, _ := pluginmgr.PluginDir()
	pluginSeen := false
	for _, name := range installed {
		bin := filepath.Join(dir, "dconsole-"+name)
		info, err := transport.PluginInfo(bin)
		if err != nil {
			continue
		}
		for _, spec := range info.Transports {
			if !pluginSeen {
				fmt.Fprintln(out, "  --- plugin transports ---")
				pluginSeen = true
			}
			mark := "ok"
			detail := fmt.Sprintf("(via dconsole-%s v%s)", name, info.Version)
			if spec.RequiredCLI != "" {
				if cliErr := transport.CLIAvailable(spec.RequiredCLI); cliErr != nil {
					mark = "missing"
					detail = "(" + cliErr.Error() + ")"
				}
			}
			fmt.Fprintf(out, "  %-12s %-8s %s\n", spec.Type, mark, detail)
		}
	}
	return nil
}
