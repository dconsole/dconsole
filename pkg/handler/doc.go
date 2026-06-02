// SPDX-License-Identifier: Apache-2.0

// Package handler is the unified extension API for everything dconsole
// can do against an alias — connection, command forwarding, and
// platform-specific high-level operations (DB sync, file sync, login,
// …). It replaces and unifies the older pkg/transport (connections)
// and pkg/provider (platform ops) abstractions.
//
// The unification mirrors how the underlying tools actually work: ssh,
// ddev, lando, ahoy, docker, kubectl, skpr, iron, pantheon — all of
// them are "spawn a local CLI subprocess that knows how to run drush
// in its environment". The differences between them (network vs
// container vs hosting-platform-API) live below dconsole's abstraction
// boundary, not at it.
//
// A Handler implements the mandatory Wrap / Exec / Pipe / Shell verbs
// for argv forwarding, then optionally implements duck-typed
// capability interfaces (DBImporter, DBSyncer, LoginCapable, …) for
// any high-level operations the underlying tool natively supports.
//
// Handlers are composable. A `handler:` block in alias YAML may carry
// a `via:` field whose value is itself a handler config, producing a
// chain — e.g. ssh outer, docker inner, for "ssh into the server, then
// docker-exec into the container". See Chain.
package handler
