// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package message implements the Matter Message Header and Protocol
// Header framing per Matter Core Specification §4.4.
//
// A Matter wire frame is structured as:
//
//	+------------------+
//	| Message Header   | 8–18 bytes (variable per Source / Dest flags)
//	|   - Flags (1B)   |
//	|   - Session ID   | 2B
//	|   - SecFlags     | 1B
//	|   - MsgCounter   | 4B
//	|   - SourceNodeID | 0 or 8B
//	|   - DestNodeID   | 0, 2, or 8B
//	+------------------+
//	| Protocol Header  | 6–12 bytes
//	|   - ExchFlags    | 1B
//	|   - Opcode       | 1B
//	|   - ExchangeID   | 2B
//	|   - VendorID     | 0 or 2B (per V flag)
//	|   - ProtocolID   | 2B
//	|   - AckCounter   | 0 or 4B (per A flag)
//	+------------------+
//	| Payload          | TLV-encoded application data
//	+------------------+
//	| MIC              | 16B (encrypted sessions only — handled in [..]/secure)
//	+------------------+
//
// This package handles the *unencrypted* framing only — the form used
// by PASE / CASE session establishment and by the Unsecured Session
// (group multicast / commissioning). Encrypted framing layers MIC +
// payload encryption on top via the [..]/secure package (M2).
package message
