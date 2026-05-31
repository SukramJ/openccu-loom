// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package mrp implements the Matter Message Reliability Protocol per
// Matter Core Specification §4.12.
//
// MRP rides on top of the unreliable UDP transport and adds:
//
//   - Per-session monotonic 32-bit message counters
//     (initialized to a random value at session creation).
//   - Sliding-window duplicate detection across the most recent 32
//     received counters.
//   - Retransmission with exponential backoff for messages flagged
//     "Reliable" (Exchange Flag R = 1) until either an ACK arrives
//     or [MaxRetransmissions] is reached.
//   - Standalone-ACK synthesis ([AckTracker]) when no payload is
//     queued to piggyback the acknowledgement on. The dispatcher
//     drains [AckTracker.Due] and emits a Secure-Channel
//     StandaloneAck per Matter §4.12.7 (opcode [StandaloneAckOpcode]
//     under [SecureChannelProtocolID]).
//
// The primitives in this package are stateful but I/O-free; the UDP
// transport ([..]/transport/udp) drives them via a small adapter
// surface so MRP itself can be unit-tested without sockets.
package mrp
