// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// unmappableTextDisplayAddr / unmappableIrrigationAddr pin the CCU
// device addresses godevccu's HmIP-WRCD / ELV-SH-WSM fixtures use.
// Both are fixed by the embedded device_descriptions JSON (godevccu's
// internal/embed/data/device_descriptions/{HmIP-WRCD,ELV-SH-WSM}.json),
// not randomly assigned at ingest time, so hard-coding is safe.
const (
	unmappableTextDisplayAddr = "VCU4243444" // HmIP-WRCD
	unmappableIrrigationAddr  = "VCU8976407" // ELV-SH-WSM

	// unmappableTextDisplayChannel is the ACOUSTIC_DISPLAY_RECEIVER
	// channel hosting *textdisplay.TextDisplay. It is the only channel
	// on this device with no Matter projection at all — the device's
	// MAINTENANCE (battery) and KEY_TRANSCEIVER (config button)
	// channels DO materialise generic Matter endpoints, so the device
	// address alone is not a valid "unmapped" signal; the assertion
	// must scope to this specific channel.
	unmappableTextDisplayChannel = 3

	// unmappableIrrigationPrimaryChannel is ELV-SH-WSM's
	// WATER_SWITCH_VIRTUAL_RECEIVER channel — the valve's primary
	// actor, the ONLY WSM channel whose bool STATE surfaces a mappable
	// OnOff candidate. Fixed by godevccu's ELV-SH-WSM device_descriptions
	// fixture. Its sibling group-STATE transmitter (offset -1, marked
	// DataPointUsageCDPState) and secondary actor channels (offsets
	// +1/+2, ce_secondary) also carry a bool STATE but are dropped from
	// the default Matter projection.
	unmappableIrrigationPrimaryChannel = 4
)

// negativeExposableRow mirrors the subset of the REST GET
// /api/v1/matter/exposable row shape this test needs. Duplicated
// rather than imported for the same reason
// harness/ccu_resolve.go's exposureItem is duplicated: the test only
// talks to the daemon through its REST/chip-tool surface, never by
// importing daemon-internal packages.
type negativeExposableRow struct {
	DeviceAddress string `json:"device_address"`
	ChannelNo     int    `json:"channel_no"`
	DPKey         string `json:"dp_key"`
	Mappable      string `json:"mappable"`
	Enabled       bool   `json:"enabled"`
}

// negativeDisplayParamKeys are HmIP-WRCD's ACOUSTIC_DISPLAY_RECEIVER
// VALUES-paramset parameters (godevccu's paramset_descriptions/
// HmIP-WRCD.json, channel :3). None of them ever drive a bridged
// Matter endpoint — see TestNegative_UnmappableDeviceClasses.
var negativeDisplayParamKeys = map[string]bool{
	"DISPLAY_DATA_STRING":             true,
	"DISPLAY_DATA_ID":                 true,
	"DISPLAY_DATA_ICON":               true,
	"DISPLAY_DATA_ALIGNMENT":          true,
	"DISPLAY_DATA_TEXT_COLOR":         true,
	"DISPLAY_DATA_BACKGROUND_COLOR":   true,
	"DISPLAY_DATA_COMMIT":             true,
	"ACOUSTIC_NOTIFICATION_SELECTION": true,
	"COMBINED_PARAMETER":              true,
	"INTERVAL":                        true,
	"REPETITIONS":                     true,
}

