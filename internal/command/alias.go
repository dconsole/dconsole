package command

import (
	"fmt"
	"io"

	"github.com/heydon/dconsole/internal/alias"
)

// SiteAliasList prints every site/env pair visible in the search path.
func SiteAliasList(out io.Writer, l *alias.Loader) error {
	sites, err := l.ListSites()
	if err != nil {
		return err
	}
	if len(sites) == 0 {
		fmt.Fprintln(out, "(no aliases found — looked in:)")
		for _, p := range l.SearchPaths {
			fmt.Fprintf(out, "  %s\n", p)
		}
		return nil
	}
	for _, site := range sites {
		envs, err := l.ListEnvs(site)
		if err != nil {
			fmt.Fprintf(out, "@%s.* (error: %v)\n", site, err)
			continue
		}
		for _, env := range envs {
			fmt.Fprintf(out, "@%s.%s\n", site, env)
		}
	}
	return nil
}
