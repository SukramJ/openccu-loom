// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// Naming-contract tests — §3.7 naming algorithm.
//
// These tests pin the three-axis naming algorithm that determines the
// HA-Discovery entity `name` field for aggregated custom-DP channels:
//
// Axis 1 — Primary vs Secondary channel (IsCustomDPPrimaryChannel /
// IsCustomDPSecondaryChannel): primary → eligible for a name;
// secondary → "vch<N>" and enabled_by_default=false.
//
// Axis 2 — Single vs Multi primary in the same HA-component category
// (HasSinglePrimaryCustomDP / is_multi_channel_device parity):
// single primary → name="" (HA renders device.name alone);
// multiple primaries → "ch<N>" per primary.
//
// Axis 3 — Channel-name override: when the operator gave the channel a
// real name the press-event builder surfaces that label
// instead of the fallback "ch<N>" string.
//
// Bug context (smoke-test against productive HmIP-CCU):
// • HmIP-PSM ch3 (the sole SWITCH primary) was named "vch4" instead of
// "" — the code was counting all primaries category-blind, seeing
// ch4/ch5 (which are SECONDARY), and over-counting.
// • Press-event channels used generic "ch1" instead of the CCU
// operator-assigned channel name.

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildNamingDevice constructs a device with the given model, a name, and
// channels up to maxCh. Uses the shared addChannel / newDevice helpers from
// custom_dp_anchor_test.go (same package).
func buildNamingDevice(model string, maxCh int) *device.Device {
	d := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001NAMING",
		Model:        model,
		Name:         "TestDevice",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := 0; i <= maxCh; i++ {
		addr := "0001NAMING:" + itoa(i)
		d.AddChannel(addr, i, "X", hmenum.ParamsetKeyValues)
	}
	return d
}

// materializeNaming runs CreateCustomDataPoints on d and fatals on error.
func materializeNaming(t *testing.T, d *device.Device) {
	t.Helper()
	if err := custom.CreateCustomDataPoints(d, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}
}

// ─── Axis 2 — Single-primary-in-category ─────────────────────────────────────

// TestNaming_HmIPPS_SingleSwitchPrimary_NoSuffix is the regression test for
// the HmIP-PSM smoke-test bug: the sole SWITCH primary on ch3 must satisfy
// HasSinglePrimaryCustomDP=true, producing an empty display name in HA
// Discovery (HA falls back to device.name alone, e.g. "Steckdose").
//
// The original defect: ch4 and ch5 are SECONDARY channels. When the naming
// code counted them as primaries the "single primary?" check returned false,
// forcing an incorrect "ch3" suffix (or in a previous variant "vch4").
func TestNaming_HmIPPS_SingleSwitchPrimary_NoSuffix(t *testing.T) {
	t.Parallel()

	dev := buildNamingDevice("HmIP-PS", 8)
	ch3 := dev.Channel("0001NAMING:3")
	putBool(ch3, hmenum.ParameterState)

	materializeNaming(t, dev)

	// ch3 carries the Switch primary.
	if cdp := ch3.CustomDataPoint(); cdp == nil {
		t.Fatal("HmIP-PS ch3 has no custom DP — IPSwitch did not materialise")
	}

	// PRIMARY check.
	if !ch3.IsCustomDPPrimaryChannel() {
		t.Error("HmIP-PS ch3 must be IsCustomDPPrimaryChannel=true")
	}
	if ch3.IsCustomDPSecondaryChannel() {
		t.Error("HmIP-PS ch3 must NOT be IsCustomDPSecondaryChannel")
	}

	// Single-primary check: there is exactly ONE switch primary in this
	// device → HasSinglePrimaryCustomDP must be true.
	if !ch3.HasSinglePrimaryCustomDP() {
		t.Error("HmIP-PS ch3 HasSinglePrimaryCustomDP must be true — it is the ONLY switch primary")
	}
}

// TestNaming_HmIPPS_SecondaryChannels_VchSuffix verifies that the secondary
// channels of HmIP-PS (ch4 and ch5, which mirror the primary for group
// membership) are correctly classified as secondary (not primary) and would
// therefore receive "vch<N>" suffixes in the discovery name builder.
func TestNaming_HmIPPS_SecondaryChannels_VchSuffix(t *testing.T) {
	t.Parallel()

	dev := buildNamingDevice("HmIP-PS", 8)
	ch3 := dev.Channel("0001NAMING:3")
	putBool(ch3, hmenum.ParameterState)

	materializeNaming(t, dev)

	// ch4 and ch5 are the secondary channels for HmIP-PS IPSwitch.
	for _, num := range []int{4, 5} {
		ch := dev.Channel("0001NAMING:" + itoa(num))
		if ch == nil {
			continue // channel may not exist in this fixture
		}
		if ch.CustomDataPoint() == nil {
			continue // no custom DP on this channel — skip
		}
		if ch.IsCustomDPPrimaryChannel() {
			t.Errorf("HmIP-PS ch%d must NOT be a primary channel — it is secondary", num)
		}
		if !ch.IsCustomDPSecondaryChannel() {
			t.Errorf("HmIP-PS ch%d must be IsCustomDPSecondaryChannel=true", num)
		}
	}
}

