// Package run keeps a backward-compatible re-export of Stdio for callers
// that import internal/run directly. The canonical definition lives in
// github.com/heydon/dconsole/pkg/transport.
package run

import "github.com/heydon/dconsole/pkg/transport"

// Stdio is an alias for transport.Stdio.
type Stdio = transport.Stdio

// DefaultStdio re-exports transport.DefaultStdio.
var DefaultStdio = transport.DefaultStdio
