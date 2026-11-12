// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
)

// fakeProjector knows one device and records the plane it was asked for.
type fakeProjector struct {
	address   string
	lastPlane *string
}

func (f fakeProjector) ProjectDevice(_ context.Context, address, plane string) (projection.Device, bool, error) {
	*f.lastPlane = plane
	if address != f.address {
		return projection.Device{}, false, nil
	}
	return projection.Device{
		Address: address, Central: "ccu1",
		Planes: []projection.PlaneView{{
			Plane: projection.PlaneMatter, State: projection.StateActive,
			Entries: []projection.Entry{{Channel: 3, Kind: "custom", Key: "STATE", Verdict: projection.VerdictWithheld, Reason: "not_on_allow_list"}},
		}},
	}, true, nil
}

type projectionToolOut struct {
	Found      bool              `json:"found"`
	Projection projection.Device `json:"projection"`
}

func TestGetDeviceProjectionReturnsThePlanes(t *testing.T) {
	var plane string
	cs := connect(t, mcp.Deps{Projection: fakeProjector{address: "ADDR001", lastPlane: &plane}})
	defer cs.Close()

	res := callTool(t, cs, "get_device_projection", map[string]any{"address": " ADDR001 ", "plane": "matter"})
	if res.IsError {
		t.Fatal("get_device_projection returned an error")
	}
	var out projectionToolOut
	unmarshalStructured(t, res, &out)
	if !out.Found || out.Projection.Central != "ccu1" || len(out.Projection.Planes) != 1 {
		t.Fatalf("out = %+v", out)
	}
	if e := out.Projection.Planes[0].Entries[0]; e.Reason != "not_on_allow_list" || e.Verdict != projection.VerdictWithheld {
		t.Fatalf("entry = %+v", e)
	}
	if plane != "matter" {
		t.Fatalf("the projector was asked for plane %q; want matter", plane)
	}
}

func TestGetDeviceProjectionReportsAnUnknownDevice(t *testing.T) {
	var plane string
	cs := connect(t, mcp.Deps{Projection: fakeProjector{address: "ADDR001", lastPlane: &plane}})
	defer cs.Close()

	res := callTool(t, cs, "get_device_projection", map[string]any{"address": "NOPE"})
	if res.IsError {
		t.Fatal("get_device_projection returned an error for an unknown device")
	}
	var out projectionToolOut
	unmarshalStructured(t, res, &out)
	if out.Found {
		t.Fatalf("out = %+v; want found=false", out)
	}
}