// TestNaming_HmIPBWTH_SingleClimatePrimary verifies the wall-thermostat
// single-primary rule: HmIP-BWTH has exactly one climate primary on ch1.
// HasSinglePrimaryCustomDP must be true so the discovery name is empty,
// causing HA to render "Wandthermostat AK" (device.name alone).
func TestNaming_HmIPBWTH_SingleClimatePrimary(t *testing.T) {
	t.Parallel()

	dev := buildNamingDevice("HmIP-BWTH", 8)

	ch0 := dev.Channel("0001NAMING:0")
	putBool(ch0, hmenum.ParameterGlobalButtonLock)
	ch1 := dev.Channel("0001NAMING:1")
	putFloat(ch1, hmenum.ParameterSetPointTemperature)
	putFloatSensor(ch1, hmenum.ParameterActualTemperature)

	materializeNaming(t, dev)

	if cdp := ch1.CustomDataPoint(); cdp == nil {
		t.Fatal("HmIP-BWTH ch1 has no custom DP — IPThermostat did not materialise")
	}
	if !ch1.IsCustomDPPrimaryChannel() {
		t.Error("HmIP-BWTH ch1 must be a primary channel")
	}
	if !ch1.HasSinglePrimaryCustomDP() {
		t.Error("HmIP-BWTH ch1 HasSinglePrimaryCustomDP must be true — single climate primary")
	}
}

// ─── Axis 2 — Multi-primary-in-category ──────────────────────────────────────

// TestNaming_MultiSwitchPrimary_HasSingleReturnsFalse pins the multi-primary
// case: when a device has two channels that are each a PRIMARY for the same
// HA component, HasSinglePrimaryCustomDP must return false for both, so the
// discovery builder assigns "ch<N>" suffixes to prevent name collisions.
//
// We construct a synthetic two-switch device directly rather than relying on
// a real device model, because the generated profile registry does not include
// a device with two independent switch primaries at fixture-creation time.
func TestNaming_MultiSwitchPrimary_HasSingleReturnsFalse(t *testing.T) {
	t.Parallel()

	// Build a device that is NOT in the profile registry so the materializer
	// will not touch it. We manually call SetCustomDataPoint + GroupNo to
	// simulate what the materializer would produce for a two-channel switch.
	d := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001MULTI",
		Model:        "HmIP-MULTISWITCH-FIXTURE",
		Name:         "Multi Switch",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch1 := d.AddChannel("0001MULTI:1", 1, "X", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("0001MULTI:2", 2, "X", hmenum.ParamsetKeyValues)

	// Attach a minimal Switch custom DP to both channels. Neither channel
	// belongs to a group (GroupNo==0) so both are treated as independent
	// primaries per IsCustomDPPrimaryChannel.
	sw1 := newTestSwitch("0001MULTI:1")
	sw2 := newTestSwitch("0001MULTI:2")
	ch1.SetCustomDataPoint(sw1)
	ch2.SetCustomDataPoint(sw2)

	// With TWO switch primaries, HasSinglePrimaryCustomDP must be false for both.
	if ch1.HasSinglePrimaryCustomDP() {
		t.Error("ch1 HasSinglePrimaryCustomDP must be false when two switch primaries exist")
	}
	if ch2.HasSinglePrimaryCustomDP() {
		t.Error("ch2 HasSinglePrimaryCustomDP must be false when two switch primaries exist")
	}
	// Both are primaries (GroupNo==0 → treated as primary).
	if !ch1.IsCustomDPPrimaryChannel() {
		t.Error("ch1 must be a primary channel (no group)")
	}
	if !ch2.IsCustomDPPrimaryChannel() {
		t.Error("ch2 must be a primary channel (no group)")
	}
}

// ─── Axis 1 — Secondary channel classification ───────────────────────────────

// TestNaming_SecondaryChannel_NotPrimary verifies the secondary classification
// contract: a channel with GroupNo!=0 and Number!=GroupNo carries a secondary
// custom DP. It must return IsCustomDPSecondaryChannel=true and
// IsCustomDPPrimaryChannel=false.
func TestNaming_SecondaryChannel_NotPrimary(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001SEC",
		Model:        "HmIP-SECONDARY-FIXTURE",
		Name:         "Secondary Fixture",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})

	// Primary channel: Number==GroupNo==3 (group master).
	chPrimary := d.AddChannel("0001SEC:3", 3, "X", hmenum.ParamsetKeyValues)
	chPrimary.GroupNo = 3

	// Secondary channel: Number==4, GroupNo==3 (slave in same group).
	chSecondary := d.AddChannel("0001SEC:4", 4, "X", hmenum.ParamsetKeyValues)
	chSecondary.GroupNo = 3

	sw := newTestSwitch("0001SEC:3")
	swSec := newTestSwitch("0001SEC:4")
	chPrimary.SetCustomDataPoint(sw)
	chSecondary.SetCustomDataPoint(swSec)

	if !chPrimary.IsCustomDPPrimaryChannel() {
		t.Error("ch3 (group master) must be IsCustomDPPrimaryChannel=true")
	}
	if chPrimary.IsCustomDPSecondaryChannel() {
		t.Error("ch3 (group master) must NOT be IsCustomDPSecondaryChannel")
	}
	if chSecondary.IsCustomDPPrimaryChannel() {
		t.Error("ch4 (group slave) must NOT be IsCustomDPPrimaryChannel")
	}
	if !chSecondary.IsCustomDPSecondaryChannel() {
		t.Error("ch4 (group slave) must be IsCustomDPSecondaryChannel=true")
	}
}

