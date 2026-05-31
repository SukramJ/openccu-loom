// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"errors"
	"testing"
)

func TestDetectBackendRequiresXMLRPCCaller(t *testing.T) {
	_, err := DetectBackend(context.Background(), DetectionConfig{InterfaceID: "HmIP-RF"})
	if err == nil {
		t.Fatal("expected error when XMLRPCCaller is nil")
	}
}

func TestDetectBackendRequiresInterfaceID(t *testing.T) {
	_, err := DetectBackend(context.Background(), DetectionConfig{
		XMLRPCCaller: &recordingCaller{reply: []any{}},
	})
	if err == nil {
		t.Fatal("expected error when InterfaceID is empty")
	}
}

func TestDetectBackendCUxDViaBINRPC(t *testing.T) {
	// BIN-RPC ping succeeds → should detect KindCUxD.
	cfg := DetectionConfig{
		XMLRPCCaller: &recordingCaller{err: errors.New("xml-rpc unreachable")},
		BINRPCCaller: &recordingCaller{reply: "PONG"},
		InterfaceID:  "CUxD",
	}
	result, err := DetectBackend(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if result.Kind != KindCUxD {
		t.Errorf("Kind = %v, want KindCUxD", result.Kind)
	}
	if !result.Capabilities.RPCCallback {
		t.Error("CUxD capabilities should include RPCCallback")
	}
}

func TestDetectBackendHomegear(t *testing.T) {
	// XML-RPC listDevices returns a Homegear-signature device.
	xmlRPC := &recordingCaller{
		reply: []any{
			map[string]any{
				"TYPE":             "HG-HM-Sec-SC-2",
				"SOFTWARE_VERSION": "4.0.0.0",
			},
		},
	}
	cfg := DetectionConfig{
		XMLRPCCaller: xmlRPC,
		InterfaceID:  "BidCos-RF",
	}
	result, err := DetectBackend(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if result.Kind != KindHomegear {
		t.Errorf("Kind = %v, want KindHomegear", result.Kind)
	}
	if !result.IsHomegear {
		t.Error("IsHomegear should be true")
	}
	if result.SoftwareVersion != "4.0.0.0" {
		t.Errorf("SoftwareVersion = %q, want %q", result.SoftwareVersion, "4.0.0.0")
	}
}

func TestDetectBackendFallbackCCU(t *testing.T) {
	// XML-RPC returns a plain device list without Homegear markers.
	xmlRPC := &recordingCaller{
		reply: []any{
			map[string]any{
				"TYPE":    "HmIP-eTRV-2",
				"ADDRESS": "0001D3C9AB1234",
			},
		},
	}
	cfg := DetectionConfig{
		XMLRPCCaller: xmlRPC,
		InterfaceID:  "HmIP-RF",
	}
	result, err := DetectBackend(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if result.Kind != KindCCU {
		t.Errorf("Kind = %v, want KindCCU", result.Kind)
	}
	if result.IsHomegear {
		t.Error("IsHomegear should be false for CCU")
	}
}

func TestDetectBackendBINRPCFailFallsThrough(t *testing.T) {
	// BIN-RPC ping fails → should not detect CUxD, should fall back to CCU.
	cfg := DetectionConfig{
		XMLRPCCaller: &recordingCaller{reply: []any{}},
		BINRPCCaller: &recordingCaller{err: errors.New("connection refused")},
		InterfaceID:  "CUxD",
	}
	result, err := DetectBackend(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if result.Kind == KindCUxD {
		t.Error("Kind should not be KindCUxD when BIN-RPC ping fails")
	}
}

func TestSniffHomegearHGPrefix(t *testing.T) {
	devs := []any{
		map[string]any{"TYPE": "HG-RF-Module", "SOFTWARE_VERSION": "3.0"},
	}
	isHG, ver := sniffHomegear(devs)
	if !isHG {
		t.Error("expected isHomegear=true")
	}
	if ver != "3.0" {
		t.Errorf("version = %q, want %q", ver, "3.0")
	}
}

func TestSniffHomegearFWField(t *testing.T) {
	devs := []any{
		map[string]any{"FIRMWARE": "homegear 5.0.0.0"},
	}
	isHG, _ := sniffHomegear(devs)
	if !isHG {
		t.Error("expected isHomegear=true via FIRMWARE field")
	}
}

// buildReply wraps a []any into the type DetectBackend receives from XML-RPC.
func buildReply(entries ...map[string]any) any {
	out := make([]any, len(entries))
	for i, m := range entries {
		out[i] = m
	}
	return out
}

// TestDetectCCUWhenXMLRPCReturnsEmptyList verifies that an empty
// listDevices reply still yields KindCCU (no Homegear signature found).
func TestDetectCCUWhenXMLRPCReturnsEmptyList(t *testing.T) {
	t.Parallel()
	xml := &recordingCaller{reply: []any{}}
	res, err := DetectBackend(context.Background(), DetectionConfig{
		XMLRPCCaller: xml,
		InterfaceID:  "BidCos-RF",
	})
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if res.Kind != KindCCU {
		t.Errorf("Kind = %v, want KindCCU for empty list", res.Kind)
	}
}

// TestDetectHomegearByFirmwareField verifies detection via the FIRMWARE field
// containing "homegear".
func TestDetectHomegearByFirmwareField(t *testing.T) {
	t.Parallel()
	xml := &recordingCaller{
		reply: buildReply(
			map[string]any{"FIRMWARE": "Homegear 4.2.0"},
		),
	}
	res, err := DetectBackend(context.Background(), DetectionConfig{
		XMLRPCCaller: xml,
		InterfaceID:  "BidCos-RF",
	})
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if res.Kind != KindHomegear {
		t.Errorf("Kind = %v, want KindHomegear via FIRMWARE field", res.Kind)
	}
}

// TestDetectFallThroughWhenXMLRPCFails verifies that when the XML-RPC call
// fails (network error), the detector falls back to KindCCU without error.
func TestDetectFallThroughWhenXMLRPCFails(t *testing.T) {
	t.Parallel()
	xml := &recordingCaller{err: errors.New("network unreachable")}
	res, err := DetectBackend(context.Background(), DetectionConfig{
		XMLRPCCaller: xml,
		InterfaceID:  "HmIP-RF",
	})
	if err != nil {
		t.Fatalf("DetectBackend returned unexpected error: %v", err)
	}
	if res.Kind != KindCCU {
		t.Errorf("Kind = %v, want KindCCU on XML-RPC failure", res.Kind)
	}
}

// TestNilBINRPCCallerSkipsCUxDDetection verifies that when no BIN-RPC
// caller is configured, CUxD is never detected even if the interface ID
// says "CUxD".
func TestNilBINRPCCallerSkipsCUxDDetection(t *testing.T) {
	t.Parallel()
	cfg := DetectionConfig{
		XMLRPCCaller: &recordingCaller{reply: []any{}},
		BINRPCCaller: nil,
		InterfaceID:  "CUxD",
	}
	res, err := DetectBackend(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if res.Kind == KindCUxD {
		t.Error("Kind = KindCUxD with nil BINRPCCaller, want non-CUxD fallback")
	}
}

// TestDetectHomegearVersionExtractedFromSoftwareVersion verifies that the
// SOFTWARE_VERSION field in a Homegear listDevices reply is carried through to
// BackendDetectionResult.SoftwareVersion.
func TestDetectHomegearVersionExtractedFromSoftwareVersion(t *testing.T) {
	t.Parallel()
	xml := &recordingCaller{
		reply: buildReply(
			map[string]any{
				"TYPE":             "HG-Internal",
				"SOFTWARE_VERSION": "6.0.0.20260101",
			},
		),
	}
	res, err := DetectBackend(context.Background(), DetectionConfig{
		XMLRPCCaller: xml,
		InterfaceID:  "BidCos-RF",
	})
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if res.SoftwareVersion != "6.0.0.20260101" {
		t.Errorf("SoftwareVersion = %q, want 6.0.0.20260101", res.SoftwareVersion)
	}
}

// TestParseMajorMinorEdgeCases verifies the internal version parser handles
// various inputs correctly.
func TestParseMajorMinorEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version   string
		wantMajor int
		wantMinor int
	}{
		{"3.55.10.20210601", 3, 55},
		{"3.49.0.0", 3, 49},
		{"3.47.10.20190101", 3, 47},
		{"4.0.0.0", 4, 0},
		{"", 0, 0},
		{"bad-version", 0, 0},
		{"3", 0, 0},  // no dot → (0,0)
		{"3.", 3, 0}, // trailing dot, minor defaults to 0
		{"3.0", 3, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			gotMaj, gotMin := parseMajorMinor(tc.version)
			if gotMaj != tc.wantMajor || gotMin != tc.wantMinor {
				t.Errorf("parseMajorMinor(%q) = (%d, %d), want (%d, %d)",
					tc.version, gotMaj, gotMin, tc.wantMajor, tc.wantMinor)
			}
		})
	}
}
