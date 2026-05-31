// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package coordinators hosts the per-central coordinators from
// SPECIFICATION §11.3. Each coordinator is a small, focused actor that
// consumes events from the internal bus and acts on a specific domain
// (cache, clients, events, devices, hub, connection recovery).
//
// Coordinators are owned by [*central.CentralUnit] — they have a 1:1
// relationship with their central. Instances are safe for concurrent
// use from multiple handlers.
package coordinators
