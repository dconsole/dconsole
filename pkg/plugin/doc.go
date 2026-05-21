// SPDX-License-Identifier: Apache-2.0

// Package plugin defines the wire protocol between dconsole and its
// subprocess plugins. Plugins are separate binaries named
// "dconsole-<type>" that implement the verbs documented here.
//
// dconsole spawns a plugin once per operation (no daemon) with one of
// these verbs as argv[1]. Most invocations also receive
// --alias-json=<filepath>, a JSON file containing the alias the
// operation is targeting. Stdio is wired straight through so streaming
// (Pipe) and interactive (Shell) behavior come for free.
//
// Wire compatibility is gated by an integer ProtocolVersion returned by
// the "plugin-info" verb. dconsole refuses plugins whose ProtocolVersion
// it doesn't recognise.
package plugin
