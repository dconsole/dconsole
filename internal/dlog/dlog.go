// Package dlog is dconsole's tiny verbosity logger. Users opt in via
// the global `-v` / `-vv` / `-vvv` flags (parsed at the very top of
// main.go), and dconsole then:
//
//   - logs each remote-command spawn via Cmdf (one line: "→ <preview argv>"),
//   - appends the matching verbosity flag (-v, -vv, -vvv) to downstream
//     drush invocations so the operator sees drush's verbose output too.
//
// Drush 8 and drush 9+ both accept -v/-vv/-vvv on every command we
// invoke (sql:dump, sql:cli, user:login, list, help, plus the
// pass-through Forward path), so a single flag pass-through works
// across versions.
package dlog

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Level is the verbosity level. Higher = more output.
type Level int

const (
	// Off is the default: no dconsole-side logging.
	Off Level = iota
	// V is -v / --verbose: log each remote command spawn.
	V
	// VV is -vv: same as V dconsole-side (drush gets -vv).
	VV
	// VVV is -vvv / --debug: same as V dconsole-side (drush gets -vvv).
	VVV
)

var (
	mu    sync.Mutex
	level Level
	sink  io.Writer = os.Stderr
)

// SetLevel records the parsed verbosity. main.go calls this once after
// extracting the flag from argv.
func SetLevel(l Level) {
	mu.Lock()
	defer mu.Unlock()
	level = l
}

// GetLevel returns the current verbosity.
func GetLevel() Level {
	mu.Lock()
	defer mu.Unlock()
	return level
}

// SetSink overrides where log lines are written (default: os.Stderr).
// Used by tests.
func SetSink(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	sink = w
}

// Cmdf logs a remote-command spawn. Pass the transport-resolved argv —
// typically `t.Preview(cmd)` so users see exactly what dconsole would
// type if they ran the command manually. No-op below level V.
func Cmdf(argv []string) {
	if GetLevel() < V {
		return
	}
	emit("→ " + quote(argv))
}

// Infof logs a higher-level dconsole event (cache hit, alias resolved,
// strategy chosen). Active at -vv and above.
func Infof(format string, args ...any) {
	if GetLevel() < VV {
		return
	}
	emit(fmt.Sprintf("· "+format, args...))
}

// Debugf logs noisy internals only useful at -vvv.
func Debugf(format string, args ...any) {
	if GetLevel() < VVV {
		return
	}
	emit(fmt.Sprintf("[debug] "+format, args...))
}

// DrushFlags returns the verbosity flags to append to a drush
// invocation. Empty when verbosity is Off.
func DrushFlags() []string {
	switch GetLevel() {
	case V:
		return []string{"-v"}
	case VV:
		return []string{"-vv"}
	case VVV:
		return []string{"-vvv"}
	}
	return nil
}

// ExtractFromArgs scans args from the front for a verbosity flag and
// returns (remaining args, level). Stops at the first non-flag token,
// so `dconsole -v @x cr` is verbose but `dconsole @x cr -v` leaves the
// -v in place (so the user can still type drush flags after the alias).
//
// Recognised: -v, --verbose, -vv, -vvv, --debug.
func ExtractFromArgs(args []string) ([]string, Level) {
	lvl := Off
	for len(args) > 0 {
		switch args[0] {
		case "-v", "--verbose":
			if lvl < V {
				lvl = V
			}
		case "-vv":
			if lvl < VV {
				lvl = VV
			}
		case "-vvv", "--debug":
			lvl = VVV
		default:
			return args, lvl
		}
		args = args[1:]
	}
	return args, lvl
}

func emit(line string) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintln(sink, line)
}

// quote turns argv into a shell-safe single-line string for display.
func quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsAny(a, " \t\"'\\$&|;<>()*?[]") || a == "" {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
