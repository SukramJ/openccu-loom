// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/north/projection"
)

// DeviceProjector answers how one device stands on the north-bound
// planes; the REST route GET /devices/{addr}/projection reads the same
// seam. The second result is false for an address no central holds.
type DeviceProjector interface {
	ProjectDevice(ctx context.Context, address, plane string) (projection.Device, bool, error)
}

type getDeviceProjectionIn struct {
	Address string `json:"address" jsonschema:"device address, as list_devices reports it"`
	Plane   string `json:"plane,omitempty" jsonschema:"narrow the answer to one plane: matter, measurements, ha_discovery or mqtt_raw; empty asks every plane"`
}

type getDeviceProjectionOut struct {
	Found      bool              `json:"found"`
	Projection projection.Device `json:"projection"`
}

// registerGetDeviceProjection implements `get_device_projection`, the
// read an assistant needs for "why is this device missing over there".
func registerGetDeviceProjection(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_device_projection",
		Description: "How one device stands on the north-bound planes: per plane, what is published and under " +
			"which identifiers, what is carried inside another entry, and what is left out with the named reason. " +
			"Answers for the planes this daemon projects (today: matter, measurements, ha_discovery and mqtt_raw).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getDeviceProjectionIn) (*mcpsdk.CallToolResult, getDeviceProjectionOut, error) {
		out, ok, err := d.Projection.ProjectDevice(ctx, strings.TrimSpace(in.Address), strings.TrimSpace(in.Plane))
		if err != nil {
			return nil, getDeviceProjectionOut{}, err
		}
		if !ok {
			return nil, getDeviceProjectionOut{Found: false, Projection: projection.Device{Planes: []projection.PlaneView{}}}, nil
		}
		return nil, getDeviceProjectionOut{Found: true, Projection: out}, nil
	})
}
