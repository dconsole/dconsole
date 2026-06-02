// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

// stubHandler is a tiny test Handler that records what Wrap was given
// and applies a simple prefix-or-quote transform. Two flavors:
//   - prefix mode: ["docker", "exec", "container", ...inner]   (no quoting)
//   - quote mode:  ["ssh", "user@host", "--", strings.Join(inner, " ")]  (quote inner)
type stubHandler struct {
	name       string
	prefix     []string // when set, prepended to inner
	quoteAfter int      // when prefix=[ssh,user@host,--], inner is shell-quoted into one string at that position
	availErr   error
}

func (h *stubHandler) Name() string     { return h.name }
func (h *stubHandler) Available() error { return h.availErr }
func (h *stubHandler) Wrap(inner []string) []string {
	if h.quoteAfter > 0 {
		// Quote inner into a single arg following h.prefix.
		out := append([]string{}, h.prefix...)
		out = append(out, strings.Join(inner, " "))
		return out
	}
	return append(append([]string{}, h.prefix...), inner...)
}
func (h *stubHandler) Exec(ctx context.Context, cmd []string, stdio Stdio) error { return nil }
func (h *stubHandler) Pipe(ctx context.Context, cmd []string, in io.Reader, out io.Writer) error {
	return nil
}
func (h *stubHandler) Shell(ctx context.Context, workDir string) error { return nil }
func (h *stubHandler) Preview(cmd []string) []string                   { return h.Wrap(cmd) }

func TestChainWrapInsideOut(t *testing.T) {
	ssh := &stubHandler{
		name:       "ssh",
		prefix:     []string{"ssh", "deploy@prod", "--"},
		quoteAfter: 1, // shell-quote inner
	}
	docker := &stubHandler{
		name:   "docker",
		prefix: []string{"docker", "exec", "drupal-app"},
	}

	chain := NewChain([]Handler{ssh, docker}) // outer-to-inner
	got := chain.Wrap([]string{"drush", "status"})
	want := []string{"ssh", "deploy@prod", "--", "docker exec drupal-app drush status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Wrap chain:\n  got  %q\n  want %q", got, want)
	}
}

func TestChainThreeLayers(t *testing.T) {
	sshJump := &stubHandler{name: "ssh-jump", prefix: []string{"ssh", "jump@bastion", "--"}, quoteAfter: 1}
	sshTarget := &stubHandler{name: "ssh-target", prefix: []string{"ssh", "deploy@prod", "--"}, quoteAfter: 1}
	docker := &stubHandler{name: "docker", prefix: []string{"docker", "exec", "drupal-app"}}

	chain := NewChain([]Handler{sshJump, sshTarget, docker})
	got := chain.Wrap([]string{"drush", "cr"})
	// jump quotes (target quotes (docker prefix + inner))
	// inner: drush cr
	// docker.Wrap: docker exec drupal-app drush cr
	// target.Wrap: ssh deploy@prod -- "docker exec drupal-app drush cr"
	// jump.Wrap:   ssh jump@bastion -- "ssh deploy@prod -- docker exec drupal-app drush cr"
	want := []string{"ssh", "jump@bastion", "--", "ssh deploy@prod -- docker exec drupal-app drush cr"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("3-layer Wrap:\n  got  %q\n  want %q", got, want)
	}
}

func TestChainNamesAndLayers(t *testing.T) {
	ssh := &stubHandler{name: "ssh"}
	docker := &stubHandler{name: "docker"}
	chain := NewChain([]Handler{ssh, docker})

	if got := chain.Name(); got != "ssh→docker" {
		t.Errorf("Name = %q, want %q", got, "ssh→docker")
	}
	if got := chain.Outer().Name(); got != "ssh" {
		t.Errorf("Outer = %q, want ssh", got)
	}
	if got := chain.Inner().Name(); got != "docker" {
		t.Errorf("Inner = %q, want docker", got)
	}
	layers := chain.Layers()
	if len(layers) != 2 || layers[0].Name() != "ssh" || layers[1].Name() != "docker" {
		t.Errorf("Layers = %v, want [ssh, docker]", layers)
	}
}

func TestChainPanicsOnTooFewLayers(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on single-layer chain")
		}
	}()
	NewChain([]Handler{&stubHandler{name: "ssh"}})
}

func TestChainAvailablePropagates(t *testing.T) {
	ssh := &stubHandler{name: "ssh"} // no error
	docker := &stubHandler{name: "docker", availErr: io.EOF}
	chain := NewChain([]Handler{ssh, docker})

	err := chain.Available()
	if err == nil {
		t.Fatal("expected an error when an inner layer is unavailable")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("error doesn't mention the failing layer: %v", err)
	}
}
