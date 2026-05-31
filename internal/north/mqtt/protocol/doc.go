// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package protocol implements the subset of MQTT 3.1.1 that the
// bridge needs — CONNECT / PUBLISH / SUBSCRIBE / PINGREQ plus the
// matching inbound frames. It is deliberately pure-Go, zero
// dependencies, so the daemon stays CGo-free.
//
// Feature coverage:
//   - MQTT 3.1.1 protocol level 0x04
//   - CONNECT with optional will (LWT) + username/password
//   - PUBLISH QoS 0 and QoS 1 (PUBACK round-trip tracked)
//   - SUBSCRIBE / UNSUBSCRIBE with one topic filter per frame
//   - PINGREQ / PINGRESP heartbeat
//   - DISCONNECT
//
// QoS 2 is rejected at Publish time — bridge users set QoS ≤ 1.
package protocol
