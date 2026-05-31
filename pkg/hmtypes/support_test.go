// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --------------------------------------------------------------------------
// ToBool
// --------------------------------------------------------------------------

func TestToBool_Bool(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   bool
		want bool
	}{{true, true}, {false, false}} {
		got, err := hmtypes.ToBool(tc.in)
		if err != nil {
			t.Fatalf("ToBool(%v) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ToBool(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestToBool_TrueStrings(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"y", "yes", "Y", "YES", "t", "true", "True", "TRUE", "on", "ON", "1"} {
		got, err := hmtypes.ToBool(s)
		if err != nil {
			t.Fatalf("ToBool(%q) error: %v", s, err)
		}
		if !got {
			t.Errorf("ToBool(%q) = false, want true", s)
		}
	}
}

func TestToBool_FalseStrings(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"n", "no", "false", "0", "off", "", "maybe"} {
		got, err := hmtypes.ToBool(s)
		if err != nil {
			t.Fatalf("ToBool(%q) error: %v", s, err)
		}
		if got {
			t.Errorf("ToBool(%q) = true, want false", s)
		}
	}
}

func TestToBool_InvalidType(t *testing.T) {
	t.Parallel()
	_, err := hmtypes.ToBool(42)
	if err == nil {
		t.Error("ToBool(int) want error, got nil")
	}
}

// --------------------------------------------------------------------------
// ChangedWithinSeconds
// --------------------------------------------------------------------------

func TestChangedWithinSeconds_InitTime(t *testing.T) {
	t.Parallel()
	// InitTime (epoch zero) must always return false.
	if hmtypes.ChangedWithinSeconds(hmtypes.InitTime, time.Minute) {
		t.Error("ChangedWithinSeconds(InitTime) = true, want false")
	}
}

func TestChangedWithinSeconds_Recent(t *testing.T) {
	t.Parallel()
	if !hmtypes.ChangedWithinSeconds(time.Now(), time.Minute) {
		t.Error("ChangedWithinSeconds(time.Now(), 1m) = false, want true")
	}
}

func TestChangedWithinSeconds_Expired(t *testing.T) {
	t.Parallel()
	old := time.Now().Add(-2 * time.Hour)
	if hmtypes.ChangedWithinSeconds(old, time.Minute) {
		t.Error("ChangedWithinSeconds(old, 1m) = true, want false")
	}
}

// --------------------------------------------------------------------------
// ValidateHost / IsHost
// --------------------------------------------------------------------------

func TestValidateHost_Empty(t *testing.T) {
	t.Parallel()
	if err := hmtypes.ValidateHost(""); err == nil {
		t.Error("ValidateHost(\"\") want error")
	}
}

func TestValidateHost_Whitespace(t *testing.T) {
	t.Parallel()
	if err := hmtypes.ValidateHost("   "); err == nil {
		t.Error("ValidateHost(\"   \") want error")
	}
}

func TestValidateHost_ValidHostname(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"localhost", "ccu3.local", "192.168.1.1", "my-ccu.home"} {
		if err := hmtypes.ValidateHost(h); err != nil {
			t.Errorf("ValidateHost(%q) unexpected error: %v", h, err)
		}
	}
}

func TestValidateHost_InvalidHostname(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"-bad", "bad-.local", "a b", "http://bad"} {
		if err := hmtypes.ValidateHost(h); err == nil {
			t.Errorf("ValidateHost(%q) want error, got nil", h)
		}
	}
}

func TestIsHost(t *testing.T) {
	t.Parallel()
	if !hmtypes.IsHost("192.168.1.1") {
		t.Error("IsHost(\"192.168.1.1\") = false, want true")
	}
	if hmtypes.IsHost("") {
		t.Error("IsHost(\"\") = true, want false")
	}
}

// --------------------------------------------------------------------------
// IsIPv4Address / IsIPv6Address
// --------------------------------------------------------------------------

