package project

import "testing"

// setTestHome points os.UserHomeDir() at `dir` for the duration of
// the test. On Unix this is HOME; on Windows it's USERPROFILE —
// Go's os.UserHomeDir reads whichever is appropriate for the GOOS,
// and tests that set only HOME silently miss Windows (the real
// user's home gets poked instead of the tmp dir, tests fail with
// "The system cannot find the path specified"). Setting both keeps
// the test platform-neutral.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
