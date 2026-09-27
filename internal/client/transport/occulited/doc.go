// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package occulited is the wire client for an openccu-lite box: the
// HTTP API that occulited serves behind the box's web server.
//
// The package knows the wire contract and nothing of Loom's domain. It
// is written from the condensed contract in the openccu-lite
// implementation plan (notes/plans/openccu-lite-backend.md, Appendix A)
// and from the occulited documentation named there; nothing in it is
// derived from the occulited sources.
//
// # Surfaces
//
//   - [Client] carries the base URL, the API token and the TLS pin. Its
//     transport adds the bearer credential, sends a body with a
//     Content-Length on every request that may carry one (the web server
//     in front of occulited answers 411 otherwise) and turns the box's
//     own refusals into typed errors ([APIError]); [Client.HTTPClient]
//     hands that transport to the XML-RPC client for the lite-rpc proxy.
//   - [Client.Detect] classifies a base URL as an openccu-lite box, one
//     that is still starting, a CCU, or unknown.
//   - [Client.Events] reads the lite-rpc event stream and
//     [Client.MetaEvents] the metadata change stream; both reconnect on
//     their own and deliver typed messages on a channel.
//   - meta.go, system.go and upnp.go hold the typed request/answer calls;
//     faults.go recognises the proxy's refusals that arrive as XML-RPC
//     faults over HTTP 200.
//
// # Typing
//
// Only the members whose type the contract states (or that a consumer
// branches on) are typed. Everything else stays available as the raw
// JSON of the answer, so a box that grows or reshapes a member Loom does
// not read never fails a call. Values of datapoints stay JSON as the
// stream sent them ([json.RawMessage]); the south-bound adapter types
// them from the paramset description.
package occulited
