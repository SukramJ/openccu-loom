// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import (
	"errors"
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

// TestNewDataPointKeyValidationErrorsAreDistinguishableSentinels verifies
// each validation failure of NewDataPointKey returns its own sentinel error,
// distinguishable via errors.Is — not four indistinguishable anonymous
// errors.New values.
func TestNewDataPointKeyValidationErrorsAreDistinguishableSentinels(t *testing.T) {
	_, err := NewDataPointKey("", "ABC:1", hmenum.ParamsetKeyValues, "LEVEL")
	if !errors.Is(err, ErrDataPointKeyInterfaceIDRequired) {
		t.Errorf("empty interface: got %v, want ErrDataPointKeyInterfaceIDRequired", err)
	}

	_, err = NewDataPointKey("iface", "", hmenum.ParamsetKeyValues, "LEVEL")
	if !errors.Is(err, ErrDataPointKeyChannelAddressRequired) {
		t.Errorf("empty channel: got %v, want ErrDataPointKeyChannelAddressRequired", err)
	}

	_, err = NewDataPointKey("iface", "ABC:1", "", "LEVEL")
	if !errors.Is(err, ErrDataPointKeyParamsetKeyRequired) {
		t.Errorf("empty paramset: got %v, want ErrDataPointKeyParamsetKeyRequired", err)
	}

	_, err = NewDataPointKey("iface", "ABC:1", hmenum.ParamsetKeyValues, "")
	if !errors.Is(err, ErrDataPointKeyParameterRequired) {
		t.Errorf("empty parameter: got %v, want ErrDataPointKeyParameterRequired", err)
	}

	// The four sentinels must be pairwise distinct so errors.Is can tell
	// them apart.
	sentinels := []error{
		ErrDataPointKeyInterfaceIDRequired,
		ErrDataPointKeyChannelAddressRequired,
		ErrDataPointKeyParamsetKeyRequired,
		ErrDataPointKeyParameterRequired,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %d (%v) must not match sentinel %d (%v)", i, a, j, b)
			}
		}
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

// TestDataPointKeyChannelNoMatchesPackageHelper proves DataPointKey.ChannelNo
// and the package-level ChannelNo helper agree on every edge case: no
// channel suffix, a non-numeric suffix, and a negative number. The method
// delegates to the helper, but this locks the two entry points to
// identical behaviour going forward.
func TestDataPointKeyChannelNoMatchesPackageHelper(t *testing.T) {
	cases := []string{
		"ABC:1",   // present
		"ABC",     // no channel suffix
		"ABC:",    // empty suffix
		"ABC:x",   // non-numeric
		"ABC:-1",  // negative
		"ABC:007", // leading zeros
		"",        // empty address
	}
	for _, addr := range cases {
		wantN, wantOK := ChannelNo(addr)
		k := DataPointKey{ChannelAddress: addr}
		gotN, gotOK := k.ChannelNo()
		if gotOK != wantOK || (wantOK && gotN != wantN) {
			t.Errorf("ChannelAddress=%q: DataPointKey.ChannelNo()=(%d,%v), package ChannelNo()=(%d,%v)",
				addr, gotN, gotOK, wantN, wantOK)
		}
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