// TestNegative_UnmappableDeviceClasses guards two "must not surface to
// Matter" boundaries on devices the daemon otherwise bridges happily:
//   - textdisplay.TextDisplay (HmIP-WRCD): ADR 0012's "Out of Matter
//     scope" class — no channel of it ever materialises an endpoint,
//     even though the same device's config button + battery telemetry do.
//   - valve.Irrigation (ELV-SH-WSM): its primary channel IS bridged as
//     OnOff (decision B), but the custom entity's redundant group-STATE
//     transmitter (ce_state) and secondary actor channels (ce_secondary)
//     must stay hidden by default so exactly one on/off endpoint surfaces.
//
// HmIP-WRCD and ELV-SH-WSM are not in [harness.DefaultDevices], so this
// test brings up its own isolated bridge with them added to the fleet
// rather than reaching for [requireBridge]'s shared one.
func TestNegative_UnmappableDeviceClasses(t *testing.T) {
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled: true,
		Devices:     []string{"HmIP-WRCD", "ELV-SH-WSM"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var candidates struct {
		Items []negativeExposableRow `json:"items"`
	}
	if status := b.RESTGet(t, "/api/v1/matter/exposable", &candidates); status != 200 {
		t.Fatalf("GET /matter/exposable: status=%d", status)
	}

	// SEND/RECEIVE direction does not apply here — a source with no
	// Matter projection has no attribute to write or subscribe to.
	// The negative assertion IS the test: the candidate row (and,
	// transitively, the bridged endpoint) must never exist.
	t.Run("textdisplay/no-exposable-candidate", func(t *testing.T) {
		// textdisplay.TextDisplay implements neither
		// MatterEndpointSource nor MatterMeasurementSource, and every
		// one of its constituent Action* generic DPs (DISPLAY_DATA_*,
		// ACOUSTIC_NOTIFICATION_SELECTION, COMBINED_PARAMETER, ...)
		// classifies the same way. The candidate collector's
		// "opaque source" skip
		// (internal/north/matteradapter/candidates.go,
		// collectChannelCandidates) drops the whole channel from the
		// candidate list rather than surfacing it as an explicit
		// unmappable row — so this channel must be entirely absent
		// from /matter/exposable, not merely marked "unmappable".
		for _, it := range candidates.Items {
			if it.DeviceAddress == unmappableTextDisplayAddr && it.ChannelNo == unmappableTextDisplayChannel {
				t.Errorf("unexpected /matter/exposable row for HmIP-WRCD's TextDisplay channel: %+v", it)
			}
		}
	})

	t.Run("textdisplay/no-bridged-endpoint", func(t *testing.T) {
		// Enable every candidate the daemon considers mappable — the
		// config-button and battery channels on the SAME device
		// legitimately materialise endpoints, only the display
		// channel must stay absent — then commission and walk the
		// aggregator to confirm no bridged endpoint traces back to a
		// display/acoustic parameter.
		expCtx, expCancel := context.WithTimeout(ctx, 15*time.Second)
		if _, err := b.EnableAllExposures(expCtx); err != nil {
			t.Fatalf("enable exposures: %v", err)
		}
		expCancel()
		// Bridge reassembly is asynchronous; give it a settle window
		// before commissioning against a still-rebuilding topology
		// (mirrors requireBridge's own bring-up in main_test.go).
		time.Sleep(1500 * time.Millisecond)

		ctl := harness.NewController(t, chipBin, 0x5001)
		out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
		if err != nil {
			t.Fatalf("commission: %v\n%s", err, out)
		}
		if !harness.PairingSuccess(out) {
			t.Fatalf("pairing did not report success:\n%s", out)
		}
		// [Bridge.ResolveCCUAddress] reads through b.SharedCtl; this
		// isolated bridge has none until we wire the controller we
		// just commissioned with — same assignment [requireBridge]
		// performs for the shared suite bridge.
		b.SharedCtl = ctl

		aggOut, err := ctl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
		if err != nil {
			t.Fatalf("aggregator parts-list: %v", err)
		}
		eps := harness.EndpointsInPartsList(aggOut)
		if len(eps) == 0 {
			t.Fatal("aggregator PartsList empty — expected at least HmIP-WRCD's config-button endpoint(s)")
		}

		for _, ep := range eps {
			addr, dpKey, ok := b.ResolveCCUAddress(ctx, t, ep, 0)
			if !ok || addr != unmappableTextDisplayAddr {
				continue
			}
			if negativeDisplayParamKeys[dpKey] {
				t.Errorf("bridged endpoint %d resolves to HmIP-WRCD via display parameter %q — TextDisplay must stay MQTT-only (ADR 0012)", ep, dpKey)
			}
		}

		if _, err := ctl.Unpair(ctx, t); err != nil {
			t.Logf("unpair (best-effort): %v", err)
		}
	})

	// valve.Irrigation (ELV-SH-WSM) IS bridged — its embedded
	// *generic.Switch (internal/model/custom/valve/valve.go) projects
	// the primary WATER_SWITCH_VIRTUAL_RECEIVER STATE onto OnOff
	// (0x0006) / OnOffPlugInUnit (0x010A) via
	// interfaces.MatterEndpointSource
	// (internal/model/generic/switch_matter.go). This is intentional
	// (docs/adr/0049-matter-one-endpoint-per-device.md, superseding
	// ADR 0012's earlier "stays MQTT-only" valve rows).
	//
	// The NEGATIVE part this suite still guards: a valve custom entity
	// spans several channels off its primary, and only the primary may
	// surface an on/off actor to Matter. Its group-STATE transmitter
	// (offset -1, marked DataPointUsageCDPState — its bool STATE merely
	// restates the primary's on/off) and its sibling actor channels
	// (offsets +1/+2, ce_secondary) each carry a bool STATE that would
	// ALSO project to OnOff, but the default Matter projection
	// (north.matter.expose_secondary_channels off) drops them so the
	// device exposes exactly one on/off endpoint. Asserted at the
	// candidate layer: exactly one WSM channel — the primary — yields a
	// mappable STATE row.
	t.Run("irrigation-valve/only-primary-channel-onoff", func(t *testing.T) {
		stateChannels := map[int]bool{}
		for _, it := range candidates.Items {
			if it.DeviceAddress == unmappableIrrigationAddr && it.DPKey == "STATE" && it.Mappable == "mappable" {
				stateChannels[it.ChannelNo] = true
			}
		}
		if !stateChannels[unmappableIrrigationPrimaryChannel] {
			t.Errorf("ELV-SH-WSM primary valve channel %d has no mappable STATE candidate — decision B expects it exposed as OnOff", unmappableIrrigationPrimaryChannel)
		}
		for ch := range stateChannels {
			if ch != unmappableIrrigationPrimaryChannel {
				t.Errorf("ELV-SH-WSM channel %d also exposes a mappable STATE candidate — the group-STATE transmitter (ce_state) and secondary actor channels (ce_secondary) must stay hidden while expose_secondary_channels is off", ch)
			}
		}
	})

	t.Run("irrigation-valve/internal-params-not-candidates", func(t *testing.T) {
		// Service / status / overflow params (usage ignored) and constituents
		// consumed by an aggregating parent (usage no_create) are hidden on
		// every north-bound surface. The Matter candidate collector applies the
		// same visibility gate (docs/adr/0049-matter-one-endpoint-per-device.md),
		// so none of these may surface as an exposable row.
		internal := map[string]bool{
			"INSTALL_TEST":               true, // ignored: service param
			"CONFIG_PENDING":             true, // ignored: service param
			"UNREACH":                    true, // ignored: service param
			"UPDATE_PENDING":             true, // ignored: service param
			"ACTUAL_TEMPERATURE_STATUS":  true, // ignored: *_STATUS validity flag
			"WATER_FLOW_STATUS":          true, // ignored: *_STATUS validity flag
			"WATER_VOLUME_OVERFLOW":      true, // ignored: *_OVERFLOW counter flag
			"PROCESS":                    true, // ignored: irrigation-program plumbing
			"SECTION_STATUS":             true, // ignored: *_STATUS validity flag
			"WEEK_PROGRAM_CHANNEL_LOCKS": true, // no_create: consumed by week profile
		}
		for _, it := range candidates.Items {
			if it.DeviceAddress == unmappableIrrigationAddr && internal[it.DPKey] {
				t.Errorf("ELV-SH-WSM exposes a Matter candidate for internal param %q (ch %d) — ignored / no_create params must stay out of the candidate list", it.DPKey, it.ChannelNo)
			}
		}
	})
}
