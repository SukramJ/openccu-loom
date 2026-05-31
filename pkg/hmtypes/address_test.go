// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ChannelAddress
// ---------------------------------------------------------------------------

func TestChannelAddress_WithChannel(t *testing.T) {
	got := ChannelAddress("ABC-123", 5)
	if got != "ABC-123:5" {
		t.Fatalf("got %q, want %q", got, "ABC-123:5")
	}
}

func TestChannelAddress_NoChannel(t *testing.T) {
	// channelNo == -1 signals "no channel" (Python None sentinel)
	got := ChannelAddress("ABC-123", -1)
	if got != "ABC-123" {
		t.Fatalf("got %q, want %q", got, "ABC-123")
	}
}

func TestChannelAddress_ZeroChannel(t *testing.T) {
	// channel 0 is valid on the wire
	got := ChannelAddress("ABC-123", 0)
	if got != "ABC-123:0" {
		t.Fatalf("got %q, want %q", got, "ABC-123:0")
	}
}

// ---------------------------------------------------------------------------
// ChannelNo
// ---------------------------------------------------------------------------

func TestChannelNo_Present(t *testing.T) {
	n, ok := ChannelNo("ABC-123:7")
	if !ok || n != 7 {
		t.Fatalf("got n=%d ok=%v, want n=7 ok=true", n, ok)
	}
}

func TestChannelNo_Absent(t *testing.T) {
	_, ok := ChannelNo("ABC-123")
	if ok {
		t.Fatal("expected ok=false for plain device address")
	}
}

func TestChannelNo_NonNumericSuffix(t *testing.T) {
	_, ok := ChannelNo("ABC-123:x")
	if ok {
		t.Fatal("expected ok=false for non-numeric channel suffix")
	}
}

func TestChannelNo_EmptySuffix(t *testing.T) {
	_, ok := ChannelNo("ABC-123:")
	if ok {
		t.Fatal("expected ok=false for empty channel suffix")
	}
}

func TestChannelNo_EmptyString(t *testing.T) {
	_, ok := ChannelNo("")
	if ok {
		t.Fatal("expected ok=false for empty string")
	}
}

// ---------------------------------------------------------------------------
// DeviceAddress
// ---------------------------------------------------------------------------

func TestDeviceAddress_WithChannel(t *testing.T) {
	got := DeviceAddress("ABC-123:3")
	if got != "ABC-123" {
		t.Fatalf("got %q, want %q", got, "ABC-123")
	}
}

func TestDeviceAddress_WithoutChannel(t *testing.T) {
	got := DeviceAddress("ABC-123")
	if got != "ABC-123" {
		t.Fatalf("got %q, want %q", got, "ABC-123")
	}
}

