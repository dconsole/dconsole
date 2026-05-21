package command

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dconsole/dconsole/internal/pluginmgr"
	"github.com/dconsole/dconsole/internal/transport"
	"github.com/dconsole/dconsole/pkg/plugin"
)

// PluginInstall handles `dconsole plugin install` with its --name/
// --url/--path/--sha256/--version flags. Reports the installed binary
// path on success.
func PluginInstall(opts pluginmgr.InstallOpts, out io.Writer) error {
	path, err := pluginmgr.Install(opts)
	if err != nil {
		return err
	}
	// After install, run plugin-info to surface the canonical name +
	// version so the user has confidence the binary works.
	info, infoErr := transport.PluginInfo(path)
	if infoErr != nil {
		fmt.Fprintf(out, "installed %s (warning: plugin-info failed: %v)\n", path, infoErr)
		return nil
	}
	fmt.Fprintf(out, "installed %s\n  name:    %s\n  version: %s\n", path, info.Name, info.Version)
	if info.Description != "" {
		fmt.Fprintf(out, "  about:   %s\n", info.Description)
	}
	transports := pluginTransports(info)
	if len(transports) > 0 {
		fmt.Fprintf(out, "  transports: %s\n", strings.Join(transports, ", "))
	}
	return nil
}

// PluginRemove removes an installed plugin by name (no "dconsole-"
// prefix expected).
func PluginRemove(name string, out io.Writer) error {
	if err := pluginmgr.Remove(name); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed dconsole-%s\n", name)
	return nil
}

// PluginList prints installed plugins with their resolved metadata.
func PluginList(out io.Writer) error {
	names, err := pluginmgr.ListInstalled()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(out, "no plugins installed (~/.dconsole/plugins/ is empty)")
		return nil
	}
	dir, _ := pluginmgr.PluginDir()
	for _, n := range names {
		bin := filepath.Join(dir, "dconsole-"+n)
		info, err := transport.PluginInfo(bin)
		if err != nil {
			fmt.Fprintf(out, "  %s\t(plugin-info failed: %v)\n", n, err)
			continue
		}
		fmt.Fprintf(out, "  %s\t%s\t%s\n", n, info.Version, info.Description)
	}
	return nil
}

// PluginInfo prints metadata for an installed plugin.
func PluginInfo(name string, out io.Writer) error {
	dir, _ := pluginmgr.PluginDir()
	bin := filepath.Join(dir, "dconsole-"+name)
	info, err := transport.PluginInfo(bin)
	if err != nil {
		return fmt.Errorf("plugin %s: %w", name, err)
	}
	fmt.Fprintf(out, "name:        %s\n", info.Name)
	fmt.Fprintf(out, "version:     %s\n", info.Version)
	fmt.Fprintf(out, "protocol:    v%d\n", info.ProtocolVersion)
	if info.Description != "" {
		fmt.Fprintf(out, "description: %s\n", info.Description)
	}
	if info.Homepage != "" {
		fmt.Fprintf(out, "homepage:    %s\n", info.Homepage)
	}
	transports := pluginTransports(info)
	if len(transports) > 0 {
		fmt.Fprintf(out, "transports:  %s\n", strings.Join(transports, ", "))
	}
	providers := pluginProviders(info)
	if len(providers) > 0 {
		fmt.Fprintf(out, "providers:   %s\n", strings.Join(providers, ", "))
	}
	fmt.Fprintf(out, "binary:      %s\n", bin)
	return nil
}

// PluginSearch fetches the index summary and prints matches. With an
// empty query, lists everything.
func PluginSearch(query string, out io.Writer) error {
	idx, err := pluginmgr.FetchIndex()
	if err != nil {
		return fmt.Errorf("fetch index: %w", err)
	}
	q := strings.ToLower(strings.TrimSpace(query))
	matched := 0
	for _, p := range idx.Plugins {
		if q != "" && !strings.Contains(strings.ToLower(p.Name), q) && !strings.Contains(strings.ToLower(p.Description), q) {
			continue
		}
		fmt.Fprintf(out, "  %s\t%s\t%s\n", p.Name, p.LatestVersion, p.Description)
		matched++
	}
	if matched == 0 {
		if q == "" {
			fmt.Fprintln(out, "index is empty")
		} else {
			fmt.Fprintf(out, "no plugins matching %q\n", q)
		}
	}
	return nil
}

func pluginTransports(info plugin.PluginInfo) []string {
	out := make([]string, 0, len(info.Transports))
	for _, t := range info.Transports {
		out = append(out, t.Type)
	}
	return out
}

func pluginProviders(info plugin.PluginInfo) []string {
	out := make([]string, 0, len(info.Providers))
	for _, p := range info.Providers {
		out = append(out, p.Type)
	}
	return out
}
