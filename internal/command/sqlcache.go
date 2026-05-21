package command

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/dbcache"
)

// SqlCacheList prints every cached dump in dconsole's local sql cache,
// newest first, with age and size for each.
func SqlCacheList(out io.Writer) error {
	entries, err := dbcache.List()
	if err != nil {
		return err
	}
	dir, _ := dbcache.Dir()
	if len(entries) == 0 {
		fmt.Fprintf(out, "no cached dumps (cache dir: %s)\n", dir)
		return nil
	}
	fmt.Fprintf(out, "cache dir: %s\n\n", dir)
	for _, e := range entries {
		age := "?"
		if !e.Meta.StoredAt.IsZero() {
			age = humaniseDuration(time.Since(e.Meta.StoredAt))
		}
		coord := fmt.Sprintf("@%s.%s", e.Meta.Site, e.Meta.Env)
		fmt.Fprintf(out, "  %s\n", coord)
		fmt.Fprintf(out, "    key:      %s\n", e.Key)
		fmt.Fprintf(out, "    strategy: %s\n", e.Meta.Strategy)
		if e.Meta.SourceDB != "" && e.Meta.SourceDB != "default" {
			fmt.Fprintf(out, "    db:       %s\n", e.Meta.SourceDB)
		}
		if len(e.Meta.StructTables) > 0 {
			fmt.Fprintf(out, "    structure_tables: %s\n", strings.Join(e.Meta.StructTables, ", "))
		}
		if e.Meta.StructKey != "" {
			fmt.Fprintf(out, "    structure_tables_key: %s\n", e.Meta.StructKey)
		}
		fmt.Fprintf(out, "    size:     %s\n", humaniseSize(e.Meta.SizeBytes))
		fmt.Fprintf(out, "    age:      %s\n", age)
		fmt.Fprintf(out, "    path:     %s\n", e.Path)
	}
	return nil
}

// SqlCacheClear removes cache entries. With an empty query, removes
// everything. With an @site.env query, removes entries matching that
// alias coordinate (any strategy/DB/structure-tables variant).
func SqlCacheClear(query string, out io.Writer) error {
	query = strings.TrimSpace(query)
	var filter func(dbcache.Entry) bool
	if query == "" {
		filter = nil // remove all
	} else {
		site, env, ok := alias.ParseRef(query)
		if !ok {
			return fmt.Errorf("usage: dconsole sql:cache clear [@site.env]; got %q", query)
		}
		filter = func(e dbcache.Entry) bool {
			return e.Meta.Site == site && e.Meta.Env == env
		}
	}
	removed, err := dbcache.Clear(filter)
	if err != nil {
		return err
	}
	switch {
	case query == "":
		fmt.Fprintf(out, "removed %d cached dump(s)\n", removed)
	default:
		fmt.Fprintf(out, "removed %d cached dump(s) for %s\n", removed, query)
	}
	return nil
}

func humaniseDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func humaniseSize(b int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/mib)
	case b >= kib:
		return fmt.Sprintf("%.1f KiB", float64(b)/kib)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
