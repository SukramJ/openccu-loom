// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// TestHmBkGetLinksRequestsFirmwareDefaultFlagWord pins the flag word both
// XML-RPC backends send with `getLinks`.
//
// The value is 0, which the firmware documents as the default and which
// selects the LEAST detail: no link paramsets, no channel descriptions
// (OpenCCU-Base src/rfd/XmlRpcMethods.cpp:370-377). Bit 0 is GL_FLAG_GROUP —
// fold a key pair partner's links into the same result — not a metadata
// toggle (OpenCCU-Base src/libhsscomm/LogicalInstance.h:33). The five fields
// this decoder reads are delivered under flags=0 regardless, so raising any
// bit here buys nothing and bit 0 in particular would duplicate rows the
// per-channel caller already collects.
func TestHmBkGetLinksRequestsFirmwareDefaultFlagWord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		call func(Caller) error
	}{
		{"ccu", func(c Caller) error {
			_, err := NewCcuBackend(c, nil, nil).GetLinks(context.Background(), "0001ABCD:1")
			return err
		}},
		{"homegear", func(c Caller) error {
			_, err := NewHomegearBackend(c, nil).GetLinks(context.Background(), "0001ABCD:1")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x := &fakeCaller{reply: []any{}}
			if err := tc.call(x); err != nil {
				t.Fatalf("GetLinks: %v", err)
			}
			method, args, ok := loadArgs(x)
			if !ok || method != "getLinks" {
				t.Fatalf("method=%q, want getLinks", method)
			}
			if len(args) != 2 {
				t.Fatalf("args=%v, want 2 (address, flags)", args)
			}
			if args[1] != 0 {
				t.Fatalf("getLinks flag word = %v, want 0 (the firmware default); "+
					"bit 0 is GL_FLAG_GROUP, not a metadata toggle", args[1])
			}
		})
	}
}

// TestHmBkCreateSystemVariableEnumJoinsValueListWithSemicolon pins the wire
// separator of the SysVar.createEnum value list on the shipping path.
//
// The parameter key is `valList` and the ReGa side splits it on ";", so the
// separator is a wire contract, not a formatting choice. The only test that
// covered it lived on a transport-level copy that no daemon reaches.
func TestHmBkCreateSystemVariableEnumJoinsValueListWithSemicolon(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: true}
	b := NewCcuBackend(nil, x, nil)

	if _, err := b.CreateSystemVariableEnum(
		context.Background(), "Mode", []string{"Off", "Auto", "Boost"},
	); err != nil {
		t.Fatalf("CreateSystemVariableEnum: %v", err)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "SysVar.createEnum" {
		t.Fatalf("method=%q, want SysVar.createEnum", method)
	}
	if len(args) != 1 {
		t.Fatalf("args=%v, want a single params map", args)
	}
	params, ok := args[0].(map[string]any)
	if !ok {
		t.Fatalf("params type %T, want map[string]any", args[0])
	}
	got, ok := params["valList"].(string)
	if !ok {
		t.Fatalf("valList missing or not a string: %#v", params)
	}
	if got != "Off;Auto;Boost" {
		t.Fatalf("valList = %q, want %q (semicolon-separated, read back by "+
			"splitting on \";\")", got, "Off;Auto;Boost")
	}
	if strings.Contains(got, ", ") {
		t.Fatalf("valList = %q contains a comma separator", got)
	}
}

