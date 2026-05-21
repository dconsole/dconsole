// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"io"
	"os"
)

// Stdio bundles the three I/O streams for a child process. Nil fields
// default to the dconsole process's own streams at exec time.
type Stdio struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// DefaultStdio wires a child to the dconsole process's own streams.
func DefaultStdio() Stdio {
	return Stdio{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}
