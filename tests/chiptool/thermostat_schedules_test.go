// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// reSetActiveScheduleStatus is the status chip-tool prints for a
// status-only SetActiveScheduleRequest response.
var reSetActiveScheduleStatus = regexp.MustCompile(`Received Command Response Status for Endpoint=\d+ Cluster=0x0000_0201 Command=0x0000_0005 Status=(0x[0-9A-Fa-f]+)`)

// TestThermostatSchedules_FlagOn is the commissioner's view of
// north.matter.thermostat_schedules on a bridged HmIP-BWTH from godevccu:
// the running state and the setpoint hold are served, and a
// SetActiveScheduleRequest naming week program 2 lands on the CCU as
// ACTIVE_PROFILE 2.
//
// The program switch is the proof the schedules were loaded at all: the
// server answers INVALID_COMMAND for a handle that names no stored
// schedule, so a bridge that read no week profile fails here. The command
// goes by id (`command-by-id` — connectedhomeip examples/chip-tool/
// commands/clusters/ClusterCommand.h:37-41 and :215-218), because the
// pinned chip-tool's knowledge of MatterScheduleConfiguration by name is
// not established; the payload keys are field ids and an octet string is
// written "hex:" (CustomArgument.h:129-146).
func TestThermostatSchedules_FlagOn(t *testing.T) {
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled:         true,
		ThermostatSchedules: true,
		Devices:             []string{"HmIP-BWTH"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	expCtx, expCancel := context.WithTimeout(ctx, 15*time.Second)
	if _, err := b.EnableAllExposures(expCtx); err != nil {
		expCancel()
		t.Fatalf("enable exposures: %v", err)
	}
	expCancel()
	// Reassembly is asynchronous; settle before commissioning, as the
	// suite's own bring-up does.
	time.Sleep(1500 * time.Millisecond)

	ctl := harness.NewController(t, chipBin, 0x5102)
	out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil {
		t.Fatalf("commission: %v\n%s", err, out)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("pairing did not report success:\n%s", out)
	}
	b.SharedCtl = ctl

	slOut, err := ctl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	var ep uint16
	for id, clusters := range harness.ServerListIDsPerEndpoint(slOut) {
		if harness.HasCluster(clusters, 0x0201) {
			ep = id
			break
		}
	}
	if ep == 0 {
		t.Fatal("no Thermostat endpoint on the bridge")
	}
	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0201)
	if !ok {
		t.Fatalf("could not resolve the CCU address of thermostat endpoint %d", ep)
	}

	t.Run("read/running-state-and-hold", func(t *testing.T) {
		for _, a := range []struct{ slug, name string }{
			{"thermostat-running-state", "ThermostatRunningState"},
			{"temperature-setpoint-hold", "TemperatureSetpointHold"},
		} {
			out, err := ctl.ReadAttr(ctx, t, "thermostat", a.slug, ep)
			if err != nil {
				t.Fatalf("read %s: %v\n%s", a.slug, err, out)
			}
			if _, ok := harness.FindAttrUint(out, a.name); !ok {
				t.Errorf("%s not served with the flag on:\n%s", a.name, out)
			}
		}
	})

	t.Run("invoke/set-active-schedule-request", func(t *testing.T) {
		// Thermostat 0x0201, SetActiveScheduleRequest 0x05, field 0
		// ScheduleHandle — the program number as one byte.
		out, err := ctl.Run(ctx, t, "thermostat", "command-by-id", "0x05", `{"0":"hex:02"}`,
			harness.FormatNodeID(ctl.NodeID), strconv.FormatUint(uint64(ep), 10))
		if err != nil {
			t.Fatalf("command-by-id SetActiveScheduleRequest: %v\n%s", err, out)
		}
		// chip-tool reports a status-only response as "Received Command
		// Response Status for Endpoint=… Cluster=0x0000_0201
		// Command=0x0000_0005 Status=0x…" ([harness.CommandSuccess] matches
		// the marker whatever the status, so the status is read here).
		m := reSetActiveScheduleStatus.FindStringSubmatch(out)
		if len(m) < 2 || m[1] != "0x0" {
			t.Fatalf("SetActiveScheduleRequest status = %v, want 0x0 — a handle the bridge holds no schedule for answers INVALID_COMMAND (0x85):\n%s", m, out)
		}
		got, ok := b.CCU.GetDPValue(address, "ACTIVE_PROFILE")
		if !ok || fmt.Sprint(got) != "2" {
			t.Fatalf("CCU ACTIVE_PROFILE = %v (%v), want 2", got, ok)
		}
	})
}

// TestThermostatSchedules_FlagOffServesNoHold is the other value, on the
// suite's shared bridge where the flag is off: the thermostat serves no
// TemperatureSetpointHold. It reads with the parser the flag-on test
// asserts the value through, so the two directions share their measure.
func TestThermostatSchedules_FlagOffServesNoHold(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0201, 1)
	if len(eps) == 0 {
		t.Skip("no Thermostat endpoint — godevccu fleet lacks a climate device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, _ := b.SharedCtl.ReadAttr(ctx, t, "thermostat", "temperature-setpoint-hold", eps[0])
	if v, ok := harness.FindAttrUint(out, "TemperatureSetpointHold"); ok {
		t.Fatalf("TemperatureSetpointHold = %d served with the flag off:\n%s", v, out)
	}
}
