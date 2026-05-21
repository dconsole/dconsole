// SPDX-License-Identifier: Apache-2.0

// Package provider is the public extension API for dconsole providers.
//
// A provider integrates a hosting service (Skpr, Pantheon, Acquia,
// Platform.sh, …) whose preferred data-movement story differs from the
// generic "drush sql:dump on source" approach — they typically offer
// pre-computed backups via their own CLI or API.
//
// Built-in providers register themselves at process start via Register.
// Out-of-tree providers are shipped as separate binaries named
// "dconsole-<type>" that dconsole discovers and drives via the wire
// protocol defined in github.com/dconsole/dconsole/pkg/plugin.
//
// Plugin authors writing a Go SDK for their provider plugin should
// depend on this package, not on dconsole's internal/* packages.
package provider