// TestHmBkGetAllDeviceDataKeyGrammarIsUniform pins the one output grammar of
// [CcuBackend.GetAllDeviceData]: channelAddress → parameter → value, which is
// the contract its caller documents and the shape the JSON-RPC fallback has
// always produced.
//
// The ReGa branch used to return the script's flat, still-escaped DP name as
// the outer key with a single literal "value" sub-key, so the two branches of
// one method answered the same question in two grammars and only one of them
// matched the documented contract.
func TestHmBkGetAllDeviceDataKeyGrammarIsUniform(t *testing.T) {
	t.Parallel()

	want := map[string]map[string]any{
		"00021BE9957782:4": {"LEVEL": 0.5},
		"0001ABCD:1":       {"STATE": true},
	}

	t.Run("rega", func(t *testing.T) {
		t.Parallel()
		// Both colon forms on purpose: the script UriEncodes the key, and
		// which characters that escapes is not stated by any source here.
		r := &fakeScriptRunner{rawJSON: `{
			"HmIP-RF.00021BE9957782%3A4.LEVEL": 0.5,
			"HmIP-RF.0001ABCD:1.STATE": true
		}`}
		b := NewCcuBackend(nil, nil, nil)
		b.SetScriptRunner(r)

		got, err := b.GetAllDeviceData(context.Background())
		if err != nil {
			t.Fatalf("GetAllDeviceData: %v", err)
		}
		hmBkAssertDeviceData(t, got, want)
	})

	t.Run("jsonrpc", func(t *testing.T) {
		t.Parallel()
		x := &fakeCaller{reply: map[string]any{
			"00021BE9957782:4": map[string]any{"LEVEL": 0.5},
			"0001ABCD:1":       map[string]any{"STATE": true},
		}}
		b := NewCcuBackend(nil, x, nil)

		got, err := b.GetAllDeviceData(context.Background())
		if err != nil {
			t.Fatalf("GetAllDeviceData: %v", err)
		}
		hmBkAssertDeviceData(t, got, want)
	})
}

// hmBkAssertDeviceData compares a GetAllDeviceData result against the expected
// channelAddress → parameter → value shape.
func hmBkAssertDeviceData(t *testing.T, got, want map[string]map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d channels %v, want %d %v", len(got), hmBkKeys(got), len(want), hmBkKeys(want))
	}
	for channel, params := range want {
		gotParams, ok := got[channel]
		if !ok {
			t.Fatalf("no entry for channel %q; keys are %v — the outer key must be the "+
				"channel address, not the script's flat data-point name", channel, hmBkKeys(got))
		}
		for name, value := range params {
			if gotParams[name] != value {
				t.Fatalf("channel %q parameter %q = %#v, want %#v (inner map: %#v)",
					channel, name, gotParams[name], value, gotParams)
			}
		}
	}
}

func hmBkKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestHmBkDetectBackendClassifiesAnyBinRpcPingAsCuxd pins the caveat that
// [DetectBackend]'s doc comment now carries: step 1 classifies KindCUxD from
// nothing but a successful BIN-RPC ping, and every CCU interface process
// answers that ping on its ordinary socket.
//
// The fake below stands in for rfd, not CUxD: it replies to `ping` with the
// one-string shape rfd's own handler accepts. If this test ever fails because
// the classification became sound, the caveat in the doc comment is what has
// to change with it.
func TestHmBkDetectBackendClassifiesAnyBinRpcPingAsCuxd(t *testing.T) {
	t.Parallel()

	res, err := DetectBackend(context.Background(), DetectionConfig{
		XMLRPCCaller: &fakeCaller{reply: []any{}},
		BINRPCCaller: &fakeCaller{reply: true},
		InterfaceID:  "BidCos-RF",
	})
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if res.Kind != KindCUxD {
		t.Fatalf("Kind = %v, want %v — a BIN-RPC ping alone still decides step 1; "+
			"if that changed on purpose, update the caveat on DetectBackend", res.Kind, KindCUxD)
	}
}

// TestHmBkSniffHomegearMatchesOnlyItsDeclaredSignatures pins the three
// signatures [sniffHomegear] recognises, so the set cannot grow or shrink
// without the comment that calls them unverified being revisited.
func TestHmBkSniffHomegearMatchesOnlyItsDeclaredSignatures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		dev     map[string]any
		want    bool
		version string
	}{
		{"type prefix", map[string]any{"TYPE": "HG-RPC", "SOFTWARE_VERSION": "0.8"}, true, "0.8"},
		{"type exact", map[string]any{"TYPE": "homegear"}, true, ""},
		{"firmware substring", map[string]any{"FIRMWARE": "Homegear 0.8.0"}, true, "Homegear 0.8.0"},
		// Stock CCU device types must not trip the branch — the measured
		// half of the claim.
		{"ccu type", map[string]any{"TYPE": "HmIP-PS", "FIRMWARE": "1.4.8"}, false, ""},
		{"ccu maintenance channel", map[string]any{"TYPE": "MAINTENANCE"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, version := sniffHomegear([]any{tc.dev})
			if got != tc.want {
				t.Fatalf("sniffHomegear(%v) = %v, want %v", tc.dev, got, tc.want)
			}
			if version != tc.version {
				t.Fatalf("version = %q, want %q", version, tc.version)
			}
		})
	}
}
