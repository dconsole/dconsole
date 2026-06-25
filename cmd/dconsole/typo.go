package main

import "strings"

// dconsoleVerbs is the canonical list of dconsole-side commands. Used
// for typo suggestions: when a user runs `dconsole foo:bar` and it
// doesn't match a built-in, we check whether it's within edit distance
// 2 of one of these and suggest the closest. Drush itself has many
// `*:*` commands (cache:rebuild, pm:enable, …) which is why we only
// suggest when the typo is close — distant `*:*` strings fall through
// to drush as before.
//
// Keep this in sync with the case statements in main.go's command
// dispatch switch.
var dconsoleVerbs = []string{
	"site:alias",
	"transport:list",
	"sql:sync",
	"sql:cache",
	"assets:cache",
	"rsync",
	"alias:convert",
	"alias:dump",
	"project:register",
	"project:list",
	"project:forget",
	"project:init",
	"inspect",
	"plugin",
}

// suggestVerb returns the closest dconsole built-in to `arg` if (a)
// `arg` looks like an intended dconsole verb (contains `:`) and (b)
// the closest built-in is within Levenshtein distance 2. Returns ""
// when nothing close enough exists — caller should then fall through
// to the normal drush-forwarding path.
func suggestVerb(arg string) string {
	if !strings.Contains(arg, ":") {
		return ""
	}
	best := ""
	bestDist := 3
	for _, v := range dconsoleVerbs {
		d := levenshtein(arg, v)
		if d < bestDist {
			best = v
			bestDist = d
		}
	}
	return best
}

// levenshtein returns the edit distance between two strings. Plain
// dynamic-programming implementation — strings here are short verb
// names (under 20 chars) so allocation cost is negligible.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
