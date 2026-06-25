//go:build darwin

package alias

import (
	"strings"
	"testing"
)

// TestParseContainerComposeURI — happy-path coverage for both URI
// shapes (absolute path → project_dir; bare name → project_name) plus
// optional ?user=, ?root=, and the rejection cases.
func TestParseContainerComposeURI(t *testing.T) {
	cases := []struct {
		name        string
		token       string
		wantProject string // ProjectName OR basename(ProjectDir)
		wantService string
		wantUser    string
		wantRoot    string
		wantErr     string
	}{
		{
			name:        "absolute path → project_dir",
			token:       "@container-compose:///Users/me/heydon?service=php",
			wantProject: "heydon", // basename
			wantService: "php",
		},
		{
			name:        "bare host → project_name",
			token:       "@container-compose://hc?service=php",
			wantProject: "hc",
			wantService: "php",
		},
		{
			name:        "with user override",
			token:       "@container-compose://hc?service=php&user=www-data",
			wantProject: "hc",
			wantService: "php",
			wantUser:    "www-data",
		},
		{
			name:        "with root for chdir",
			token:       "@container-compose:///opt/proj?service=php&root=/var/www/html",
			wantProject: "proj",
			wantService: "php",
			wantRoot:    "/var/www/html",
		},
		{
			name:    "missing service is an error",
			token:   "@container-compose://hc",
			wantErr: "service",
		},
		{
			name:    "userinfo prefix is rejected (no remote-host concept)",
			token:   "@container-compose://me@hc?service=php",
			wantErr: "user@",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseInline(c.token)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q; got nil", c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseInline: %v", err)
			}
			if got.Handler.Type != "container-compose" {
				t.Errorf("handler type = %q, want container-compose", got.Handler.Type)
			}
			cc := got.Handler.ContainerCompose
			if cc == nil {
				t.Fatal("Handler.ContainerCompose is nil")
			}
			// Project assertion: either ProjectName matches OR the
			// basename of ProjectDir matches.
			project := cc.ProjectName
			if project == "" {
				// crude basename — fine for test fixtures
				idx := strings.LastIndex(cc.ProjectDir, "/")
				if idx >= 0 {
					project = cc.ProjectDir[idx+1:]
				} else {
					project = cc.ProjectDir
				}
			}
			if project != c.wantProject {
				t.Errorf("project = %q (from ProjectName=%q ProjectDir=%q), want %q",
					project, cc.ProjectName, cc.ProjectDir, c.wantProject)
			}
			if cc.Service != c.wantService {
				t.Errorf("Service = %q, want %q", cc.Service, c.wantService)
			}
			if cc.User != c.wantUser {
				t.Errorf("User = %q, want %q", cc.User, c.wantUser)
			}
			if got.Root != c.wantRoot {
				t.Errorf("Root = %q, want %q", got.Root, c.wantRoot)
			}
		})
	}
}
