// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newIPSoundPlayerLEDDevice builds the HmIP-MP3P channel-6 LED strip
// through the real registry: LEVEL + ACTIVITY_STATE on the channel,
// matching the IPSoundPlayerLed schema's Fields map (FieldDirection is
// Bare(ACTIVITY_STATE) on the light's own channel).
func newIPSoundPlayerLEDDevice(t *testing.T) (*SoundPlayerLED, *device.Channel) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "VCU3456789",
		Model:        "HmIP-MP3P",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch := dev.AddChannel("VCU3456789:6", 6, "DIMMER_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	putWritableFloatDP(ch, hmenum.ParameterLevel)
	putEnumSensorDP(ch, hmenum.ParameterActivityState, activityStateValueList)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}

	cdp := ch.CustomDataPoint()
	led, ok := cdp.(*SoundPlayerLED)
	if !ok {
		t.Fatalf("custom data point is %T, want *SoundPlayerLED", cdp)
	}
	return led, ch
}

// TestSoundPlayerLEDBindsDirection is the regression guard for
// FieldDirection on the IPSoundPlayerLed schema (ACTIVITY_STATE): the
// profile maps it directly onto the LED's own channel, HmIP-MP3P carries
// ACTIVITY_STATE on channel 6, and SoundPlayerLED held no pointer to it —
// the sibling IPSoundPlayer profile on channel 2 was already fixed for the
// same parameter (sp.direction in siren/sound.go), the LED channel was not.
func TestSoundPlayerLEDBindsDirection(t *testing.T) {
	t.Parallel()
	led, ch := newIPSoundPlayerLEDDevice(t)

	activity, ok := ch.Parameter(hmenum.ParameterActivityState).(*generic.Sensor[int32])
	if !ok {
		t.Fatal("ACTIVITY_STATE is not an enum sensor data point")
	}
	activity.OnEvent(1) // "UP"

	label, ok := led.ActivityState()
	if !ok {
		t.Fatal("ActivityState() reported nothing after ACTIVITY_STATE was fed — the slot is unbound")
	}
	if label != "UP" {
		t.Errorf("ActivityState() = %q, want %q", label, "UP")
	}
}
