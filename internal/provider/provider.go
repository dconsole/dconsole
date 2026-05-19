// Package provider hosts the registry for in-tree providers. The public
// extension API lives in github.com/heydon/dconsole/pkg/provider; this
// package re-exports the symbols other dconsole packages already import
// here, plus the init() registrations for built-in providers (ironstar).
package provider

import (
	pkgprovider "github.com/heydon/dconsole/pkg/provider"
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
