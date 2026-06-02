// SPDX-License-Identifier: Apache-2.0

package handler

import "github.com/dconsole/dconsole/pkg/transport"

// Stdio is a type alias to pkg/transport.Stdio while both packages
// coexist during the v0.4.0 migration. Once pkg/transport is removed,
// the canonical definition will move here.
type Stdio = transport.Stdio

// DefaultStdio re-exports transport.DefaultStdio for the same reason.
var DefaultStdio = transport.DefaultStdio
