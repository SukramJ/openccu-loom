// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newValuesDP(channelAddr, parameter string) *generic.DataPoint[bool] {
	return generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      parameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

func makeDevice(name, model, address string) *Device {
	return New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     address,
		Model:       model,
		Name:        name,
	})
}

// ---------------------------------------------------------------------------
// stripChannelAddressSuffix
// ---------------------------------------------------------------------------

func TestStripChannelAddressSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"Wohnzimmer:3", "Wohnzimmer"},
		{"Wohnzimmer", "Wohnzimmer"},
		{"Foo:bar", "Foo:bar"}, // non-numeric suffix — keep
		{"foo:1:2", "foo:1"},   // only LAST :N stripped when numeric
		{":3", ":3"},           // idx == 0, not > 0 — keep
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := stripChannelAddressSuffix(tc.input)
			if got != tc.want {
				t.Fatalf("stripChannelAddressSuffix(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// baseChannelName
// ---------------------------------------------------------------------------

func TestBaseChannelName(t *testing.T) {
	t.Parallel()

	t.Run("explicit label returns as-is", func(t *testing.T) {
		t.Parallel()
		d := makeDevice("Schlafzimmer", "HmIP-eTRV", "000ABC")
		ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
		ch.Name = "Schlafzimmer Heizung"
		got := baseChannelName(ch, d.Model, d.Name)
		if got != "Schlafzimmer Heizung" {
			t.Fatalf("got %q, want %q", got, "Schlafzimmer Heizung")
		}
	})

	t.Run("auto-default Name falls back to deviceName:channelNo", func(t *testing.T) {
		t.Parallel()
		d := makeDevice("Schlafzimmer", "HmIP-eTRV", "000ABC")
		ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
		ch.Name = "HmIP-eTRV 000ABC:1" // auto-default form
		got := baseChannelName(ch, d.Model, d.Name)
		want := "Schlafzimmer:1"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("empty Name falls back to deviceName:channelNo", func(t *testing.T) {
		t.Parallel()
		d := makeDevice("Schlafzimmer", "HmIP-eTRV", "000ABC")
		ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
		// ch.Name == "" (zero value)
		got := baseChannelName(ch, d.Model, d.Name)
		want := "Schlafzimmer:1"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("empty deviceName AND empty channel Name returns empty", func(t *testing.T) {
		t.Parallel()
		d := makeDevice("", "HmIP-eTRV", "000ABC")
		ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
		// ch.Name == "" (zero value), deviceName == ""
		got := baseChannelName(ch, d.Model, d.Name)
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

// ---------------------------------------------------------------------------
// BuildDataPointName
// ---------------------------------------------------------------------------
//
// NameData composition (Name / FullName / TranslatedName / TranslatedFullName)
// is unit-tested in `internal/model/naming/namedata_test.go`. The tests below
// cover the channel-aware factory; they exercise the NameData type only as
// the function's return value.

func TestBuildDataPointName_NilChannel(t *testing.T) {
	t.Parallel()
	got := BuildDataPointName(nil, "STATE", "")
	if got != naming.EmptyNameData {
		t.Fatalf("nil channel must return EmptyNameData, got %+v", got)
	}
}

func TestBuildDataPointName_SingleChannelNoTranslation(t *testing.T) {
	t.Parallel()
	// Single channel with STATE — no :N disambiguation postfix expected.
	d := makeDevice("Wohnzimmer", "HmIP-PS", "000ABC")
	ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
	// Only one channel → IsParameterInMultipleChannels returns false.

	nd := BuildDataPointName(ch, "STATE", "")
	if nd.ChannelName != "Wohnzimmer" {
		t.Errorf("ChannelName = %q, want %q", nd.ChannelName, "Wohnzimmer")
	}
	if nd.ParameterName != "State" {
		t.Errorf("ParameterName = %q, want %q", nd.ParameterName, "State")
	}
	if nd.TranslatedParameterName != "" {
		t.Errorf("TranslatedParameterName = %q, want empty", nd.TranslatedParameterName)
	}
	if nd.Name() != "State" {
		t.Errorf("Name() = %q, want %q", nd.Name(), "State")
	}
	if nd.FullName() != "Wohnzimmer State" {
		t.Errorf("FullName() = %q, want %q", nd.FullName(), "Wohnzimmer State")
	}
}

func TestBuildDataPointName_MultiChannel(t *testing.T) {
	t.Parallel()
	// Two channels both exposing STATE → " chN" postfix for non-zero channels.
	d := makeDevice("Wohnzimmer", "HmIP-PS", "000ABC")
	ch0 := d.AddChannel("000ABC:0", 0, "", hmenum.ParamsetKeyValues)
	ch1 := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("000ABC:2", 2, "", hmenum.ParamsetKeyValues)

	// Put STATE on all three channels so IsParameterInMultipleChannels returns true.
	ch0.Put(newValuesDP("000ABC:0", "STATE"))
	ch1.Put(newValuesDP("000ABC:1", "STATE"))
	ch2.Put(newValuesDP("000ABC:2", "STATE"))

	t.Run("channel 0 gets no postfix even when multi-channel", func(t *testing.T) {
		t.Parallel()
		nd := BuildDataPointName(ch0, "STATE", "")
		if nd.ParameterName != "State" {
			t.Errorf("ch0 ParameterName = %q, want %q", nd.ParameterName, "State")
		}
	})

	t.Run("channel 1 gets ch1 postfix", func(t *testing.T) {
		t.Parallel()
		nd := BuildDataPointName(ch1, "STATE", "")
		if nd.ParameterName != "State ch1" {
			t.Errorf("ch1 ParameterName = %q, want %q", nd.ParameterName, "State ch1")
		}
	})

	t.Run("channel 2 gets ch2 postfix", func(t *testing.T) {
		t.Parallel()
		nd := BuildDataPointName(ch2, "STATE", "")
		if nd.ParameterName != "State ch2" {
			t.Errorf("ch2 ParameterName = %q, want %q", nd.ParameterName, "State ch2")
		}
	})
}

func TestBuildDataPointName_MultiChannelWithTranslation(t *testing.T) {
	t.Parallel()
	d := makeDevice("Wohnzimmer", "HmIP-PS", "000ABC")
	ch1 := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("000ABC:2", 2, "", hmenum.ParamsetKeyValues)

	ch1.Put(newValuesDP("000ABC:1", "STATE"))
	ch2.Put(newValuesDP("000ABC:2", "STATE"))

	nd := BuildDataPointName(ch1, "STATE", "Status")
	if nd.TranslatedParameterName != "Status ch1" {
		t.Errorf("TranslatedParameterName = %q, want %q", nd.TranslatedParameterName, "Status ch1")
	}
	if nd.ParameterName != "State ch1" {
		t.Errorf("ParameterName = %q, want %q", nd.ParameterName, "State ch1")
	}
}

func TestBuildDataPointName_UniqueCustomNameSkipsPostfix(t *testing.T) {
	t.Parallel()
	// STATE exists on multiple channels, but channel 3 carries a unique
	// custom name that already identifies it — no " chN" postfix. The
	// derived-name sibling keeps the postfix.
	d := makeDevice("Wohnzimmer", "HmIP-BSM", "000ABC")
	ch3 := d.AddChannel("000ABC:3", 3, "", hmenum.ParamsetKeyValues)
	ch4 := d.AddChannel("000ABC:4", 4, "", hmenum.ParamsetKeyValues)
	ch3.Put(newValuesDP("000ABC:3", "STATE"))
	ch4.Put(newValuesDP("000ABC:4", "STATE"))
	ch3.Name = "Relay Status"

	nd := BuildDataPointName(ch3, "STATE", "Status")
	if nd.ParameterName != "State" {
		t.Errorf("unique custom name: ParameterName = %q, want %q", nd.ParameterName, "State")
	}
	if nd.TranslatedParameterName != "Status" {
		t.Errorf("unique custom name: TranslatedParameterName = %q, want %q",
			nd.TranslatedParameterName, "Status")
	}
	if nd.ChannelName != "Relay Status" {
		t.Errorf("unique custom name: ChannelName = %q, want %q", nd.ChannelName, "Relay Status")
	}

	nd4 := BuildDataPointName(ch4, "STATE", "")
	if nd4.ParameterName != "State ch4" {
		t.Errorf("derived sibling: ParameterName = %q, want %q", nd4.ParameterName, "State ch4")
	}
}

func TestBuildDataPointName_CustomNameWithChannelNoKeepsPostfix(t *testing.T) {
	t.Parallel()
	// A custom name following the <name>:<no> scheme is treated like a
	// derived name: the :N suffix is stripped for the channel base and the
	// " chN" postfix stays.
	d := makeDevice("Wohnzimmer", "HmIP-BSM", "000ABC")
	ch4 := d.AddChannel("000ABC:4", 4, "", hmenum.ParamsetKeyValues)
	ch5 := d.AddChannel("000ABC:5", 5, "", hmenum.ParamsetKeyValues)
	ch4.Put(newValuesDP("000ABC:4", "STATE"))
	ch5.Put(newValuesDP("000ABC:5", "STATE"))
	ch5.Name = "Relay:1"

	nd := BuildDataPointName(ch5, "STATE", "")
	if nd.ChannelName != "Relay" {
		t.Errorf("ChannelName = %q, want %q", nd.ChannelName, "Relay")
	}
	if nd.ParameterName != "State ch5" {
		t.Errorf("ParameterName = %q, want %q", nd.ParameterName, "State ch5")
	}
}

func TestBuildDataPointName_DuplicateCustomNamesKeepPostfix(t *testing.T) {
	t.Parallel()
	// Two channels providing the same parameter share a custom name — the
	// name alone cannot identify either channel, so both keep the postfix.
	d := makeDevice("Wohnzimmer", "HmIP-BSM", "000ABC")
	ch4 := d.AddChannel("000ABC:4", 4, "", hmenum.ParamsetKeyValues)
	ch6 := d.AddChannel("000ABC:6", 6, "", hmenum.ParamsetKeyValues)
	ch4.Put(newValuesDP("000ABC:4", "STATE"))
	ch6.Put(newValuesDP("000ABC:6", "STATE"))
	ch4.Name = "Relay Twin"
	ch6.Name = "Relay Twin"

	nd4 := BuildDataPointName(ch4, "STATE", "")
	if nd4.ParameterName != "State ch4" {
		t.Errorf("ch4 ParameterName = %q, want %q", nd4.ParameterName, "State ch4")
	}
	nd6 := BuildDataPointName(ch6, "STATE", "")
	if nd6.ParameterName != "State ch6" {
		t.Errorf("ch6 ParameterName = %q, want %q", nd6.ParameterName, "State ch6")
	}
}

func TestBuildDataPointName_SameNameSiblingWithoutParameterNotAmbiguous(t *testing.T) {
	t.Parallel()
	// A sibling channel shares the custom name but does NOT provide the
	// parameter — the name still uniquely identifies the parameter-carrying
	// channel, so no postfix. The parameter is multi-channel via a third,
	// derived-name channel.
	d := makeDevice("Wohnzimmer", "HmIP-BSM", "000ABC")
	ch3 := d.AddChannel("000ABC:3", 3, "", hmenum.ParamsetKeyValues)
	ch4 := d.AddChannel("000ABC:4", 4, "", hmenum.ParamsetKeyValues)
	ch5 := d.AddChannel("000ABC:5", 5, "", hmenum.ParamsetKeyValues)
	ch3.Put(newValuesDP("000ABC:3", "STATE"))
	ch4.Put(newValuesDP("000ABC:4", "STATE"))
	ch3.Name = "Status"
	ch5.Name = "Status" // no STATE on this channel

	nd := BuildDataPointName(ch3, "STATE", "")
	if nd.ParameterName != "State" {
		t.Errorf("ParameterName = %q, want %q", nd.ParameterName, "State")
	}
}

func TestBuildDataPointName_ExplicitChannelName(t *testing.T) {
	t.Parallel()
	// Channel has a real operator-set name — no :N suffix to strip.
	d := makeDevice("Schlafzimmer", "HmIP-eTRV", "000ABC")
	ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
	ch.Name = "Schlafzimmer Heizung"

	nd := BuildDataPointName(ch, "SET_POINT_TEMPERATURE", "")
	if nd.ChannelName != "Schlafzimmer Heizung" {
		t.Errorf("ChannelName = %q, want %q", nd.ChannelName, "Schlafzimmer Heizung")
	}
}

func TestBuildDataPointName_AutoDefaultChannelName(t *testing.T) {
	t.Parallel()
	// Channel.Name equals the auto-default "Model Address" form →
	// falls back to deviceName:channelNo then strips the :N suffix.
	d := makeDevice("Wohnzimmer", "HmIP-eTRV", "000ABC")
	ch := d.AddChannel("000ABC:1", 1, "", hmenum.ParamsetKeyValues)
	ch.Name = "HmIP-eTRV 000ABC:1" // matches autoDefault

	nd := BuildDataPointName(ch, "STATE", "")
	// baseChannelName returns "Wohnzimmer:1"; stripChannelAddressSuffix yields "Wohnzimmer".
	if nd.ChannelName != "Wohnzimmer" {
		t.Errorf("ChannelName = %q, want %q", nd.ChannelName, "Wohnzimmer")
	}
}
