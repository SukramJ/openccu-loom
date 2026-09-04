// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestBridgedEndpointNameCarriesTheOperatorsChannelWord pins the channel
// word of a bridged endpoint's NodeLabel to the operator's configured
// locale, measured on the daemon's own composition path.
//
// The word is no longer resolved where it is used: the endpoint assembler
// consumes a finished string handed down through bridge.Config, and the only
// place that turns cfg.Locale into that string is startMatterBridge. A
// config field nobody assigns compiles, keeps every assembler-level test
// green, and silently shows a German operator "Channel 1" on every Apple /
// Google Home card — the surface where it is most expensive to notice.
//
// So the assertion runs through the production path end to end: the bridge
// is the one startMatterBridge built, and the name is read back off the
// handler that answers GET /api/v1/matter/endpoints, through the same
// inspector the REST mount is given. Nothing here constructs an
// endpoint.Config, which is what makes the pin able to see the gap between
// "the assembler honours the field" and "the daemon fills it in".
func TestBridgedEndpointNameCarriesTheOperatorsChannelWord(t *testing.T) {
	t.Parallel()

	const (
		deviceAddress = "0001LOC01"
		deviceName    = "Wohnzimmerlampe"
		channelNo     = 3
		// The German catalogue's channel.title plus the raw channel
		// number: the disambiguator an unnamed channel falls back to.
		wantName = deviceName + " Kanal 3"
	)

	cfg := config.Default()
	cfg.Locale = "de"
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-locale", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-locale")
	unit, ok := reg.Get("ccu-locale")
	if !ok {
		t.Fatal("the test registry did not hold the central it was built with")
	}
	unit.ModelRegistry.Put(switchDeviceWithUnnamedChannel(deviceAddress, deviceName, channelNo))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("the Matter bridge did not start; without it this pin asserts nothing")
	}
	t.Cleanup(bundle.stop)

	endpoints := bridgedEndpointsViaREST(t, matterEndpointInspector{bridge: bundle.bridge})

	var got string
	var seen []string
	for _, ep := range endpoints {
		if ep.DeviceAddress != deviceAddress {
			continue
		}
		seen = append(seen, ep.FriendlyName)
		if ep.FriendlyName == wantName {
			got = ep.FriendlyName
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no bridged endpoint materialised for %s; the pin would pass vacuously (endpoints: %d)",
			deviceAddress, len(endpoints))
	}
	if got == "" {
		t.Errorf("bridged endpoint names for %s = %q, want one reading %q — "+
			"the daemon did not hand its locale's channel word to the bridge, so every "+
			"non-English operator sees the English fallback on their controller",
			deviceAddress, seen, wantName)
	}
}

// switchDeviceWithUnnamedChannel builds the one device shape that makes the
// channel word observable: an operator-named device whose channel carries no
// name of its own, so the endpoint name falls back to the channel word plus
// the raw channel number. A named channel would render its own name and the
// word would never appear.
//
// The switch data point is what gives the channel a Matter projection at all
// — without a bridgeable source the device contributes no endpoint and the
// assertion above would have nothing to read.
func switchDeviceWithUnnamedChannel(address, name string, channelNo int) *device.Device {
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      address,
		Name:         name,
		Model:        "HmIP-PS",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	channelAddress := address + ":" + strconv.Itoa(channelNo)
	ch := dev.AddChannel(channelAddress, channelNo, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: channelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	return dev
}

// bridgedEndpointsViaREST reads the assembled topology back the way an
// operator does — through the handler behind GET /api/v1/matter/endpoints —
// so the observed string is the one the daemon actually publishes rather
// than one the test reached in and computed.
func bridgedEndpointsViaREST(t *testing.T, inspector handlers.MatterEndpointInspector) []handlers.MatterEndpointInfo {
	t.Helper()
	rec := httptest.NewRecorder()
	handlers.MatterEndpoints(inspector)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/matter/endpoints", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/matter/endpoints = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var list handlers.MatterEndpointList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode endpoint list: %v (body %s)", err, rec.Body.String())
	}
	return list.Endpoints
}
