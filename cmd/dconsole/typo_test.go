package main

import "testing"

func TestSuggestVerb(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// One-character typos of every category get caught.
		{"transpost:list", "transport:list"},
		{"transport:lst", "transport:list"},
		{"sql:snc", "sql:sync"},
		{"sql:cahe", "sql:cache"},
		{"assets:cache", "assets:cache"}, // exact match returns itself
		{"project:lst", "project:list"},
		{"alias:dmp", "alias:dump"},

		// No colon → not a candidate for verb suggestion at all.
		// `rsync` could match `rsync` but suggestVerb gates on `:`
		// so freeform commands like `cr`, `status` fall through to drush.
		{"rsynk", ""},
		{"plgin", ""},
		{"cr", ""},

		// Genuine drush commands with `:` that aren't close to any
		// dconsole built-in must NOT get a suggestion (else we'd block
		// users from running them).
		{"cache:rebuild", ""},
		{"pm:enable", ""},
		{"user:login", ""},
		{"updb", ""},

		// Distance > 2 — no suggestion.
		{"foo:bar", ""},
		{"xyz:abc", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := suggestVerb(c.in)
			if got != c.want {
				t.Errorf("suggestVerb(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"transport:list", "transpost:list", 1},
		{"sql:sync", "sql:snc", 1},
	}
	for _, c := range cases {
		got := levenshtein(c.a, c.b)
		if got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
