// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"slices"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMCPWriteToolsGatedByAllowWrites pins ADR 0025's central invariant:
// the MCP tool catalogue exposes write-capable tools only when writes are
// enabled (AllowWrites + a wired Writer); read tools are always present.
// This is the in-tree guard the ADR requires so a write tool cannot leak
// into the default read-only posture.
func TestMCPWriteToolsGatedByAllowWrites(t *testing.T) {
	t.Parallel()

	const writeTool = "set_datapoint"
	readTools := []string{"list_centrals", "list_devices", "get_device"}

	// Read-only posture: Enabled but AllowWrites=false.
	readOnly := mcpToolNames(t, mcp.Deps{
		Centrals:    emptyCentrals{},
		Devices:     emptyDevices{},
		AllowWrites: false,
	})
	for _, name := range readTools {
		if !slices.Contains(readOnly, name) {
			t.Errorf("read-only catalogue missing read tool %q (have %v)", name, readOnly)
		}
	}
	if slices.Contains(readOnly, writeTool) {
		t.Errorf("read-only catalogue must NOT contain %q (have %v)", writeTool, readOnly)
	}

	// AllowWrites=true but no Writer wired: still read-only (defensive).
	noWriter := mcpToolNames(t, mcp.Deps{
		Centrals:    emptyCentrals{},
		Devices:     emptyDevices{},
		AllowWrites: true,
	})
	if slices.Contains(noWriter, writeTool) {
		t.Errorf("AllowWrites with nil Writer must NOT expose %q (have %v)", writeTool, noWriter)
	}

	// Writes enabled: AllowWrites=true AND a Writer wired.
	withWrites := mcpToolNames(t, mcp.Deps{
		Centrals:    emptyCentrals{},
		Devices:     emptyDevices{},
		Writer:      mcpNoopWriter{},
		AllowWrites: true,
	})
	if !slices.Contains(withWrites, writeTool) {
		t.Errorf("write-enabled catalogue must contain %q (have %v)", writeTool, withWrites)
	}
}

// mcpToolNames builds the server from deps, connects an in-memory client,
// and returns the advertised tool names.
func mcpToolNames(t *testing.T, deps mcp.Deps) []string {
	t.Helper()
	ctx := context.Background()
	srv := mcp.NewServer(deps)

	t1, t2 := mcpsdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = ss.Close() }()

	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "contract", Version: "1"}, nil)
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	var names []string
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		names = append(names, tool.Name)
	}
	return names
}

// --- minimal fakes ---

type emptyCentrals struct{}

func (emptyCentrals) Names() []string { return nil }

type emptyDevices struct{}

func (emptyDevices) Devices() []*device.Device            { return nil }
func (emptyDevices) Device(string) (*device.Device, bool) { return nil, false }
func (emptyDevices) CentralOf(string) string              { return "" }

type mcpNoopWriter struct{}

func (mcpNoopWriter) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return nil
}
