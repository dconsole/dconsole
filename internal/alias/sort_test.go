package alias

import (
	"reflect"
	"testing"
)

func TestSortByLifecycle(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "NAA layout",
			in:   []string{"feature", "local", "prod", "test"},
			want: []string{"local", "feature", "test", "prod"},
		},
		{
			name: "feature comes before dev",
			in:   []string{"prod", "dev", "feature", "local", "test"},
			want: []string{"local", "feature", "dev", "test", "prod"},
		},
		{
			name: "common four-env flow",
			in:   []string{"prod", "stage", "dev", "local"},
			want: []string{"local", "dev", "stage", "prod"},
		},
		{
			name: "unknowns sort to end alphabetically",
			in:   []string{"banana", "prod", "apple", "dev"},
			want: []string{"dev", "prod", "apple", "banana"},
		},
		{
			name: "case insensitive",
			in:   []string{"PROD", "Local", "Dev"},
			want: []string{"Local", "Dev", "PROD"},
		},
		{
			name: "staging variants",
			in:   []string{"production", "staging", "qa", "develop", "self"},
			want: []string{"self", "develop", "qa", "staging", "production"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SortedByLifecycle(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("\n  got  %q\n  want %q", got, c.want)
			}
		})
	}
}
