// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package udp implements the Matter UDP transport per Matter Core
// Specification §4.3.
//
// Matter mandates UDP/IPv6 on port 5540 for both the Operational and
// Commissionable surfaces. The Listener listens on a configured
// address (default `[::]:5540`) and dispatches every datagram to a
// caller-supplied [Handler]. Outbound traffic flows through
// [Listener.Send].
//
// IPv4 is supported as a fallback for HA deployments without IPv6
// configured on the LAN side. Production deployments should prefer
// IPv6 (mDNS / DNS-SD operational lookup uses link-local IPv6 hop
// addresses).
//
// The package is dependency-free Go stdlib (`net`); the listener
// goroutine is owned by the caller (Listener.Serve runs until ctx
// cancels). MRP / Message-Header parsing live in sibling packages —
// this transport is intentionally protocol-blind.
package udp
