package alias

import "testing"

func TestCheckSync_OpenByDefault(t *testing.T) {
	src := &Alias{Site: "x", Env: "prod"}
	dst := &Alias{Site: "x", Env: "dev"}
	if err := CheckSync(src, dst, false); err != nil {
		t.Errorf("expected default open policy, got %v", err)
	}
}

func TestCheckSync_TargetProtectedRefusesAll(t *testing.T) {
	src := &Alias{Site: "x", Env: "dev"}
	dst := &Alias{Site: "x", Env: "prod", Policy: Policy{SyncPolicy: "protected"}}
	err := CheckSync(src, dst, false)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	pe, ok := err.(*PolicyError)
	if !ok {
		t.Fatalf("expected *PolicyError, got %T", err)
	}
	if pe.Side != "target" {
		t.Errorf("rejection should come from target side, got %q", pe.Side)
	}
}

func TestCheckSync_TargetAllowsListedSource(t *testing.T) {
	src := &Alias{Site: "x", Env: "dev"}
	dst := &Alias{Site: "x", Env: "stage", Policy: Policy{AllowSyncFrom: []string{"dev", "ci"}}}
	if err := CheckSync(src, dst, false); err != nil {
		t.Errorf("dev should be allowed, got %v", err)
	}
}

func TestCheckSync_TargetRefusesUnlistedSource(t *testing.T) {
	src := &Alias{Site: "x", Env: "prod"}
	dst := &Alias{Site: "x", Env: "stage", Policy: Policy{AllowSyncFrom: []string{"dev", "ci"}}}
	if err := CheckSync(src, dst, false); err == nil {
		t.Error("prod should NOT be allowed to write into stage")
	}
}

func TestCheckSync_SourceRefusesUnlistedTarget(t *testing.T) {
	// Prod doesn't want anyone reading it except dev/stage.
	src := &Alias{Site: "x", Env: "prod", Policy: Policy{AllowSyncTo: []string{"dev", "stage"}}}
	ok := &Alias{Site: "x", Env: "dev"}
	bad := &Alias{Site: "x", Env: "scratch"}
	if err := CheckSync(src, ok, false); err != nil {
		t.Errorf("prod → dev should pass, got %v", err)
	}
	if err := CheckSync(src, bad, false); err == nil {
		t.Errorf("prod → scratch should be refused by source policy")
	} else if pe, _ := err.(*PolicyError); pe == nil || pe.Side != "source" {
		t.Errorf("refusal should come from source side, got %v", err)
	}
}

func TestCheckSync_ForceOverrides(t *testing.T) {
	src := &Alias{Site: "x", Env: "dev"}
	dst := &Alias{Site: "x", Env: "prod", Policy: Policy{SyncPolicy: "protected"}}
	if err := CheckSync(src, dst, true); err != nil {
		t.Errorf("--force should override, got %v", err)
	}
}

func TestValidatePolicyOverride(t *testing.T) {
	cases := []struct {
		name     string
		base     Policy
		override Policy
		wantErr  bool
	}{
		// --- accepted: no change / tighten ---
		{
			name:     "no_policy_either_side",
			base:     Policy{},
			override: Policy{},
		},
		{
			name:     "identical_protected",
			base:     Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
			override: Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
		},
		{
			name:     "tighten_open_to_protected",
			base:     Policy{SyncPolicy: "open"},
			override: Policy{SyncPolicy: "protected"},
		},
		{
			name:     "tighten_empty_sync_policy_to_protected",
			base:     Policy{},
			override: Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
		},
		{
			name:     "shrink_allow_sync_from",
			base:     Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev", "ci"}},
			override: Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
		},
		{
			name:     "shrink_allow_sync_to",
			base:     Policy{AllowSyncTo: []string{"dev", "stage", "scratch"}},
			override: Policy{AllowSyncTo: []string{"dev"}},
		},
		{
			name:     "tighten_list_to_empty_under_protected",
			base:     Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
			override: Policy{SyncPolicy: "protected"},
		},

		// --- refused: loosen ---
		{
			name:     "loosen_protected_to_open",
			base:     Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
			override: Policy{SyncPolicy: "open", AllowSyncFrom: []string{"dev"}},
			wantErr:  true,
		},
		{
			name:     "loosen_protected_to_empty",
			base:     Policy{SyncPolicy: "protected"},
			override: Policy{SyncPolicy: ""},
			wantErr:  true,
		},
		{
			name:     "grow_allow_sync_from",
			base:     Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
			override: Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev", "prod"}},
			wantErr:  true,
		},
		{
			name:     "drop_allow_sync_to_restriction",
			base:     Policy{AllowSyncTo: []string{"dev"}},
			override: Policy{AllowSyncTo: nil},
			wantErr:  true,
		},
		{
			name:     "grow_allow_sync_from_under_open",
			base:     Policy{AllowSyncFrom: []string{"dev"}},
			override: Policy{AllowSyncFrom: []string{"dev", "scratch"}},
			wantErr:  true,
		},
		{
			name:     "add_entry_to_empty_protected_list",
			base:     Policy{SyncPolicy: "protected"},
			override: Policy{SyncPolicy: "protected", AllowSyncFrom: []string{"dev"}},
			wantErr:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePolicyOverride(tc.base, tc.override)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
		})
	}
}
