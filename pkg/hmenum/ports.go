// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// Default port constants. Values mirror SPECIFICATION §7.2.
const (
	// DefaultJSONRPCPort is the HTTP port for CCU JSON-RPC (/api/homematic.cgi).
	DefaultJSONRPCPort = 80

	// DefaultJSONRPCTLSPort is the HTTPS variant.
	DefaultJSONRPCTLSPort = 443

	// DefaultBINRPCPort is the CUxD BIN-RPC port (daemon → CUxD).
	DefaultBINRPCPort = 8701

	// DefaultXMLRPCCallbackPort is the daemon's XML-RPC callback listener.
	DefaultXMLRPCCallbackPort = 8120

	// DefaultBINRPCCallbackPort is the daemon's BIN-RPC callback listener.
	DefaultBINRPCCallbackPort = 8129
)

// DetectionPort is the plain-HTTP / TLS port pair a southbound client
// uses to probe an interface when the user does not pin an explicit port.
type DetectionPort struct {
	Plain int
	TLS   int // 0 = no TLS variant
}

// DetectionPorts maps each MVP interface to its canonical probe ports.
// The HmIP-RF entry covers HmIP-Wired devices too: the CCU exposes a
// single HmIP service on port 2010/42010 for both RF and Wired
// flavours.
var DetectionPorts = map[Interface]DetectionPort{
	InterfaceBidCosRF:       {Plain: 2001, TLS: 42001},
	InterfaceBidCosWired:    {Plain: 2000, TLS: 42000},
	InterfaceHmIPRF:         {Plain: 2010, TLS: 42010},
	InterfaceVirtualDevices: {Plain: 9292, TLS: 49292},
	InterfaceCUxD:           {Plain: DefaultBINRPCPort, TLS: 0},
}
