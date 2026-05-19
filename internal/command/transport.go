package command

import (
	"fmt"
	"io"
	"sort"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/transport"
)

// TransportList prints each registered transport with a check for whether
// its underlying CLI is on PATH.
func TransportList(out io.Writer) error {
	names := transport.Names()
	sort.Strings(names)
	for _, n := range names {
		// Build a minimal alias to probe Available() through the factory.
		// Some factories require type-specific blocks; for "available"
		// reporting we don't need a usable transport, just a CLI probe.
		err := transport.ProbeAvailable(n)
		mark := "ok"
		detail := ""
		if err != nil {
			mark = "missing"
			detail = " (" + err.Error() + ")"
		}
		fmt.Fprintf(out, "  %-8s %s%s\n", n, mark, detail)
	}
	return nil
}

// requires alias.Alias for documentation cohesion — keep import alive.
var _ = (*alias.Alias)(nil)