// ─── Axis 3 — Channel-name override for press-event entities ─────────────────

// TestNaming_PressEvent_UsesOperatorChannelName tests that when a channel
// carries 2+ PRESS_* parameters AND the operator gave it a real name,
// BuildChannelEvent surfaces that name instead of falling back to "ch<N>".
// This is the regression case from the smoke-test: a wall button channel
// named "Taster Wohnzimmer oben links" was surfaced as "ch1".
func TestNaming_PressEvent_UsesOperatorChannelName(t *testing.T) {
	t.Parallel()

	ch := &namedPressChannel{
		params:      map[string]struct{}{"PRESS_SHORT": {}, "PRESS_LONG": {}},
		channelName: "Taster Wohnzimmer oben links",
	}
	ev := mqtt.Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001WRC2",
		ChannelNo:     1,
		Parameter:     "PRESS_SHORT",
		Channel:       ch,
	}

	db := mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("gh"), "ccu")
	_, _, _, buf, ok := db.BuildChannelEvent(ev)
	if !ok {
		t.Fatal("BuildChannelEvent returned ok=false")
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	name, _ := payload["name"].(string)
	if name != "Taster Wohnzimmer oben links" {
		t.Errorf("name = %q, want %q (operator channel name)", name, "Taster Wohnzimmer oben links")
	}
}

// TestNaming_PressEvent_FallsBackToChN_WhenNoChannelName tests the fallback:
// when the channel has no operator-assigned name, BuildChannelEvent falls back
// to "ch<N>".
func TestNaming_PressEvent_FallsBackToChN_WhenNoChannelName(t *testing.T) {
	t.Parallel()

	ch := &namedPressChannel{
		params:      map[string]struct{}{"PRESS_SHORT": {}, "PRESS_LONG": {}},
		channelName: "", // no operator name
	}
	ev := mqtt.Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001WRC2",
		ChannelNo:     2,
		Parameter:     "PRESS_SHORT",
		Channel:       ch,
	}

	db := mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("gh"), "ccu")
	_, _, _, buf, ok := db.BuildChannelEvent(ev)
	if !ok {
		t.Fatal("BuildChannelEvent returned ok=false")
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	name, _ := payload["name"].(string)
	if name != "ch2" {
		t.Errorf("name = %q, want %q (fallback channel-number suffix)", name, "ch2")
	}
}

// TestNaming_PressEvent_SinglePress_NotAggregated verifies that a channel
// carrying only one PRESS_* parameter does NOT produce a BuildChannelEvent
// payload — it falls through to the per-parameter path instead.
func TestNaming_PressEvent_SinglePress_NotAggregated(t *testing.T) {
	t.Parallel()

	ch := &namedPressChannel{
		params: map[string]struct{}{"PRESS_SHORT": {}}, // only one
	}
	ev := mqtt.Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001BTN",
		ChannelNo:     1,
		Parameter:     "PRESS_SHORT",
		Channel:       ch,
	}

	db := mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("gh"), "ccu")
	_, _, _, _, ok := db.BuildChannelEvent(ev)
	if ok {
		t.Error("BuildChannelEvent must return ok=false for a single-press channel")
	}
}

