package command

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/heydon/dconsole/internal/alias"
	"github.com/heydon/dconsole/internal/assetscache"
)

// AssetsCacheList prints every cached asset bundle in dconsole's
// local assets cache, newest first.
func AssetsCacheList(out io.Writer) error {
	entries, err := assetscache.List()
	if err != nil {
		return err
	}
	dir, _ := assetscache.Dir()
	if len(entries) == 0 {
		fmt.Fprintf(out, "no cached asset bundles (cache dir: %s)\n", dir)
		fmt.Fprintln(out, "  (only provider-supplied bundles cache here — rsync/diff modes don't.)")
		return nil
	}
	fmt.Fprintf(out, "cache dir: %s\n\n", dir)
	for _, e := range entries {
		age := "?"
		if !e.Meta.StoredAt.IsZero() {
			age = humaniseAssetsDuration(time.Since(e.Meta.StoredAt))
		}
		coord := fmt.Sprintf("@%s.%s", e.Meta.Site, e.Meta.Env)
		fmt.Fprintf(out, "  %s\n", coord)
		fmt.Fprintf(out, "    key:      %s\n", e.Key)
		fmt.Fprintf(out, "    strategy: %s\n", e.Meta.Strategy)
		fmt.Fprintf(out, "    pathspec: %s\n", e.Meta.Pathspec)
		if e.Meta.SourcePath != "" {
			fmt.Fprintf(out, "    src path: %s\n", e.Meta.SourcePath)
		}
		fmt.Fprintf(out, "    size:     %s\n", humaniseAssetsSize(e.Meta.SizeBytes))
		fmt.Fprintf(out, "    age:      %s\n", age)
		fmt.Fprintf(out, "    path:     %s\n", e.Path)
	}
	return nil
}

// AssetsCacheClear removes cache entries. Empty query removes all;
// @site.env query removes entries for that coordinate.
func AssetsCacheClear(query string, out io.Writer) error {
	query = strings.TrimSpace(query)
	var filter func(assetscache.Entry) bool
	if query == "" {
		filter = nil
	} else {
		site, env, ok := alias.ParseRef(query)
		if !ok {
			return fmt.Errorf("usage: dconsole assets:cache clear [@site.env]; got %q", query)
		}
		filter = func(e assetscache.Entry) bool {
			return e.Meta.Site == site && e.Meta.Env == env
		}
	}
	removed, err := assetscache.Clear(filter)
	if err != nil {
		return err
	}
	if query == "" {
		fmt.Fprintf(out, "removed %d cached bundle(s)\n", removed)
	} else {
		fmt.Fprintf(out, "removed %d cached bundle(s) for %s\n", removed, query)
	}
	return nil
}

func humaniseAssetsDuration(d time.Duration) string {
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

func humaniseAssetsSize(b int64) string {
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
