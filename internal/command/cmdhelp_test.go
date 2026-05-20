package command

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderCommandHelp_RsyncSpec(t *testing.T) {
	var out bytes.Buffer
	RenderCommandHelp(&out, RsyncSpec())
	got := out.String()
	// Each section header should be present, in drush 13 order.
	wantInOrder := []string{
		"Description:",
		"Usage:",
		"Arguments:",
		"Options:",
		"Help:",
	}
	prev := -1
	for _, s := range wantInOrder {
		idx := strings.Index(got, s)
		if idx < 0 {
			t.Errorf("missing section %q in output:\n%s", s, got)
			continue
		}
		if idx <= prev {
			t.Errorf("section %q appears out of order", s)
		}
		prev = idx
	}
	// Each argument and option should be listed.
	for _, needle := range []string{
		"src", "dst",
		"-v, --verbose",
		"--force",
		"%files", "%root", "%private",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("output missing %q", needle)
		}
	}
}

func TestRenderCommandHelp_SqlSyncSpec(t *testing.T) {
	var out bytes.Buffer
	RenderCommandHelp(&out, SqlSyncSpec())
	got := out.String()
	for _, needle := range []string{
		"Dump a source database",
		"<@source.env> <@target.env>",
		"--keep-dump",
		"--dump-path=PATH",
		"drush sql:dump",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("output missing %q", needle)
		}
	}
}

func TestArgsHaveHelpFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"none", []string{"@x", "@y"}, false},
		{"--help present", []string{"@x", "--help"}, true},
		{"-h present", []string{"-h"}, true},
		{"help as positional", []string{"help"}, true},
		{"help anywhere", []string{"@x", "@y", "--help"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ArgsHaveHelpFlag(c.args); got != c.want {
				t.Errorf("ArgsHaveHelpFlag(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestOptionTag_FormatsCorrectly(t *testing.T) {
	cases := []struct {
		opt  OptionSpec
		want string
	}{
		{OptionSpec{Short: "v", Long: "verbose"}, "-v, --verbose"},
		{OptionSpec{Long: "force"}, "    --force"},
		{OptionSpec{Long: "dump-path", ValueName: "PATH"}, "    --dump-path=PATH"},
		{OptionSpec{Short: "u", Long: "uri", ValueName: "URL"}, "-u, --uri=URL"},
	}
	for _, c := range cases {
		if got := optionTag(c.opt); got != c.want {
			t.Errorf("optionTag(%+v) = %q, want %q", c.opt, got, c.want)
		}
	}
}
