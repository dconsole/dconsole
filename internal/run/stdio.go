package run

import (
	"io"
	"os"
)

// Stdio bundles the three I/O streams for a child process. Nil values
// default to os.Stdin/Stdout/Stderr at exec time.
type Stdio struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// DefaultStdio wires the child to the dconsole process's own streams.
func DefaultStdio() Stdio {
	return Stdio{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}
