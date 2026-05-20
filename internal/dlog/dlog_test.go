package dlog

import (
	"bytes"
	"strings"
	"testing"
)

func TestExtractFromArgs(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		wantArgs []string
		wantLvl  Level
	}{
		{"empty", nil, nil, Off},
		{"none", []string{"@x", "cr"}, []string{"@x", "cr"}, Off},
		{"-v at front", []string{"-v", "@x", "cr"}, []string{"@x", "cr"}, V},
		{"--verbose at front", []string{"--verbose", "@x"}, []string{"@x"}, V},
		{"-vv", []string{"-vv", "@x"}, []string{"@x"}, VV},
		{"-vvv", []string{"-vvv", "@x"}, []string{"@x"}, VVV},
		{"--debug aliases vvv", []string{"--debug", "@x"}, []string{"@x"}, VVV},
		{"-v after @x is left alone", []string{"@x", "-v"}, []string{"@x", "-v"}, Off},
		{"multiple flags pick highest", []string{"-v", "-vvv", "-vv", "@x"}, []string{"@x"}, VVV},
		{"absent → Off, args unchanged", []string{"sql:sync", "@a", "@b"}, []string{"sql:sync", "@a", "@b"}, Off},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, lvl := ExtractFromArgs(c.in)
			if lvl != c.wantLvl {
				t.Errorf("level = %v, want %v", lvl, c.wantLvl)
			}
			if !sliceEqual(got, c.wantArgs) {
				t.Errorf("remaining args = %q, want %q", got, c.wantArgs)
			}
		})
	}
}

func TestDrushFlags(t *testing.T) {
	cases := []struct {
		lvl  Level
		want []string
	}{
		{Off, nil},
		{V, []string{"-v"}},
		{VV, []string{"-vv"}},
		{VVV, []string{"-vvv"}},
	}
	prev := GetLevel()
	defer SetLevel(prev)
	for _, c := range cases {
		SetLevel(c.lvl)
		got := DrushFlags()
		if !sliceEqual(got, c.want) {
			t.Errorf("DrushFlags(%v) = %v, want %v", c.lvl, got, c.want)
		}
	}
}

func TestCmdfRespectsLevel(t *testing.T) {
	prev := GetLevel()
	defer SetLevel(prev)
	var buf bytes.Buffer
	SetSink(&buf)
	defer SetSink(nil)

	SetLevel(Off)
	Cmdf([]string{"drush", "status"})
	if buf.Len() != 0 {
		t.Errorf("Cmdf at Off should be silent; got %q", buf.String())
	}

	SetLevel(V)
	Cmdf([]string{"drush", "sql:dump", "--gzip"})
	out := buf.String()
	if !strings.Contains(out, "→ drush sql:dump --gzip") {
		t.Errorf("expected spawn line in output:\n%s", out)
	}

	// Spaces and special chars get quoted.
	buf.Reset()
	Cmdf([]string{"ssh", "user@host", "drush sql:dump --uri=https://x.test"})
	out = buf.String()
	if !strings.Contains(out, `"drush sql:dump --uri=https://x.test"`) {
		t.Errorf("expected quoted arg:\n%s", out)
	}
}

func TestInfofAtVV(t *testing.T) {
	prev := GetLevel()
	defer SetLevel(prev)
	var buf bytes.Buffer
	SetSink(&buf)
	defer SetSink(nil)

	SetLevel(V)
	Infof("cache hit: %s", "/tmp/x")
	if buf.Len() != 0 {
		t.Errorf("Infof at level V should be silent; got %q", buf.String())
	}
	SetLevel(VV)
	Infof("cache hit: %s", "/tmp/x")
	if !strings.Contains(buf.String(), "cache hit: /tmp/x") {
		t.Errorf("Infof at level VV should emit; got %q", buf.String())
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
