// Package provider hosts the registry shim for providers. The public
// extension API lives in github.com/dconsole/dconsole/pkg/provider; this
// package re-exports the symbols other dconsole packages already import
// here. There are no in-tree provider implementations — every provider
// arrives as a subprocess plugin via internal/provider/subprocess.go.
package provider

import (
	pkgprovider "github.com/dconsole/dconsole/pkg/provider"
)

// Re-exports from pkg/provider.

type (
	Provider     = pkgprovider.Provider
	Factory      = pkgprovider.Factory
	Registration = pkgprovider.Registration
	LoginCapable = pkgprovider.LoginCapable
)

var (
	ErrNotSupported       = pkgprovider.ErrNotSupported
	Register              = pkgprovider.Register
	Lookup                = pkgprovider.Lookup
	For                   = pkgprovider.For
	Names                 = pkgprovider.Names
	SetUnknownTypeHandler = pkgprovider.SetUnknownTypeHandler
)
