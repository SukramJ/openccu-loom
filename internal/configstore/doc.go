// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package configstore is the higher-level facade over the
// SQLite-backed config tables. It marries the bootstrap-tier YAML
// ([config.BootstrapConfig]) with the DB-tier sections + centrals
// + secret env-overlay into one [config.Config] the daemon can
// consume.
//
// Responsibilities:
//
//   - Section JSON load/save with typed validation (one method per
//     section, e.g. [Store.MQTT], [Store.Matter]).
//   - Effective-config assembly: bootstrap defaults + DB-overrides +
//     env-resolution for secrets, producing one [config.Config]
//     struct compatible with the existing daemon wiring.
//   - Source-attribution map for the Wave-C schema endpoint:
//     reports each field's effective source (bootstrap / db / env /
//     default) so the SPA can render the source pill.
//   - Read-only / read-write detection: probes whether the
//     bootstrap YAML and the data-dir are writable; the SPA surfaces
//     this as the LiveEdit capability.
package configstore