func TestDeviceAddress_EmptyString(t *testing.T) {
	got := DeviceAddress("")
	if got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// SplitChannelAddress
// ---------------------------------------------------------------------------

func TestSplitChannelAddress_ChannelPresent(t *testing.T) {
	dev, ch, ok := SplitChannelAddress("ABC-123:12")
	if !ok || dev != "ABC-123" || ch != 12 {
		t.Fatalf("got dev=%q ch=%d ok=%v, want dev=ABC-123 ch=12 ok=true", dev, ch, ok)
	}
}

func TestSplitChannelAddress_NoChannel(t *testing.T) {
	dev, ch, ok := SplitChannelAddress("ABC-123")
	if ok || dev != "ABC-123" || ch != -1 {
		t.Fatalf("got dev=%q ch=%d ok=%v, want dev=ABC-123 ch=-1 ok=false", dev, ch, ok)
	}
}

func TestSplitChannelAddress_PythonNoneSuffix(t *testing.T) {
	// Python get_split_channel_address handled "ADDR:None" defensively.
	dev, ch, ok := SplitChannelAddress("ABC-1:None")
	if ok || dev != "ABC-1" || ch != -1 {
		t.Fatalf("got dev=%q ch=%d ok=%v, want dev=ABC-1 ch=-1 ok=false", dev, ch, ok)
	}
}

func TestSplitChannelAddress_MultipleColons(t *testing.T) {
	// Only the first colon is the separator; extra colons make it non-numeric.
	dev, ch, ok := SplitChannelAddress("ABC-1:2:3")
	if ok || dev != "ABC-1" || ch != -1 {
		t.Fatalf("got dev=%q ch=%d ok=%v, want dev=ABC-1 ch=-1 ok=false", dev, ch, ok)
	}
}

func TestSplitChannelAddress_EmptyString(t *testing.T) {
	dev, ch, ok := SplitChannelAddress("")
	if ok || dev != "" || ch != -1 {
		t.Fatalf("got dev=%q ch=%d ok=%v, want dev= ch=-1 ok=false", dev, ch, ok)
	}
}

// ---------------------------------------------------------------------------
// IsChannelAddress
// ---------------------------------------------------------------------------

func TestIsChannelAddress_Valid(t *testing.T) {
	cases := []string{
		"ABC-1:0",
		"ABC-1:99",
		"ABC-1:100",
		"HmIP-RF:1",
		"0123456789:5",
	}
	for _, addr := range cases {
		if !IsChannelAddress(addr) {
			t.Errorf("expected IsChannelAddress(%q)=true", addr)
		}
	}
}

func TestIsChannelAddress_Invalid(t *testing.T) {
	cases := []string{
		"",
		"ABC",  // no colon
		"AB:1", // too short (< 5 chars before colon)
		"A-VERY-LONG-ADDRESS-THAT-IS-WAY-TOO-LONG:1", // >20 chars before colon
		"ABC-1:1000", // channel > 999 (4 digits)
		"ABC-1:",     // empty channel
		"ABC-1:x",    // non-digit channel
	}
	for _, addr := range cases {
		if IsChannelAddress(addr) {
			t.Errorf("expected IsChannelAddress(%q)=false", addr)
		}
	}
}

// ---------------------------------------------------------------------------
// IsDeviceAddress
// ---------------------------------------------------------------------------

func TestIsDeviceAddress_Valid(t *testing.T) {
	cases := []string{
		"ABC-1",
		"ABCDE",
		"0123456789abc",
		"HmIP-RF-12345",
	}
	for _, addr := range cases {
		if !IsDeviceAddress(addr) {
			t.Errorf("expected IsDeviceAddress(%q)=true", addr)
		}
	}
}

func TestIsDeviceAddress_Invalid(t *testing.T) {
	cases := []string{
		"",
		"AB", // too short
		"A-VERY-LONG-ADDRESS-THAT-IS-WAY-TOO-LONG", // >20 chars
		"ABC-1:1", // colon present
		"ABC 1",   // space
	}
	for _, addr := range cases {
		if IsDeviceAddress(addr) {
			t.Errorf("expected IsDeviceAddress(%q)=false", addr)
		}
	}
}

// ---------------------------------------------------------------------------
// IsParamsetKey
// ---------------------------------------------------------------------------

func TestIsParamsetKey_Valid(t *testing.T) {
	cases := []string{
		"CALCULATED",
		"COMBINED",
		"DUMMY",
		"LINK",
		"MASTER",
		"SERVICE",
		"VALUES",
	}
	for _, k := range cases {
		if !IsParamsetKey(k) {
			t.Errorf("expected IsParamsetKey(%q)=true", k)
		}
	}
}

func TestIsParamsetKey_Invalid(t *testing.T) {
	cases := []string{
		"",
		"values", // case-sensitive
		"UNKNOWN",
		"PARAMSET",
		"VALUES ", // trailing space
	}
	for _, k := range cases {
		if IsParamsetKey(k) {
			t.Errorf("expected IsParamsetKey(%q)=false", k)
		}
	}
}
