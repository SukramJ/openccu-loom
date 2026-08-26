// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package textdisplay_test

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// TestIPTextDisplayConstructorCapturesRowFormattingValueLists verifies
// that the constructor lifts the alignment and colour VALUE_LISTs off
// the device's own paramset.
//
// Those three lists had no production caller at all: the state payload
// dropped available_alignments / available_text_colors /
// available_background_colors through omitempty on every device, so no
// consumer could offer the pickers, and validateAlignment /
// validateTextColor / validateBackgroundColor — which pass anything
// while their list is empty — let an unknown label reach the CCU.
func TestIPTextDisplayConstructorCapturesRowFormattingValueLists(t *testing.T) {
	t.Parallel()

	ch := makeTextDisplayChannel(t, "HmIP-WRCD:3")
	putDisplayEnum(t, ch, "HmIP-WRCD:3", hmenum.ParameterDisplayDataAlignment, []string{"LEFT", "CENTER", "RIGHT"})
	putDisplayEnum(t, ch, "HmIP-WRCD:3", hmenum.ParameterDisplayDataTextColor, []string{"WHITE", "BLACK"})
	putDisplayEnum(t, ch, "HmIP-WRCD:3", hmenum.ParameterDisplayDataBackgroundColor, []string{"WHITE", "BLACK"})

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	td, ok := dp.(*textdisplay.TextDisplay)
	if !ok {
		t.Fatalf("constructor returned %T, want *textdisplay.TextDisplay", dp)
	}

	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"alignments", td.AvailableAlignments(), []string{"LEFT", "CENTER", "RIGHT"}},
		{"text colors", td.AvailableTextColors(), []string{"WHITE", "BLACK"}},
		{"background colors", td.AvailableBackgroundColors(), []string{"WHITE", "BLACK"}},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("available %s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// putDisplayEnum installs a writable ENUM data point carrying valueList
// on ch, matching the shape the CCU reports for the row-formatting
// parameters.
func putDisplayEnum(t *testing.T, ch *device.Channel, address string, p hmenum.Parameter, valueList []string) {
	t.Helper()
	ch.Put(generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	}))
}
