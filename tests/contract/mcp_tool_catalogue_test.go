// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
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

	readTools := []string{
		"list_centrals", "list_devices", "get_device", "read_paramset", "get_health",
		// Device-topology read tools (gated on a wired DeviceLister).
		"list_rooms", "list_functions", "list_channels",
		// Hub-aggregate read tools (gated on HubResolver + CentralLister).
		"list_programs", "list_sysvars", "list_service_messages",
		"list_alarm_messages", "list_inbox", "get_system_info",
		// Alarm / Security & Safety read tools (gated on Alarm / Security).
		"list_alarm_zones", "list_triggered_motion", "get_security_status",
	}
	// arm_alarm_zone / disarm_alarm_zone / reset_motion are gated on
	// AlarmControl + AllowWrites, exactly like the other write tools — the
	// most consequential ones in the catalogue, since they change the
	// armed state of a physical security system. A prior version of this
	// test never wired Alarm/AlarmControl/Security into fullDeps, so these
	// three tools were absent from every catalogue this test built and the
	// posture guard never saw them at all.
	writeTools := []string{
		"set_datapoint", "write_paramset", "trigger_program",
		"arm_alarm_zone", "disarm_alarm_zone", "reset_motion",
	}

	fullDeps := func(allowWrites bool) mcp.Deps {
		return mcp.Deps{
			Centrals:     emptyCentrals{},
			Devices:      emptyDevices{},
			Writer:       mcpNoopWriter{},
			Paramsets:    mcpNoopParamsets{},
			Health:       mcpNoopHealth{},
			Hubs:         mcpNoopHubs{},
			Alarm:        mcpParityAlarm{},
			AlarmControl: mcpParityAlarm{},
			Security:     mcpParitySecurity{},
			AllowWrites:  allowWrites,
		}
	}

	// Read-only posture: every dependency wired, but AllowWrites=false.
	// Read tools present; no write tool registered.
	readOnly := mcpToolNames(t, fullDeps(false))
	for _, name := range readTools {
		if !slices.Contains(readOnly, name) {
			t.Errorf("read-only catalogue missing read tool %q (have %v)", name, readOnly)
		}
	}
	for _, name := range writeTools {
		if slices.Contains(readOnly, name) {
			t.Errorf("read-only catalogue must NOT contain write tool %q (have %v)", name, readOnly)
		}
	}

	// AllowWrites=true but no write-capable deps wired: still no write tools.
	noWriteDeps := mcpToolNames(t, mcp.Deps{
		Centrals:    emptyCentrals{},
		Devices:     emptyDevices{},
		AllowWrites: true,
	})
	for _, name := range writeTools {
		if slices.Contains(noWriteDeps, name) {
			t.Errorf("AllowWrites with no write deps must NOT expose %q (have %v)", name, noWriteDeps)
		}
	}

	// Writes enabled with every dependency wired: all write tools present.
	withWrites := mcpToolNames(t, fullDeps(true))
	for _, name := range writeTools {
		if !slices.Contains(withWrites, name) {
			t.Errorf("write-enabled catalogue must contain %q (have %v)", name, withWrites)
		}
	}
}

// TestMCPToolNamingTaxonomy pins the naming concept documented over
// registerReadTools: every MCP tool name uses one of the sanctioned verb
// prefixes in allowedVerbs so the catalogue reads as a single coherent
// design rather than a grab-bag. A new tool that invents an unsanctioned
// verb (or spells one out verbosely) trips this guard.
func TestMCPToolNamingTaxonomy(t *testing.T) {
	t.Parallel()

	allowedVerbs := []string{"list", "get", "read", "set", "write", "trigger", "arm", "disarm", "reset"}

	names := mcpToolNames(t, mcp.Deps{
		Centrals:     emptyCentrals{},
		Devices:      emptyDevices{},
		Writer:       mcpNoopWriter{},
		Paramsets:    mcpNoopParamsets{},
		Health:       mcpNoopHealth{},
		Hubs:         mcpNoopHubs{},
		Alarm:        mcpParityAlarm{},
		AlarmControl: mcpParityAlarm{},
		Security:     mcpParitySecurity{},
		AllowWrites:  true,
	})
	if len(names) == 0 {
		t.Fatal("no tools advertised")
	}
	for _, name := range names {
		verb, _, found := strings.Cut(name, "_")
		if !found {
			t.Errorf("tool %q has no verb_noun shape", name)
			continue
		}
		if !slices.Contains(allowedVerbs, verb) {
			t.Errorf("tool %q uses verb %q outside the sanctioned set %v", name, verb, allowedVerbs)
		}
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

type mcpNoopParamsets struct{}

func (mcpNoopParamsets) GetParamset(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (mcpNoopParamsets) PutParamset(context.Context, string, hmenum.ParamsetKey, map[string]any) error {
	return nil
}

type mcpNoopHealth struct{}

func (mcpNoopHealth) Overall() health.Status       { return health.StatusHealthy }
func (mcpNoopHealth) Snapshot() []health.Component { return nil }
func (mcpNoopHealth) Gauges() map[string]float64   { return nil }

type mcpNoopHubs struct{}

func (mcpNoopHubs) HubFor(string) *hub.Hub { return nil }
