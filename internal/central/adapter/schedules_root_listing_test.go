// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// rootScheduleBackend describes an HM-TC-IT-WM-W-EU: a wall thermostat
// whose week profiles (P1..P3) and WEEK_PROGRAM_POINTER both live in the
// device-level MASTER paramset, with no WEEK_PROFILE channel and no
// CLIMATECONTROL_* channel anywhere.
//
// Channel types and paramset shape follow the device / paramset
// descriptions the simulator ships for this model.
func rootScheduleBackend(deviceAddress string) *paramsetFakeOps {
	return &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: deviceAddress, Type: "HM-TC-IT-WM-W-EU", Paramsets: []string{"MASTER"}},
				{Address: deviceAddress + ":0", Parent: deviceAddress, Type: "MAINTENANCE"},
				{Address: deviceAddress + ":2", Parent: deviceAddress, Type: "THERMALCONTROL_TRANSMIT"},
			}, nil
		},
		getParamsetDescriptionFn: func(
			_ context.Context, address string, key hmenum.ParamsetKey,
		) (map[string]hmproto.ParameterData, error) {
			if address != deviceAddress || key != hmenum.ParamsetKeyMaster {
				return nil, nil
			}
			out := map[string]hmproto.ParameterData{
				string(hmenum.ParameterWeekProgramPointer): {
					Type:       hmenum.ParameterTypeEnum,
					Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
					Min:        json.RawMessage("0"),
					Max:        json.RawMessage("2"),
					ValueList:  []string{"WEEK PROGRAM 1", "WEEK PROGRAM 2", "WEEK PROGRAM 3"},
				},
			}
			for profile := 1; profile <= 3; profile++ {
				for slot := 1; slot <= 13; slot++ {
					out[fmt.Sprintf("P%d_ENDTIME_MONDAY_%d", profile, slot)] = hmproto.ParameterData{
						Type:       hmenum.ParameterTypeInteger,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
					}
					out[fmt.Sprintf("P%d_TEMPERATURE_MONDAY_%d", profile, slot)] = hmproto.ParameterData{
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
					}
				}
			}
			return out, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}
}

// TestListScheduleDevicesFindsARootOnlyScheduleAfterHydration drives the
// real ingest pipeline and then asks for the fleet-wide schedule listing.
//
// The listing's device-root branch used to inspect the root channel's
// MASTER data points — but hydration deliberately drops every schedule
// slot parameter before a data point is built (it becomes the attached
// week-profile descriptor instead), so that branch could never fire on a
// hydrated device and a wall thermostat with no climate channel at all
// was silently missing from GET /schedules and the MCP fleet tool.
func TestListScheduleDevicesFindsARootOnlyScheduleAfterHydration(t *testing.T) {
	t.Parallel()
	const addr = "RFTCIT100"

	c, err := central.New(central.Config{Name: "ccu-rf"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	b := rootScheduleBackend(addr)
	w := client.NewValueWriter()
	w.Register("ccu-rf", "BidCos-RF", b)

	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())
	if err := p.IngestFromBackend(
		t.Context(), "BidCos-RF", hmenum.InterfaceBidCosRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get(addr)
	if !ok {
		t.Fatalf("device %s not ingested", addr)
	}

	got, err := NewSchedulesDomain(reg, w).ListScheduleDevices(t.Context())
	if err != nil {
		t.Fatalf("ListScheduleDevices: %v", err)
	}
	var found bool
	for _, s := range got {
		if s.Address != addr {
			continue
		}
		found = true
		if s.Kind != "climate" {
			t.Errorf("kind: got %q, want %q", s.Kind, "climate")
		}
		ch := dev.Channel(s.Channel.Address)
		if ch == nil && s.Channel.Address != addr {
			t.Errorf("channel %q is not a channel of %s", s.Channel.Address, addr)
			continue
		}
		if ch == nil {
			ch = dev.RootChannel()
		}
		if ch.WeekProfile() == nil {
			t.Errorf("listing points at %q, which carries no week profile", s.Channel.Address)
		}
	}
	if !found {
		t.Fatalf("device %s missing from the schedule listing: %+v", addr, got)
	}
}
