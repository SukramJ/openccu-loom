// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// mcpFleetSeams are the Deps fields that back the eight fleet read
// tools. Each is optional by design — a nil seam leaves its tool
// unregistered rather than advertising one that cannot answer — which is
// exactly why the composition root's side needs pinning: dropping one of
// these lines removes a tool from a running daemon and nothing else
// changes. The catalogue guard cannot see it, because that guard builds
// its own fully-wired Deps.
//
// The type is named qualified on purpose: rest.Deps is constructed in
// the same file and carries fields of the same names, so a bare "Deps"
// would be satisfied by the REST literal and every one of these lines
// could be deleted with the pin still green.
var mcpFleetSeams = []string{
	"Groups",
	"Areas",
	"Interfaces",
	"History",
	"Visibility",
	"Energy",
	"Links",
	"Schedules",
}

// TestPin_MCPFleetSeams_WiredInDaemon pins that the daemon hands every
// fleet read seam to the MCP server.
//
// The eight domains behind them — groups, areas, interfaces, history,
// visibility, energy, links, schedules — spent months in a declared
// backlog map that stayed accurate and unresolved: the REST surface
// carried them and an assistant had no way to read any of them. The
// tools exist now; this is the line that keeps them reachable.
func TestPin_MCPFleetSeams_WiredInDaemon(t *testing.T) {
	for _, field := range mcpFleetSeams {
		contract.MustFindStructLiteralField(
			t,
			"cmd/openccu-loom/daemon_rest_mount.go",
			"mcp.Deps", field,
		)
	}
}