// ─── table-driven multi-case test ────────────────────────────────────────────

// TestNaming_ChannelClassification_Table is the table-driven regression
// that exercises Primary/Secondary/SinglePrimary classification for the
// documented device scenarios.
func TestNaming_ChannelClassification_Table(t *testing.T) {
	t.Parallel()

	// Build the HmIP-PS device once (has one switch primary ch3 and secondary ch4/ch5).
	hmipPS := buildNamingDevice("HmIP-PS", 8)
	ch3PS := hmipPS.Channel("0001NAMING:3")
	putBool(ch3PS, hmenum.ParameterState)
	materializeNaming(t, hmipPS)

	// Build HmIP-BWTH (one climate primary ch1).
	hmipBWTH := buildNamingDevice("HmIP-BWTH", 8)
	ch0BWTH := hmipBWTH.Channel("0001NAMING:0")
	putBool(ch0BWTH, hmenum.ParameterGlobalButtonLock)
	ch1BWTH := hmipBWTH.Channel("0001NAMING:1")
	putFloat(ch1BWTH, hmenum.ParameterSetPointTemperature)
	putFloatSensor(ch1BWTH, hmenum.ParameterActualTemperature)
	materializeNaming(t, hmipBWTH)

	cases := []struct {
		name              string
		ch                *device.Channel
		wantPrimary       bool
		wantSecondary     bool
		wantSinglePrimary bool
	}{
		{
			name:              "HmIP-PS ch3 switch primary — single in category",
			ch:                hmipPS.Channel("0001NAMING:3"),
			wantPrimary:       true,
			wantSecondary:     false,
			wantSinglePrimary: true,
		},
		{
			name:              "HmIP-BWTH ch1 climate primary — single in category",
			ch:                hmipBWTH.Channel("0001NAMING:1"),
			wantPrimary:       true,
			wantSecondary:     false,
			wantSinglePrimary: true,
		},
		{
			name:              "HmIP-BWTH ch0 lock primary (button-lock) — single in category",
			ch:                hmipBWTH.Channel("0001NAMING:0"),
			wantPrimary:       true,
			wantSecondary:     false,
			wantSinglePrimary: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.ch == nil {
				t.Fatalf("channel not found for case %q", tc.name)
			}
			if got := tc.ch.IsCustomDPPrimaryChannel(); got != tc.wantPrimary {
				t.Errorf("IsCustomDPPrimaryChannel = %v, want %v", got, tc.wantPrimary)
			}
			if got := tc.ch.IsCustomDPSecondaryChannel(); got != tc.wantSecondary {
				t.Errorf("IsCustomDPSecondaryChannel = %v, want %v", got, tc.wantSecondary)
			}
			if got := tc.ch.HasSinglePrimaryCustomDP(); got != tc.wantSinglePrimary {
				t.Errorf("HasSinglePrimaryCustomDP = %v, want %v", got, tc.wantSinglePrimary)
			}
		})
	}
}

// ─── test stubs / fakes ───────────────────────────────────────────────────────

// namedPressChannel is a minimal ChannelInspector+ChannelNamer+ChannelNamer
// used by the press-event naming tests. It carries an arbitrary parameter set
// and an optional operator-assigned name.
type namedPressChannel struct {
	params      map[string]struct{}
	channelName string
}

func (c *namedPressChannel) HasParameter(name string) bool {
	_, ok := c.params[name]
	return ok
}

func (c *namedPressChannel) ChannelName() string {
	return c.channelName
}

// newTestSwitch returns a minimal AttachableDataPoint that satisfies the
// haComponentProvider interface so HasSinglePrimaryCustomDP can count it.
// The HAComponent() method returns "switch" so the counter in
// HasSinglePrimaryCustomDP recognises both channels as belonging to the
// same HA component and returns false (multi-primary case).
func newTestSwitch(addr string) device.AttachableDataPoint {
	return &minimalSwitchDP{addr: addr}
}

// minimalSwitchDP satisfies device.AttachableDataPoint (DataPointKey) and
// the unexported haComponentProvider interface that HasSinglePrimaryCustomDP
// uses via a type assertion (HAComponent). It avoids importing the full
// switchdev package so the contract test stays lean.
type minimalSwitchDP struct {
	addr string
}

func (m *minimalSwitchDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{ChannelAddress: m.addr}
}

func (m *minimalSwitchDP) HAComponent() string { return "switch" }
