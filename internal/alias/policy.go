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
