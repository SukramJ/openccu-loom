// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hmevent defines the domain events the daemon publishes on
// its internal EventBus. The bus is generic — each event type is a
// concrete struct — so there is no runtime polymorphism penalty; the
// compiler enforces payload correctness at every subscribe / publish
// site.
//
// Every event carries a timestamp captured at publish time and a
// stable [EventType] tag for metrics and logging. The full event
// catalogue is the set of types declared in this package.
package hmevent
