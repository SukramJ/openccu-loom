// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hub hosts the daemon's model of CCU "hub" entities:
// programs, system variables, install-mode, alarm and service
// messages, per-interface connectivity, and firmware-update status.
//
// Each entity exposes:
//
//   - A typed read surface with [Value] / [List] accessors that
//     return a snapshot plus an "observed" flag.
//   - Subscription hooks (`OnUpdate`) fired when the value changes.
//   - Command methods that delegate to narrow writer interfaces; the
//     HubCoordinator wires real implementations (ReGa scripts or
//     JSON-RPC) at composition time.
//
// The types are transport-agnostic: they have no dependency on the
// client/transport packages.
package hub
