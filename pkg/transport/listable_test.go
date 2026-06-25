// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"errors"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
)

// TestListableNames_HideWhenMissing — a handler registered with
// HideWhenMissing=true and an unavailable RequiredCLI is dropped
// from ListableNames; one without HideWhenMissing stays even when
// its CLI is missing.
func TestListableNames_HideWhenMissing(t *testing.T) {
	// Register two test-only handlers. Both require a CLI that
	// definitely isn't on PATH (a UUIDish string). One opts into
	// HideWhenMissing; the other doesn't.
	missingCLI := "definitely-not-a-real-cli-test-only-name"
	Register("test-hidden", Registration{
		RequiredCLI:     missingCLI,
		HideWhenMissing: true,
		Build:           func(*alias.Alias) (Transport, error) { return nil, errors.New("test") },
	})
	Register("test-visible", Registration{
		RequiredCLI:     missingCLI,
		HideWhenMissing: false,
		Build:           func(*alias.Alias) (Transport, error) { return nil, errors.New("test") },
	})
	t.Cleanup(func() {
		registryMu.Lock()
		delete(registry, "test-hidden")
		delete(registry, "test-visible")
		registryMu.Unlock()
	})

	names := ListableNames()
	have := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if have("test-hidden") {
		t.Error("test-hidden should be filtered out (HideWhenMissing=true + CLI absent)")
	}
	if !have("test-visible") {
		t.Error("test-visible should be present (HideWhenMissing=false → always listed)")
	}
}

// TestListableNames_AvailableHandlersAlwaysShown — a HideWhenMissing
// handler whose CLI IS present must still appear (the flag only
// hides when the CLI is missing, not when it's present).
func TestListableNames_AvailableHandlersAlwaysShown(t *testing.T) {
	Register("test-empty-cli", Registration{
		// RequiredCLI empty → always considered available.
		HideWhenMissing: true,
		Build:           func(*alias.Alias) (Transport, error) { return nil, errors.New("test") },
	})
	t.Cleanup(func() {
		registryMu.Lock()
		delete(registry, "test-empty-cli")
		registryMu.Unlock()
	})

	found := false
	for _, n := range ListableNames() {
		if n == "test-empty-cli" {
			found = true
			break
		}
	}
	if !found {
		t.Error("test-empty-cli should be listed (no RequiredCLI → never hidden)")
	}
}
