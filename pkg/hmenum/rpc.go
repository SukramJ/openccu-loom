// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// RPCType identifies the RPC dialect used for a given operation.
type RPCType string

// RPCType values.
const (
	RPCTypeXMLRPC  RPCType = "xmlrpc"
	RPCTypeJSONRPC RPCType = "jsonrpc"
)

// String returns the wire representation.
func (r RPCType) String() string { return string(r) }

// RPCServerType is the server-side RPC dialect.
type RPCServerType string

// RPCServerType values.
const (
	// RPCServerTypeXMLRPC is the XML-RPC callback server (HTTP).
	RPCServerTypeXMLRPC RPCServerType = "xml_rpc"
	// RPCServerTypeBINRPC is the BIN-RPC callback server (raw TCP).
	// Used for CUxD — this diverges, which routes
	// CUxD through an XML-RPC workaround. openccu-loom speaks BIN-RPC
	// natively (see SPECIFICATION §8.5 and ADR 0002).
	RPCServerTypeBINRPC RPCServerType = "bin_rpc"
	// RPCServerTypeNone signals that no callback server is required
	// (JSON-RPC-only or polling-only paths).
	RPCServerTypeNone RPCServerType = "none"
)

// String returns the wire representation.
func (r RPCServerType) String() string { return string(r) }

// ProxyInitState enumerates the outcomes of an XML-RPC proxy init/de-init.
type ProxyInitState int

// ProxyInitState values.
const (
	ProxyInitStateFailed       ProxyInitState = 0
	ProxyInitStateSuccess      ProxyInitState = 1
	ProxyInitStateDeInitFailed ProxyInitState = 4
	ProxyInitStateDeInitOK     ProxyInitState = 8
	ProxyInitStateDeInitSkip   ProxyInitState = 16
)

// PingPongMismatchType categorises a ping/pong anomaly.
type PingPongMismatchType string

// PingPongMismatchType values.
const (
	PingPongMismatchPending PingPongMismatchType = "pending"
	PingPongMismatchUnknown PingPongMismatchType = "unknown"
)

// String returns the wire representation.
func (t PingPongMismatchType) String() string { return string(t) }

// OptionalSettings names runtime feature toggles.
type OptionalSettings string

// OptionalSettings values.
const (
	OptionalSettingSRDisableRandomizeOutput OptionalSettings = "SR_DISABLE_RANDOMIZED_OUTPUT"
	OptionalSettingSRRecordSystemInit       OptionalSettings = "SR_RECORD_SYSTEM_INIT"
)

// String returns the wire representation.
func (o OptionalSettings) String() string { return string(o) }
