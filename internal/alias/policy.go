package alias

import (
	"fmt"
	"strings"
)

// PolicyError is returned when a sync is refused by a policy. The error
// message names the rejecting side so the operator knows where to look.
type PolicyError struct {
	Side    string // "source" or "target"
	From    string // source alias label
	To      string // target alias label
	Reason  string
}

func (e *PolicyError) Error() string {
	return fmt.Sprintf("policy refused %s → %s on the %s side: %s (pass --force to override)",
		e.From, e.To, e.Side, e.Reason)
}

// CheckSync verifies that `source` is allowed to flow into `target`. Both
// sides' Policy blocks are consulted; the most restrictive wins. Returns
// nil if the sync is allowed (or if force=true).
func CheckSync(source, target *Alias, force bool) error {
	if force {
		return nil
	}
	if err := checkSourceSide(source, target); err != nil {
		return err
	}
	if err := checkTargetSide(source, target); err != nil {
		return err
	}
	return nil
}

func checkSourceSide(source, target *Alias) error {
	p := source.Policy
	if p.SyncPolicy == "protected" || len(p.AllowSyncTo) > 0 {
		// Source has restricted who can pull from it.
		if !contains(p.AllowSyncTo, target.Env) {
			return &PolicyError{
				Side:   "source",
				From:   labelOf(source),
				To:     labelOf(target),
				Reason: refusalReason("allow_sync_to", target.Env, p.AllowSyncTo, p.SyncPolicy),
			}
		}
	}
	return nil
}

func checkTargetSide(source, target *Alias) error {
	p := target.Policy
	if p.SyncPolicy == "protected" || len(p.AllowSyncFrom) > 0 {
		if !contains(p.AllowSyncFrom, source.Env) {
			return &PolicyError{
				Side:   "target",
				From:   labelOf(source),
				To:     labelOf(target),
				Reason: refusalReason("allow_sync_from", source.Env, p.AllowSyncFrom, p.SyncPolicy),
			}
		}
	}
	return nil
}

func refusalReason(listName, env string, list []string, mode string) string {
	if mode == "protected" && len(list) == 0 {
		return "sync_policy: protected (no envs in " + listName + ")"
	}
	if len(list) == 0 {
		return listName + " is empty"
	}
	return fmt.Sprintf("%q is not in %s: %s", env, listName, strings.Join(list, ", "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func labelOf(a *Alias) string { return "@" + a.Site + "." + a.Env }

// ValidatePolicyOverride reports whether override's Policy would loosen
// base's: a per-machine .dconsole.override.yml must only be able to make
// policy stricter (or equal), never weaker. This is accident-prevention,
// not a security boundary — the committed manifest is editable by anyone
// with write access to the repo. The point is to catch the "I just
// wanted to swap my ssh key" override that quietly also drops the
// sync_policy block and un-protects prod.
//
// Returns nil if override tightens or matches base, else an error naming
// which field loosened. Both inputs are post-merge values (base comes
// from the committed env, override comes from the per-machine env block
// as it appears in the override file).
func ValidatePolicyOverride(base, override Policy) error {
	// 1. sync_policy strictness is monotone: "" ≡ "open" (no restriction)
	//    < "protected". The override may raise the rank but not lower it.
	if policyRank(override.SyncPolicy) < policyRank(base.SyncPolicy) {
		return fmt.Errorf("sync_policy: override sets %q but base is %q — overrides can only tighten policy, not loosen it (edit the base manifest if this is intentional)",
			displayPolicy(override.SyncPolicy), displayPolicy(base.SyncPolicy))
	}
	// 2. allow_sync_from / allow_sync_to lists can only shrink. The
	//    effective "permitted env set" depends on sync_policy + list, so
	//    we compare that set rather than list contents directly.
	if err := validateAllowListOverride("allow_sync_from",
		base.SyncPolicy, base.AllowSyncFrom,
		override.SyncPolicy, override.AllowSyncFrom); err != nil {
		return err
	}
	if err := validateAllowListOverride("allow_sync_to",
		base.SyncPolicy, base.AllowSyncTo,
		override.SyncPolicy, override.AllowSyncTo); err != nil {
		return err
	}
	return nil
}

// policyRank maps a sync_policy string to a strictness rank. Higher = stricter.
func policyRank(p string) int {
	switch p {
	case "protected":
		return 1
	default: // "" and "open" both mean no restriction
		return 0
	}
}

func displayPolicy(p string) string {
	if p == "" {
		return "open"
	}
	return p
}

// validateAllowListOverride enforces that the set of envs the override
// permits (via sync_policy + list) is a subset of what base permits.
// Semantics match CheckSync: the list activates when sync_policy is
// "protected" OR the list is non-empty.
func validateAllowListOverride(fieldName, basePolicy string, baseList []string, ovPolicy string, ovList []string) error {
	baseUnrestricted := isUnrestricted(basePolicy, baseList)
	ovUnrestricted := isUnrestricted(ovPolicy, ovList)
	if baseUnrestricted {
		// Base permits every env — any override list (even empty) can only
		// tighten or match.
		return nil
	}
	if ovUnrestricted {
		return fmt.Errorf("%s: base restricts to %v but the override would drop that restriction — overrides can only tighten policy (edit the base manifest if this is intentional)",
			fieldName, baseList)
	}
	// Both restricted: override list must be a subset of base list.
	for _, env := range ovList {
		if !contains(baseList, env) {
			return fmt.Errorf("%s: override adds %q which is not in the base list %v — overrides can only remove entries, not add them (edit the base manifest if this is intentional)",
				fieldName, env, baseList)
		}
	}
	return nil
}

// isUnrestricted reports whether a (sync_policy, list) pair permits every
// env. Mirrors CheckSync's trigger: the list activates when policy is
// "protected" OR the list is non-empty; otherwise there's no restriction.
func isUnrestricted(policy string, list []string) bool {
	if policy == "protected" {
		return false
	}
	return len(list) == 0
}
