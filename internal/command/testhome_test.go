package command

import "testing"

// setTestHome — see internal/project/testhome_test.go. Routes
// os.UserHomeDir() at `dir` cross-platform (HOME on Unix, USERPROFILE
// on Windows). Tests that touch ~/.dconsole/ MUST use this instead
// of raw t.Setenv("HOME", …) or they'll silently escape the tmp dir
// on Windows and corrupt the runner's real home.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
