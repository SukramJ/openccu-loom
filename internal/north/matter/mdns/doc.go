// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package mdns advertises the Matter bridge over mDNS / DNS-SD per
// Matter Core Spec §4.3.
//
// Two service types are emitted:
//
//   - `_matter._tcp` — operational service. The bridge advertises
//     this once any commissioner has paired and at least one fabric
//     is active. Service instance name is
//     `<compressed-fabric-id>-<node-id>` in 16+16 lowercase hex.
//   - `_matterc._udp` — commissionable service. Active only when the
//     bridge has an open commissioning window (see ADR 0012 on the
//     pairing UI). Service instance name is a 16-byte random
//     identifier in lowercase hex.
//
// TXT records carry the discovery metadata the commissioner uses to
// pre-screen candidates (discriminator, vendor / product, pairing
// hint, etc.) without speaking to the device.
//
// openccu-loom ships a minimal pure-Go implementation:
//
//   - [Service] is the typed record bundle.
//   - [BuildOperationalService] / [BuildCommissionableService] map
//     the bridge state to a [Service].
//   - [Advertiser] is the runtime surface; [NewMulticastAdvertiser]
//     announces / responds via UDP multicast on 5353. [Noop] is a
//     stub for tests.
//
// The implementation does *not* aim to be a general-purpose mDNS
// stack. It services the Matter discovery requirements only.
package mdns
