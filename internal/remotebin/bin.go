package remotebin

import (
	"fmt"
	"path"

	"github.com/heydon/dconsole/internal/alias"
)

// Kind values supported by alias.bin.kind.
const (
	KindDrush  = "drush"
	KindDrupal = "drupal"
	KindAuto   = "auto"
)

// Resolved is a concrete RemoteBin choice with a path. Auto must be
// resolved to a concrete kind before we can build argv — that happens in
// probe.go, which requires running a command via a Transport.
type Resolved struct {
	Kind string // KindDrush or KindDrupal
	Path string // absolute or root-relative path to the binary on the remote
}

// Resolve returns the bin choice when the alias declared a concrete kind.
// If kind is auto and we have no probe result yet, returns (nil, ok=false).
func Resolve(a *alias.Alias) (*Resolved, bool) {
	kind := a.Bin.Kind
	if kind == "" {
		kind = KindDrush
	}
	if kind == KindAuto {
		return nil, false
	}
	return &Resolved{
		Kind: kind,
		Path: defaultPath(a, kind),
	}, true
}

// Argv builds the command line to invoke the remote CLI with the given
// dconsole-side arguments. For now it's a verbatim pass-through; flag-name
// translation between drush and drupal will live here as the divergence
// becomes known.
func (r *Resolved) Argv(args []string) []string {
	out := make([]string, 0, len(args)+1)
	out = append(out, r.Path)
	out = append(out, args...)
	return out
}

func defaultPath(a *alias.Alias, kind string) string {
	if a.Bin.Path != "" {
		return a.Bin.Path
	}
	if kind != KindDrush && kind != KindDrupal {
		return kind
	}
	if a.Root == "" {
		return kind // fall back to PATH lookup
	}
	// Modern composer-installed Drupal puts vendor as a SIBLING of the
	// docroot (root = /path/to/web, vendor = /path/to/vendor). That's
	// drupal/recommended-project and ~all new sites since Drupal 8.
	// Default accordingly. Legacy in-docroot vendors and D7 should set
	// bin.path explicitly.
	return path.Join(a.Root, "..", "vendor", "bin", kind)
}

// CandidatePaths returns the paths dconsole would try, in order, when
// auto-probing on the remote. Used by both Probe (to find a working
// binary) and inspect output.
func CandidatePaths(a *alias.Alias) []string {
	var out []string
	if a.Bin.Path != "" {
		out = append(out, a.Bin.Path)
	}
	if a.Root != "" {
		// drupal preferred over drush (CLI-in-Core is the future);
		// sibling vendor preferred over nested (modern composer layout).
		out = append(out,
			path.Join(a.Root, "..", "vendor", "bin", "drupal"),
			path.Join(a.Root, "vendor", "bin", "drupal"),
			path.Join(a.Root, "..", "vendor", "bin", "drush"),
			path.Join(a.Root, "vendor", "bin", "drush"),
		)
	}
	out = append(out, "drupal", "drush")
	return out
}

// ValidateKind reports a clear error for unknown bin.kind values.
func ValidateKind(kind string) error {
	switch kind {
	case "", KindDrush, KindDrupal, KindAuto:
		return nil
	}
	return fmt.Errorf("unknown bin.kind %q (want drush, drupal, or auto)", kind)
}
