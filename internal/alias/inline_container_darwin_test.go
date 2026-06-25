//go:build darwin

package alias

import (
	"strings"
	"testing"
)

func TestParseInlineContainer(t *testing.T) {
	a, err := ParseInline("@container://drupal-app?user=www-data")
	if err != nil {
		t.Fatal(err)
	}
	if a.Handler.Type != "container" || a.Handler.Container == nil {
		t.Fatalf("handler: %+v", a.Handler)
	}
	c := a.Handler.Container
	if c.Container != "drupal-app" {
		t.Errorf("container = %q", c.Container)
	}
	if c.User != "www-data" {
		t.Errorf("user = %q", c.User)
	}
}

// TestParseInlineContainerRejectsHost — Apple container has no remote
// daemon. user@host syntax should be rejected loudly rather than
// silently dropped (it'd look like the docker scheme but wouldn't
// actually be tunnelled).
func TestParseInlineContainerRejectsHost(t *testing.T) {
	_, err := ParseInline("@container://gordon@host/drupal-app")
	if err == nil {
		t.Fatal("expected error for user@host in container URI")
	}
	if !strings.Contains(err.Error(), "no remote-host") {
		t.Errorf("error should explain the no-remote-host limitation: %v", err)
	}
}
