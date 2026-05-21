// SPDX-License-Identifier: Apache-2.0

// Package transport is the public extension API for dconsole transports.
//
// A transport reaches a remote environment described by an Alias. Built-in
// transports (ssh, ddev, docker, kubectl, lando, compose, exec) register
// themselves at process start via Register. Out-of-tree transports are
// shipped as separate binaries named "dconsole-<type>" that dconsole
// discovers in ~/.dconsole/plugins/ or $PATH and drives via the wire
// protocol defined in github.com/dconsole/dconsole/pkg/plugin.
//
// Plugin authors writing a Go SDK for their plugin should depend on this
// package, not on dconsole's internal/* packages.
package transport
