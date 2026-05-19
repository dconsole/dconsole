package alias

import (
	"sort"
	"strings"
)

// lifecycleOrder maps an env name to its preferred sort key. Lower means
// earlier in the typical promotion flow (laptop → ... → production).
// Anything not in this map sorts AFTER all known envs, alphabetically,
// so unfamiliar names don't accidentally land in the middle of a list.
var lifecycleOrder = map[string]int{
	// "where I work"
	"self":         0,
	"local":        1,
	"laptop":       2,
	// feature / branch — code starts here, gets merged into dev
	"feature":      5,
	"branch":       6,
	"preview":      7,
	"integration":  8,
	// shared development trunk
	"dev":          10,
	"develop":      11,
	"development":  12,
	// QA / test
	"test":         30,
	"testing":      31,
	"qa":           32,
	// stage / pre-prod
	"stage":        40,
	"staging":      41,
	"preprod":      42,
	"pre-prod":     43,
	"prelive":      44,
	// production
	"prod":         50,
	"production":   51,
	"live":         52,
}

// SortByLifecycle sorts env names so the typical promotion flow reads
// top-to-bottom: local first, prod last. Unknown env names go to the
// end, alphabetically. The input slice is sorted in place.
func SortByLifecycle(envs []string) {
	sort.SliceStable(envs, func(i, j int) bool {
		oi, ki := lifecyclePosition(envs[i])
		oj, kj := lifecyclePosition(envs[j])
		if oi != oj {
			return oi < oj
		}
		return ki < kj
	})
}

// SortedByLifecycle returns a new sorted slice; original is untouched.
func SortedByLifecycle(envs []string) []string {
	out := make([]string, len(envs))
	copy(out, envs)
	SortByLifecycle(out)
	return out
}

func lifecyclePosition(name string) (order int, key string) {
	lower := strings.ToLower(name)
	if pos, ok := lifecycleOrder[lower]; ok {
		return pos, lower
	}
	return 1000, lower
}
