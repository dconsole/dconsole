package alias

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DrushAliasFile mirrors the relevant subset of the Drush 13 alias YAML
// schema. We only decode the keys dconsole knows how to map; everything
// else is silently dropped (with a note in the output).
type DrushAliasFile map[string]DrushEnv

// DrushEnv is one environment block in a Drush 13 *.site.yml.
type DrushEnv struct {
	URI     string                 `yaml:"uri"`
	Root    string                 `yaml:"root"`
	Host    string                 `yaml:"host"`
	User    string                 `yaml:"user"`
	Paths   DrushPaths             `yaml:"paths"`
	SSH     DrushSSH               `yaml:"ssh"`
	Docker  *DrushDocker           `yaml:"docker"`
	Kubectl *DrushKubectl          `yaml:"kubectl"`
	EnvVars map[string]string      `yaml:"env-vars"`
	Extra   map[string]any         `yaml:",inline"`
}

type DrushPaths struct {
	DrushScript string `yaml:"drush-script"`
	Files       string `yaml:"files"`
	Private     string `yaml:"private"`
}

type DrushSSH struct {
	Options string `yaml:"options"`
	TTY     *bool  `yaml:"tty"`
}

type DrushDocker struct {
	Service string         `yaml:"service"`
	Exec    DrushDockerExec `yaml:"exec"`
}

type DrushDockerExec struct {
	Options string `yaml:"options"`
}

type DrushKubectl struct {
	Namespace  string `yaml:"namespace"`
	Resource   string `yaml:"resource"`
	Container  string `yaml:"container"`
	Kubeconfig string `yaml:"kubeconfig"`
}

// ConvertDrushFile reads a Drush 13 site.yml from `path` and writes the
// dconsole equivalent to `out`. Returns a list of warnings for any keys
// that couldn't be mapped (so the user knows what to hand-check).
func ConvertDrushFile(path string, out io.Writer) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var src DrushAliasFile
	if err := yaml.Unmarshal(data, &src); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	converted := AliasFile{}
	var warnings []string
	for env, drushEnv := range src {
		a, w := convertEnv(env, drushEnv)
		converted[env] = a
		warnings = append(warnings, w...)
	}

	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	defer enc.Close()
	if err := enc.Encode(converted); err != nil {
		return warnings, err
	}
	return warnings, nil
}

func convertEnv(envName string, src DrushEnv) (Alias, []string) {
	var warnings []string
	a := Alias{
		URI:     src.URI,
		Root:    src.Root,
		EnvVars: src.EnvVars,
	}

	// Bin: drush 13 doesn't distinguish drupal vs drush; assume drush.
	a.Bin.Kind = "drush"
	if src.Paths.DrushScript != "" {
		a.Bin.Path = src.Paths.DrushScript
	}

	switch {
	case src.Kubectl != nil:
		a.Transport.Type = "kubectl"
		a.Transport.Kubectl = &KubectlTransport{
			Namespace:  src.Kubectl.Namespace,
			Resource:   src.Kubectl.Resource,
			Container:  src.Kubectl.Container,
			Kubeconfig: src.Kubectl.Kubeconfig,
		}
	case src.Docker != nil:
		// Drush's `docker:` key actually means docker-compose exec.
		a.Transport.Type = "compose"
		a.Transport.Compose = &ComposeTransport{
			Service: src.Docker.Service,
		}
		if src.Docker.Exec.Options != "" {
			a.Transport.Compose.ExecOptions = splitArgs(src.Docker.Exec.Options)
		}
		warnings = append(warnings, fmt.Sprintf("@%s: docker: block converted to transport.compose (project_dir not set — add it manually if running from another directory)", envName))
	case src.Host != "":
		a.Transport.Type = "ssh"
		a.Transport.SSH = &SSHTransport{
			Host: src.Host,
			User: src.User,
		}
		if src.SSH.Options != "" {
			opts, ident := extractIdentityFile(splitArgs(src.SSH.Options))
			a.Transport.SSH.Options = opts
			if ident != "" {
				a.Transport.SSH.IdentityFile = ident
			}
		}
	default:
		// No transport — Drush treats this as "local". dconsole represents
		// local sites with the exec transport.
		a.Transport.Type = "exec"
		a.Transport.Exec = &ExecTransport{}
	}

	for key := range src.Extra {
		switch key {
		case "command":
			warnings = append(warnings, fmt.Sprintf("@%s: `command:` (per-command options) was not converted — dconsole forwards all command flags, so add them at invocation time", envName))
		case "paths":
			// only drush-script is used; files/private are derived at runtime
		default:
			warnings = append(warnings, fmt.Sprintf("@%s: drush key %q is not mapped to an dconsole equivalent", envName, key))
		}
	}

	return a, warnings
}

// extractIdentityFile pulls an `-i <path>` pair out of a flat options
// slice, returning (remaining, path). Recognizes `-i path` (two args),
// `-i=path`, and `--identity-file path` / `--identity-file=path` since
// some users hand-roll these. Only the first occurrence is honored.
func extractIdentityFile(args []string) ([]string, string) {
	var out []string
	var ident string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case ident == "" && a == "-i" && i+1 < len(args):
			ident = args[i+1]
			i++
		case ident == "" && strings.HasPrefix(a, "-i="):
			ident = strings.TrimPrefix(a, "-i=")
		case ident == "" && a == "--identity-file" && i+1 < len(args):
			ident = args[i+1]
			i++
		case ident == "" && strings.HasPrefix(a, "--identity-file="):
			ident = strings.TrimPrefix(a, "--identity-file=")
		default:
			out = append(out, a)
		}
	}
	return out, ident
}

// splitArgs splits a shell-style options string by whitespace, preserving
// simple single-quoted segments. This is a best-effort split for the
// fairly tame strings Drush aliases tend to carry (e.g. "-p 2222",
// "--user www-data", "-o StrictHostKeyChecking=no").
func splitArgs(s string) []string {
	var out []string
	var current []rune
	inQuote := false
	for _, r := range s {
		switch {
		case r == '\'':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if len(current) > 0 {
				out = append(out, string(current))
				current = current[:0]
			}
		default:
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		out = append(out, string(current))
	}
	return out
}
