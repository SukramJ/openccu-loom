// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package switchdev — OnOff cluster-server parity tests.
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts
// (cases ported at lines 12–46).
//
// Conversion pattern:
//   - Each test cites the matter.js file + case name in its header.
//   - Matter.js-specific framework constructs (MockServerNode, async
//     observers, TypeScript type checks) are translated to equivalent
//     Go semantics or skipped with a rationale comment.

package switchdev

import (
	"context"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	"github.com/SukramJ/go-fabric/tlv"
)

// TestParityMatterJS_OnOffServer_AcceptsExtensionsTypeCheck verifies that
// the Switch's OnOff server accepts the three off-only commands. This is
// the Go equivalent of the TypeScript type-check test.
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts:12
// (case "accepts extensions of off-only commands").
func TestParityMatterJS_OnOffServer_AcceptsExtensionsTypeCheck(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	ctx := context.Background()
	for _, cmd := range []uint32{onoff.CmdOff, onoff.CmdOn, onoff.CmdToggle} {
		if _, err := srv.MatterInvoke(ctx, cmd, nil); err != nil {
			t.Errorf("command 0x%02X returned error: %v (want nil — all three commands must be implemented)", cmd, err)
		}
	}
}

// TestParityMatterJS_OnOffServer_ObservedValuesSequence locks the
// two-toggle value sequence [true, false] — the exact assertion in
// matter.js's observer test (observedValues deep equals [true, false]).
// Each toggle's southbound value is what the CCU then confirms, so the
// next toggle starts from the confirmed state.
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts:23
// (case "properly supports async observers") and :44
// (expect(observedValues).deep.equals([true, false])).
func TestParityMatterJS_OnOffServer_ObservedValuesSequence(t *testing.T) {
	t.Parallel()
	s, srv, w := matterSwitch(t)
	ctx := context.Background()

	observed := make([]bool, 0, 2)
	for i := range 2 {
		if _, err := srv.MatterInvoke(ctx, onoff.CmdToggle, nil); err != nil {
			t.Fatalf("toggle %d: %v", i, err)
		}
		calls := w.snapshot()
		if len(calls) != i+1 {
			t.Fatalf("toggle %d: %d CCU write(s), want %d", i, len(calls), i+1)
		}
		val, ok := calls[i].value.(bool)
		if !ok {
			t.Fatalf("toggle %d: southbound value type = %T, want bool", i, calls[i].value)
		}
		s.OnState(val)
		if v, _ := srv.MatterRead(onoff.AttrOnOff); v != val {
			t.Fatalf("toggle %d: OnOff = %v after the CCU confirmed %v", i, v, val)
		}
		observed = append(observed, val)
	}

	want := []bool{true, false}
	for i, v := range observed {
		if v != want[i] {
			t.Errorf("observed[%d] = %v, want %v", i, v, want[i])
		}
	}
}

// TestParityMatterJS_OnOffServer_ClusterRevisionMatchesMatterJS pins the
// revision the switch's OnOff server reports against the matter.js
// snapshot. A revision drift makes chip-tool's attribute validation
// fail and Apple's HAP mapper reject the endpoint.
//
// Mirrors matter.js packages/model/src/standard/elements/on-off.element.ts.
func TestParityMatterJS_OnOffServer_ClusterRevisionMatchesMatterJS(t *testing.T) {
	t.Parallel()
	el := hmLgtLoadOnOffElement(t)
	_, srv, _ := matterSwitch(t)
	v, ok := srv.MatterRead(0xFFFD)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok=false")
	}
	if v != el.Revision {
		t.Errorf("ClusterRevision = %v, want %d (matter.js on-off.element.ts)", v, el.Revision)
	}
}

// TestParityMatterJS_OnOffServer_StartUpOnOffNullable verifies that
// StartUpOnOff starts null and a null write after an enum write restores
// null (quality X in matter.js on-off.element.ts).
func TestParityMatterJS_OnOffServer_StartUpOnOffNullable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srv, _ := matterSwitch(t)
	if v, ok := srv.MatterRead(onoff.AttrStartUpOnOff); !ok || v != nil {
		t.Fatalf("StartUpOnOff default = (%v, %v), want (nil, true)", v, ok)
	}
	if err := srv.MatterWrite(ctx, onoff.AttrStartUpOnOff, uint64(0)); err != nil {
		t.Fatalf("MatterWrite(StartUpOnOff, 0): %v", err)
	}
	if err := srv.MatterWrite(ctx, onoff.AttrStartUpOnOff, nil); err != nil {
		t.Fatalf("MatterWrite(StartUpOnOff, nil): %v", err)
	}
	if v, _ := srv.MatterRead(onoff.AttrStartUpOnOff); v != nil {
		t.Errorf("StartUpOnOff after null write = %v, want nil", v)
	}
}

// TestParityMatterJS_OnOffServer_OptionsAbsent locks the absence of
// OnOff.Options (0x000F). The attribute is a Zigbee-Cluster-Library
// holdover that Matter dropped — matter.js HEAD on-off.element.ts and
// chip HEAD's zzz_generated/.../OnOff/AttributeIds.h both omit it.
func TestParityMatterJS_OnOffServer_OptionsAbsent(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	_, srv, _ := matterSwitch(t)
	if _, ok := srv.MatterRead(spuriousOptionsID); ok {
		t.Errorf("OnOff.Options (0x000F) MUST NOT be readable — not in matter.js or chip schema")
	}
}

// TestParityMatterJS_OnOffServer_TlvBooleanWireEncoding verifies that the
// OnOff value the server reads after a CCU-confirmed state encodes to
// the TLV boolean control octets: 0x08 for false and 0x09 for true, with
// no payload bytes. chip TLVReader::Get(bool&) accepts only those two
// element types; anything else is WRONG_TLV_TYPE.
//
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:14-17
// (encode "true" → 0x09; encode "false" → 0x08) and
// packages/node/test/behaviors/on-off/OnOffServerTest.ts:23.
func TestParityMatterJS_OnOffServer_TlvBooleanWireEncoding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state    bool
		wantByte byte
		name     string
	}{
		{state: false, wantByte: 0x08, name: "08"},
		{state: true, wantByte: 0x09, name: "09"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, srv, _ := matterSwitch(t)
			s.OnState(tc.state)

			val, ok := srv.MatterRead(onoff.AttrOnOff)
			if !ok {
				t.Fatalf("MatterRead(OnOff) ok=false")
			}
			boolVal, isBool := val.(bool)
			if !isBool {
				t.Fatalf("MatterRead(OnOff) type=%T, want bool", val)
			}
			if boolVal != tc.state {
				t.Fatalf("MatterRead(OnOff)=%v, want %v", boolVal, tc.state)
			}

			enc := tlv.NewEncoder()
			enc.PutBool(tlv.AnonymousTag(), boolVal)
			wire, err := enc.Bytes()
			if err != nil {
				t.Fatalf("TLV encode: %v", err)
			}
			if len(wire) != 1 || wire[0] != tc.wantByte {
				t.Errorf("TLV boolean wire = %x, want [%02x]", wire, tc.wantByte)
			}
		})
	}
}
