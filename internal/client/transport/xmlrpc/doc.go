// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package xmlrpc is the southbound XML-RPC transport used to talk to
// every HTTP-based CCU interface (HmIP-RF, BidCos-RF, BidCos-Wired,
// VirtualDevices) and to host our own XML-RPC callback endpoint.
//
// The XML-RPC data model is encoded as typed values: an interface
// [Value] implemented by [IntValue], [BoolValue], [StringValue],
// [DoubleValue], [DateTimeValue], [Base64Value], [StructValue],
// [ArrayValue], and [NilValue]. Encoding is handled by each type's
// own MarshalXML; the polymorphic decode of `<value>` is driven by
// [DecodeValue] using the stream tokenizer.
//
// This package handles wire encoding only. Retry, circuit breaking,
// throttling, and coalescing live one layer up in internal/client;
// hmerr.Context enrichment happens here because the transport owns
// the protocol/method/host triple.
package xmlrpc
