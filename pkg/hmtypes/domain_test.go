// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestNewDataPointKeyValidates(t *testing.T) {
	_, err := NewDataPointKey("", "ABC:1", hmenum.ParamsetKeyValues, "LEVEL")
	if err == nil {
		t.Error("empty interface should fail")
	}
	_, err = NewDataPointKey("iface", "", hmenum.ParamsetKeyValues, "LEVEL")
	if err == nil {
		t.Error("empty channel should fail")
	}
	_, err = NewDataPointKey("iface", "ABC:1", "", "LEVEL")
	if err == nil {
		t.Error("empty paramset should fail")
	}
	_, err = NewDataPointKey("iface", "ABC:1", hmenum.ParamsetKeyValues, "")
	if err == nil {
		t.Error("empty parameter should fail")
	}
	k, err := NewDataPointKey("iface", "ABC:1", hmenum.ParamsetKeyValues, "LEVEL")
	if err != nil {
		t.Fatal(err)
	}
	if k.String() != "iface|ABC:1|VALUES|LEVEL" {
		t.Fatalf("String=%q", k.String())
	}
}

func TestDataPointKeyDeviceAddressAndChannel(t *testing.T) {
	k := DataPointKey{InterfaceID: "iface", ChannelAddress: "ABC:5", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"}
	if k.DeviceAddress() != "ABC" {
		t.Fatalf("DeviceAddress=%q", k.DeviceAddress())
	}
	n, ok := k.ChannelNo()
	if !ok || n != 5 {
		t.Fatalf("ChannelNo()=%d,%v", n, ok)
	}
}

func TestDataPointKeyChannelNoRejectsGarbage(t *testing.T) {
	k := DataPointKey{ChannelAddress: "ABC:x"}
	if _, ok := k.ChannelNo(); ok {
		t.Error("non-numeric channel should be rejected")
	}
	k = DataPointKey{ChannelAddress: "ABC"}
	if _, ok := k.ChannelNo(); ok {
		t.Error("missing channel should be rejected")
	}
}

func TestPathDataString(t *testing.T) {
	p := PathData{CentralName: "home", DeviceModel: "HmIP-BROLL", ChannelAddress: "ABC:1", Parameter: "LEVEL"}
	got := p.String()
	want := "central/home/device/HmIP-BROLL/ABC:1/LEVEL"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPathDataSkipsEmptySegments(t *testing.T) {
	p := PathData{CentralName: "home", Parameter: "LEVEL"}
	if p.String() != "central/home/LEVEL" {
		t.Fatalf("String=%q", p.String())
	}
}

func TestParamValueEqual(t *testing.T) {
	if !IntValue(3).Equal(IntValue(3)) {
		t.Error("int equality broken")
	}
	if IntValue(3).Equal(FloatValue(3.0)) {
		t.Error("int vs float must not be equal")
	}
	if !ListValue([]string{"a", "b"}).Equal(ListValue([]string{"a", "b"})) {
		t.Error("list equality broken")
	}
	if ListValue([]string{"a"}).Equal(ListValue([]string{"a", "b"})) {
		t.Error("list length mismatch should not compare equal")
	}
}

func TestParamValueAsString(t *testing.T) {
	cases := map[string]ParamValue{
		"":      NoneValue(),
		"true":  BoolValue(true),
		"42":    IntValue(42),
		"3.14":  FloatValue(3.14),
		"hello": StringValue("hello"),
		"[a,b]": ListValue([]string{"a", "b"}),
	}
	for want, v := range cases {
		if got := v.AsString(); got != want {
			t.Errorf("AsString of %s = %q, want %q", v.Kind, got, want)
		}
	}
}
