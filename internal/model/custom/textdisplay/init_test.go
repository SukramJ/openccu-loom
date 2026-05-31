// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay" // trigger init()
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// makeTextDisplayChannel builds a bare device + channel for constructor testing.
func makeTextDisplayChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GHI0001"})
	return d.AddChannel(address, 3, "TEXT_DISPLAY", hmenum.ParamsetKeyValues)
}

// --- Registration tests ---

// TestIPTextDisplayConstructorIsRegistered verifies that the init()
// block registers a non-nil constructor for DeviceProfileIPTextDisplay.
func TestIPTextDisplayConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileIPTextDisplay")
	}
}

// --- Constructor returns valid DP ---

// TestIPTextDisplayConstructorReturnsValidDP verifies that the
// constructor returns a non-nil AttachableDataPoint without error.
func TestIPTextDisplayConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeTextDisplayChannel(t, "HmIP-WRCD:3")

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// --- DataPointKey tests ---

// TestIPTextDisplayConstructorDataPointKeyUsesChannelAddress verifies
// that the constructor sets a DataPointKey with the correct channel
// address.
func TestIPTextDisplayConstructorDataPointKeyUsesChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-WRCD:3"
	ch := makeTextDisplayChannel(t, addr)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.ChannelAddress != addr {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, addr)
	}
}

// TestIPTextDisplayConstructorDataPointKeyParameterIsDisplayDataCommit
// verifies that the constructor sets a DataPointKey keyed on
// DISPLAY_DATA_COMMIT — the canonical identity for the composite
// text-display DP.
func TestIPTextDisplayConstructorDataPointKeyParameterIsDisplayDataCommit(t *testing.T) {
	t.Parallel()

	ch := makeTextDisplayChannel(t, "HmIP-WRCD:3")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.Parameter != string(hmenum.ParameterDisplayDataCommit) {
		t.Errorf("DataPointKey().Parameter = %q, want %q",
			key.Parameter, hmenum.ParameterDisplayDataCommit)
	}
}

// TestIPTextDisplayConstructorDataPointKeyParamsetIsValues verifies
// that the key uses the VALUES paramset.
func TestIPTextDisplayConstructorDataPointKeyParamsetIsValues(t *testing.T) {
	t.Parallel()

	ch := makeTextDisplayChannel(t, "HmIP-WRCD:3")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("DataPointKey().ParamsetKey = %q, want %q",
			key.ParamsetKey, hmenum.ParamsetKeyValues)
	}
}

// TestIPTextDisplayConstructorSetsInterfaceIDFromDevice verifies that
// the constructor populates InterfaceID from the channel's device.
func TestIPTextDisplayConstructorSetsInterfaceIDFromDevice(t *testing.T) {
	t.Parallel()

	ch := makeTextDisplayChannel(t, "HmIP-WRCD:3")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.InterfaceID != "HmIP-RF" {
		t.Errorf("DataPointKey().InterfaceID = %q, want %q", key.InterfaceID, "HmIP-RF")
	}
}

// TestIPTextDisplayConstructorCanAttachToChannel verifies that the
// constructed DP can be attached to a channel via SetCustomDataPoint
// and retrieved back.
func TestIPTextDisplayConstructorCanAttachToChannel(t *testing.T) {
	t.Parallel()

	ch := makeTextDisplayChannel(t, "HmIP-WRCD:3")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	ch.SetCustomDataPoint(dp)
	if got := ch.CustomDataPoint(); got == nil {
		t.Fatal("channel must hold the attached custom DP")
	}
}
