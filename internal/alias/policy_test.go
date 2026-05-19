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
