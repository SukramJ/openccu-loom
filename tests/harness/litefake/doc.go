// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package litefake is an in-process fake of an openccu-lite box, written
// from the wire contract in the openccu-lite implementation plan
// (notes/plans/openccu-lite-backend.md, Appendix A). It is our own code:
// nothing in it is derived from the occulited sources, only from that
// condensed contract and the occulited documentation named there.
//
// # Composition
//
// A [Fake] starts a godevccu simulator with one listener per interface
// (the lite topology: each interface process has its own device
// partition and callback registry) and plays the role occulited's RPC
// subscriber plays on a real box: it is the only XML-RPC client that
// calls init on the interface processes, it serves the callback endpoint
// through Loom's own xmlrpc server mux, and it turns every callback into
// a message on an in-memory ring. A loopback HTTP server then offers the
// LAN-side surfaces a client of the box sees:
//
//   - /api/rpc/v1/interfaces, /api/rpc/v1/xmlrpc/{interface} (the
//     XML-RPC proxy with token auth, the per-method tier check and the
//     init refusal), /api/rpc/v1/events (the SSE event stream with
//     resume, filters, resync and stream limits), /api/rpc/v1/state;
//   - /api/auth/v1/state, /api/system/v1/health, /api/meta/v1/version;
//   - /upnp/basic_dev.cgi and the HTML shell catch-all that answers any
//     other non-API path, /ise/checkrega.cgi included.
//
// # Knobs
//
// The knobs in knobs.go steer the fake from a test: readiness,
// interface outages and restarts, a process restart (new boot id),
// dropped streams, forced overflow and gaps, the token table, the
// heartbeat interval. [Fake.V] exposes the godevccu instance so a test
// can make a device report a value; [Fake.Calls] lists the API requests
// the fake received.
//
// # Lifecycle
//
// [Start] brings everything up and returns once every interface has
// accepted the subscriber's init; [Fake.Close] stops the HTTP servers,
// every open stream and the simulator. Each goroutine the fake starts
// ends on Close at the latest.
package litefake