func TestIsIPv4Address(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"192.168.1.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"::1", false},
		{"localhost", false},
		{"", false},
		{"256.0.0.1", false},
	}
	for _, tc := range cases {
		got := hmtypes.IsIPv4Address(tc.in)
		if got != tc.want {
			t.Errorf("IsIPv4Address(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsIPv6Address(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"::1", true},
		{"[::1]", true},
		{"2001:db8::1", true},
		{"192.168.1.1", false},
		{"", false},
		{"localhost", false},
	}
	for _, tc := range cases {
		got := hmtypes.IsIPv6Address(tc.in)
		if got != tc.want {
			t.Errorf("IsIPv6Address(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// --------------------------------------------------------------------------
// CleanupTextFromHTMLTags
// --------------------------------------------------------------------------

func TestCleanupTextFromHTMLTags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"<b>bold</b>", "bold"},
		{"no tags", "no tags"},
		{"<a href=\"x\">link</a>", "link"},
		{"a &amp; b", "a  b"},
		{"", ""},
	}
	for _, tc := range cases {
		got := hmtypes.CleanupTextFromHTMLTags(tc.in)
		if got != tc.want {
			t.Errorf("CleanupTextFromHTMLTags(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --------------------------------------------------------------------------
// SupportsRxMode
// --------------------------------------------------------------------------

func TestSupportsRxMode_Burst(t *testing.T) {
	t.Parallel()
	modes := []hmenum.RxMode{hmenum.RxModeBurst, hmenum.RxModeAlways}
	if !hmtypes.SupportsRxMode(hmenum.CommandRxModeBurst, modes) {
		t.Error("SupportsRxMode(BURST, [BURST,ALWAYS]) = false, want true")
	}
}

func TestSupportsRxMode_WakeupMissing(t *testing.T) {
	t.Parallel()
	modes := []hmenum.RxMode{hmenum.RxModeBurst}
	if hmtypes.SupportsRxMode(hmenum.CommandRxModeWakeup, modes) {
		t.Error("SupportsRxMode(WAKEUP, [BURST]) = true, want false")
	}
}

func TestSupportsRxMode_EmptyModes(t *testing.T) {
	t.Parallel()
	if hmtypes.SupportsRxMode(hmenum.CommandRxModeBurst, nil) {
		t.Error("SupportsRxMode(BURST, nil) = true, want false")
	}
}

// --------------------------------------------------------------------------
// HashSHA256
// --------------------------------------------------------------------------

func TestHashSHA256_Deterministic(t *testing.T) {
	t.Parallel()
	h1 := hmtypes.HashSHA256("hello")
	h2 := hmtypes.HashSHA256("hello")
	if h1 != h2 {
		t.Errorf("HashSHA256 not deterministic: %q != %q", h1, h2)
	}
}

func TestHashSHA256_DifferentInputs(t *testing.T) {
	t.Parallel()
	if hmtypes.HashSHA256("a") == hmtypes.HashSHA256("b") {
		t.Error("HashSHA256 collision for different inputs")
	}
}

func TestHashSHA256_NonEmpty(t *testing.T) {
	t.Parallel()
	h := hmtypes.HashSHA256(42)
	if h == "" {
		t.Error("HashSHA256 returned empty string")
	}
	// Base64 output should not contain spaces.
	if strings.ContainsAny(h, " \t\n") {
		t.Errorf("HashSHA256 result has whitespace: %q", h)
	}
}

// --------------------------------------------------------------------------
// DebugEnabled
// --------------------------------------------------------------------------

func TestDebugEnabled_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// In the test runner (not a debugger), this must return false.
	if hmtypes.DebugEnabled() {
		t.Error("DebugEnabled() = true in test runner, want false")
	}
}

// --------------------------------------------------------------------------
// CreateRandomDeviceAddresses
// --------------------------------------------------------------------------

func TestCreateRandomDeviceAddresses_Keys(t *testing.T) {
	t.Parallel()
	addrs := []string{"ABC001", "DEF002"}
	got := hmtypes.CreateRandomDeviceAddresses(addrs)
	if len(got) != 2 {
		t.Fatalf("CreateRandomDeviceAddresses len = %d, want 2", len(got))
	}
	for _, a := range addrs {
		if _, ok := got[a]; !ok {
			t.Errorf("CreateRandomDeviceAddresses missing key %q", a)
		}
	}
}

func TestCreateRandomDeviceAddresses_VCUPrefix(t *testing.T) {
	t.Parallel()
	got := hmtypes.CreateRandomDeviceAddresses([]string{"X1"})
	for _, v := range got {
		if !strings.HasPrefix(v, "VCU") {
			t.Errorf("CreateRandomDeviceAddresses value %q lacks VCU prefix", v)
		}
	}
}

// --------------------------------------------------------------------------
// IsPort (A7v4-09)
// --------------------------------------------------------------------------

func TestIsPort_ValidRange(t *testing.T) {
	t.Parallel()
	for _, p := range []int{0, 1, 8080, 65535} {
		if !hmtypes.IsPort(p) {
			t.Errorf("IsPort(%d) = false, want true", p)
		}
	}
}

func TestIsPort_Invalid(t *testing.T) {
	t.Parallel()
	for _, p := range []int{-1, 65536, 100000} {
		if hmtypes.IsPort(p) {
			t.Errorf("IsPort(%d) = true, want false", p)
		}
	}
}

// --------------------------------------------------------------------------
// GetRxModes (A7v4-10)
// --------------------------------------------------------------------------

func TestGetRxModes_Nil(t *testing.T) {
	t.Parallel()
	if got := hmtypes.GetRxModes(nil); got != nil {
		t.Errorf("GetRxModes(nil) = %v, want nil", got)
	}
}

func TestGetRxModes_Zero(t *testing.T) {
	t.Parallel()
	v := 0
	got := hmtypes.GetRxModes(&v)
	if len(got) != 0 {
		t.Errorf("GetRxModes(0) = %v, want empty", got)
	}
}

func TestGetRxModes_BurstAndWakeup(t *testing.T) {
	t.Parallel()
	// RxModeBurst=2, RxModeWakeup=8 → bitmask 10
	v := 10
	modes := hmtypes.GetRxModes(&v)
	found := make(map[hmenum.RxMode]bool)
	for _, m := range modes {
		found[m] = true
	}
	if !found[hmenum.RxModeBurst] {
		t.Error("GetRxModes(10) missing RxModeBurst")
	}
	if !found[hmenum.RxModeWakeup] {
		t.Error("GetRxModes(10) missing RxModeWakeup")
	}
	if found[hmenum.RxModeAlways] {
		t.Error("GetRxModes(10) should not include RxModeAlways")
	}
}

func TestGetRxModes_AllBits(t *testing.T) {
	t.Parallel()
	// ALWAYS=1, BURST=2, CONFIG=4, WAKEUP=8, LAZY_CONFIG=16 → 31
	v := 31
	modes := hmtypes.GetRxModes(&v)
	if len(modes) != 5 {
		t.Errorf("GetRxModes(31) len = %d, want 5; got %v", len(modes), modes)
	}
}

// --------------------------------------------------------------------------
// ElementMatchesKey (A7v4-08)
// --------------------------------------------------------------------------

func TestElementMatchesKey_RightWildcard_Default(t *testing.T) {
	t.Parallel()
	// "HMW_" prefix-matches "HMW_IO_12_Sensor"
	if !hmtypes.ElementMatchesKey([]string{"HMW_"}, "HMW_IO_12_Sensor", true, false, true) {
		t.Error("ElementMatchesKey right-wildcard prefix match failed")
	}
}

func TestElementMatchesKey_Exact(t *testing.T) {
	t.Parallel()
	if !hmtypes.ElementMatchesKey([]string{"HmIP-RF"}, "HmIP-RF", false, false, false) {
		t.Error("exact match failed")
	}
	if hmtypes.ElementMatchesKey([]string{"HmIP-RF"}, "HmIP-RFUSB", false, false, false) {
		t.Error("exact match should not match prefix")
	}
}

func TestElementMatchesKey_CaseInsensitive(t *testing.T) {
	t.Parallel()
	if !hmtypes.ElementMatchesKey([]string{"hmip-rf"}, "HmIP-RF", true, false, false) {
		t.Error("case-insensitive exact match failed")
	}
}

func TestElementMatchesKey_Substring(t *testing.T) {
	t.Parallel()
	if !hmtypes.ElementMatchesKey([]string{"IP-R"}, "HmIP-RF", true, true, true) {
		t.Error("substring match failed")
	}
}

func TestElementMatchesKey_EmptyCompareWith(t *testing.T) {
	t.Parallel()
	if hmtypes.ElementMatchesKey([]string{"HmIP"}, "", true, false, true) {
		t.Error("empty compareWith should return false")
	}
}

func TestElementMatchesKey_EmptySearchElements(t *testing.T) {
	t.Parallel()
	if hmtypes.ElementMatchesKey([]string{}, "HmIP-RF", true, false, true) {
		t.Error("empty searchElements should return false")
	}
}

// --------------------------------------------------------------------------
// FindFreePort (A7v4-11)
// --------------------------------------------------------------------------

func TestFindFreePort_EphemeralOS(t *testing.T) {
	t.Parallel()
	port, err := hmtypes.FindFreePort(0, 0)
	if err != nil {
		t.Fatalf("FindFreePort(0,0) error: %v", err)
	}
	if !hmtypes.IsPort(port) || port == 0 {
		t.Errorf("FindFreePort(0,0) = %d, want valid ephemeral port", port)
	}
}

func TestFindFreePort_Range(t *testing.T) {
	t.Parallel()
	// Use high ephemeral range to avoid conflicts; one port in a wide
	// window should be free.
	port, err := hmtypes.FindFreePort(40000, 41000)
	if err != nil {
		t.Fatalf("FindFreePort(40000,41000) error: %v", err)
	}
	if port < 40000 || port > 41000 {
		t.Errorf("FindFreePort(40000,41000) = %d, out of range", port)
	}
}

func TestFindFreePort_InvalidRange(t *testing.T) {
	t.Parallel()
	_, err := hmtypes.FindFreePort(500, 100)
	if err == nil {
		t.Fatal("FindFreePort with lo>hi should return error")
	}
	if !errors.Is(err, hmtypes.ErrPortRangeInvalid) {
		t.Errorf("FindFreePort invalid range: got %v, want ErrPortRangeInvalid", err)
	}
}

// --------------------------------------------------------------------------
// isValidHostname / ValidateHost edge cases
// --------------------------------------------------------------------------

// TestValidateHost_TooLongHostname verifies that a hostname longer than 253
// characters is rejected (exercises line 58-60 of support.go).
// Use 'g' characters (not hex digits) so the string cannot accidentally
// match the ipv6Pattern (^[0-9a-fA-F:]+$) in ValidateHost.
func TestValidateHost_TooLongHostname(t *testing.T) {
	t.Parallel()
	host := strings.Repeat("g", 254) // 254 chars > 253-char DNS limit; 'g' is not a hex digit
	if err := hmtypes.ValidateHost(host); err == nil {
		t.Errorf("ValidateHost(%d-char host) want error, got nil", len(host))
	}
}

// TestValidateHost_EmptyLabel verifies that a hostname with an empty label
// (consecutive dots) is rejected (exercises line 63-65 of support.go).
func TestValidateHost_EmptyLabel(t *testing.T) {
	t.Parallel()
	if err := hmtypes.ValidateHost("a..b"); err == nil {
		t.Error("ValidateHost(\"a..b\") want error, got nil (empty label)")
	}
}

// --------------------------------------------------------------------------
// SupportsRxMode: Wakeup-match path
// --------------------------------------------------------------------------

// TestSupportsRxMode_WakeupMatch verifies that a WAKEUP command mode matches
// when the rx-mode set contains RxModeWakeup (exercises line 169-171 of
// support.go — the inner return-true branch).
func TestSupportsRxMode_WakeupMatch(t *testing.T) {
	t.Parallel()
	modes := []hmenum.RxMode{hmenum.RxModeWakeup, hmenum.RxModeAlways}
	if !hmtypes.SupportsRxMode(hmenum.CommandRxModeWakeup, modes) {
		t.Error("SupportsRxMode(WAKEUP, [WAKEUP,ALWAYS]) = false, want true")
	}
}

// --------------------------------------------------------------------------
// ElementMatchesKey: left-wildcard-only (suffix match) path
// --------------------------------------------------------------------------

// TestElementMatchesKey_LeftWildcardSuffix verifies that leftWildcard=true
// and rightWildcard=false performs a HasSuffix check (exercises line
// 304-305 of support.go).
func TestElementMatchesKey_LeftWildcardSuffix(t *testing.T) {
	t.Parallel()
	// "world" is a suffix of "helloworld".
	if !hmtypes.ElementMatchesKey([]string{"world"}, "helloworld", false, true, false) {
		t.Error("ElementMatchesKey(leftWild) suffix match: got false, want true")
	}
	// "xyz" is NOT a suffix of "helloworld".
	if hmtypes.ElementMatchesKey([]string{"xyz"}, "helloworld", false, true, false) {
		t.Error("ElementMatchesKey(leftWild) non-suffix: got true, want false")
	}
}
