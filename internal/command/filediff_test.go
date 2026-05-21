package command

import (
	"reflect"
	"sort"
	"testing"

	"github.com/dconsole/dconsole/internal/alias"
)

// fakeAlias is a minimal Alias instance for orchestrator-detection
// tests that only inspect endpoint structure, not transport behaviour.
var fakeAlias = alias.Alias{Site: "demo", Env: "test"}

func TestParseFindOutput_BasicLines(t *testing.T) {
	body := []byte(
		"foo.txt\t1234567890.123\t512\n" +
			"sub/bar.png\t1234567999.0\t2048\n" +
			"baz.pdf\t1234567000.0\t102400\n",
	)
	m, err := parseFindOutput(body)
	if err != nil {
		t.Fatal(err)
	}
	want := FileMap{
		"foo.txt":     {Path: "foo.txt", MTime: 1234567890, Size: 512},
		"sub/bar.png": {Path: "sub/bar.png", MTime: 1234567999, Size: 2048},
		"baz.pdf":     {Path: "baz.pdf", MTime: 1234567000, Size: 102400},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("parseFindOutput:\n  got  %v\n  want %v", m, want)
	}
}

func TestParseFindOutput_SkipsBlanksAndMalformed(t *testing.T) {
	body := []byte(
		"foo.txt\t100\t10\n" +
			"\n" + // blank line
			"only-two-fields\t100\n" + // malformed — skipped
			"bar.txt\t200\t20\n",
	)
	m, err := parseFindOutput(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Errorf("expected 2 entries, got %d (%v)", len(m), m)
	}
	if _, ok := m["only-two-fields"]; ok {
		t.Errorf("malformed line shouldn't have produced an entry")
	}
}

func TestParseFindOutput_PathWithSpaces(t *testing.T) {
	// `find -printf` produces tab-separated output; paths with spaces
	// are fine as long as they don't contain tabs.
	body := []byte("path with spaces.jpg\t100\t1234\n")
	m, _ := parseFindOutput(body)
	if e, ok := m["path with spaces.jpg"]; !ok || e.Size != 1234 {
		t.Errorf("path with spaces lost: %v", m)
	}
}

func TestDiffSets(t *testing.T) {
	cases := []struct {
		name           string
		source, target FileMap
		wantChanged    []string
		wantDeleted    []string
	}{
		{
			name:        "identical",
			source:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}},
			target:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}},
			wantChanged: nil,
			wantDeleted: nil,
		},
		{
			name:        "new on source",
			source:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}, "b": {Path: "b", MTime: 200, Size: 20}},
			target:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}},
			wantChanged: []string{"b"},
			wantDeleted: nil,
		},
		{
			name:        "source newer",
			source:      FileMap{"a": {Path: "a", MTime: 200, Size: 10}},
			target:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}},
			wantChanged: []string{"a"},
			wantDeleted: nil,
		},
		{
			name:        "size differs",
			source:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}},
			target:      FileMap{"a": {Path: "a", MTime: 100, Size: 20}},
			wantChanged: []string{"a"},
			wantDeleted: nil,
		},
		{
			name:        "target newer — left alone",
			source:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}},
			target:      FileMap{"a": {Path: "a", MTime: 999, Size: 10}},
			wantChanged: nil,
			wantDeleted: nil,
		},
		{
			name:        "only on target",
			source:      FileMap{},
			target:      FileMap{"a": {Path: "a", MTime: 100, Size: 10}, "b": {Path: "b", MTime: 100, Size: 10}},
			wantChanged: nil,
			wantDeleted: []string{"a", "b"},
		},
		{
			name:        "mixed",
			source:      FileMap{"keep": {Path: "keep", MTime: 100, Size: 10}, "new": {Path: "new", MTime: 100, Size: 10}},
			target:      FileMap{"keep": {Path: "keep", MTime: 100, Size: 10}, "delete": {Path: "delete", MTime: 100, Size: 10}},
			wantChanged: []string{"new"},
			wantDeleted: []string{"delete"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotChanged, gotDeleted := diffSets(c.source, c.target)
			sort.Strings(gotChanged)
			sort.Strings(gotDeleted)
			sort.Strings(c.wantChanged)
			sort.Strings(c.wantDeleted)
			if !sliceEqualOrBothEmpty(gotChanged, c.wantChanged) {
				t.Errorf("changed:\n  got  %v\n  want %v", gotChanged, c.wantChanged)
			}
			if !sliceEqualOrBothEmpty(gotDeleted, c.wantDeleted) {
				t.Errorf("deletedOnTarget:\n  got  %v\n  want %v", gotDeleted, c.wantDeleted)
			}
		})
	}
}

func sliceEqualOrBothEmpty(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func TestSummarise(t *testing.T) {
	src := FileMap{
		"a": {Path: "a"}, "b": {Path: "b"}, "c": {Path: "c"}, "d": {Path: "d"},
	}
	changed := []string{"a", "b"}
	deleted := []string{"x"}
	s := Summarise(src, changed, deleted)
	if s.Changed != 2 || s.DeletedOnTarget != 1 || s.UnchangedFiles != 2 {
		t.Errorf("Summarise = %+v; want {Changed:2 DeletedOnTarget:1 UnchangedFiles:2}", s)
	}
}

func TestEffectivePathspecs(t *testing.T) {
	cases := []struct {
		name string
		ps   string
		opts RsyncOpts
		want []string
	}{
		{"default", "", RsyncOpts{}, []string{"%files"}},
		{"include-private", "", RsyncOpts{IncludePrivate: true}, []string{"%files", "%private"}},
		{"explicit endpoint pathspec", "%private", RsyncOpts{}, []string{"%private"}},
		{"override wins", "%files", RsyncOpts{PathspecOverride: []string{"%private"}}, []string{"%private"}},
		{"override list", "", RsyncOpts{PathspecOverride: []string{"%files", "%private"}}, []string{"%files", "%private"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := effectivePathspecs(c.ps, c.opts)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsOrchestratable(t *testing.T) {
	aliasEP := func() *Endpoint {
		// Endpoint with .Alias set is what the orchestrator looks for.
		return &Endpoint{Alias: nil, PathSpec: ""}
	}
	_ = aliasEP

	cases := []struct {
		name string
		src  *Endpoint
		dst  *Endpoint
		want bool
	}{
		{"both local", &Endpoint{Local: "/a"}, &Endpoint{Local: "/b"}, false},
		{"freeform path on alias", &Endpoint{Alias: &fakeAlias, PathSpec: "/abs/path"}, &Endpoint{Alias: &fakeAlias, PathSpec: "%files"}, false},
		{"both %files", &Endpoint{Alias: &fakeAlias, PathSpec: "%files"}, &Endpoint{Alias: &fakeAlias, PathSpec: "%files"}, true},
		{"both empty (default %files)", &Endpoint{Alias: &fakeAlias}, &Endpoint{Alias: &fakeAlias}, true},
		{"%private on both", &Endpoint{Alias: &fakeAlias, PathSpec: "%private"}, &Endpoint{Alias: &fakeAlias, PathSpec: "%private"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isOrchestratable(c.src, c.dst)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
