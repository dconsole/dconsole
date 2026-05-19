// Package transport hosts the registry for in-tree transports. The
// public extension API lives in github.com/heydon/dconsole/pkg/transport;
// this package re-exports the symbols other dconsole packages already
// import here, plus the init() registrations for the built-in transports
// (ssh, ddev, docker, kubectl, lando, compose, exec).
package transport

import (
	pkgtransport "github.com/heydon/dconsole/pkg/transport"
)

// Re-exports from pkg/transport so existing internal callers (forward,
// sqlsync, rsync, …) can keep importing internal/transport.

type (
	Transport    = pkgtransport.Transport
	Factory      = pkgtransport.Factory
	Registration = pkgtransport.Registration
	Stdio        = pkgtransport.Stdio
)

var (
	Register             = pkgtransport.Register
	Lookup               = pkgtransport.Lookup
	For                  = pkgtransport.For
	Names                = pkgtransport.Names
	ProbeAvailable       = pkgtransport.ProbeAvailable
	RequiredCLI          = pkgtransport.RequiredCLI
	CLIAvailable         = pkgtransport.CLIAvailable
	DefaultStdio         = pkgtransport.DefaultStdio
	SetUnknownTypeHandler = pkgtransport.SetUnknownTypeHandler
)
