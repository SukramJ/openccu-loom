// SPDX-License-Identifier: MIT
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

	"github.com/SukramJ/go-fabric/tlv"
)

// TestParityMatterJS_OnOffServer_AcceptsExtensionsTypeCheck verifies that
// the Switch's OnOff cluster correctly implements the required interface
// surface (On, Off, Toggle commands). This is the Go equivalent of the
// TypeScript type-check test.
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts:12
// (case "accepts extensions of off-only commands").
func TestParityMatterJS_OnOffServer_AcceptsExtensionsTypeCheck(t *testing.T) {
	t.Parallel()
	// Compile-time: Switch implements interfaces.MatterClusterServer including
	// MatterInvoke. Runtime: verify Off (0x00), On (0x01), Toggle (0x02) all
	// succeed without error — structural parity with OnOffServer.on/off/toggle.
	w := &stubWriter{}
	s := newTestSwitch(t, "VCU0000001:1", "", w)
	ctx := context.Background()

	for _, cmd := range []uint32{matterCmdOff, matterCmdOn, matterCmdToggle} {
		_, err := s.MatterInvoke(ctx, cmd, nil)
		if err != nil {
			t.Errorf("command 0x%02X returned error: %v (want nil — all three commands must be implemented)", cmd, err)
		}
	}
}

// TestParityMatterJS_OnOffServer_ToggleObserverSemanticsOn asserts that
// after toggle on an off device the OnOff attribute reads true.
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts:23
// (case "properly supports async observers" — first toggle half).
// The async observer machinery has no Go equivalent; we assert the
// state-machine semantics instead.
func TestParityMatterJS_OnOffServer_ToggleObserverSemanticsOn(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s := newTestSwitch(t, "VCU0000001:1", "", w)

	// First toggle: off→on (unobserved start is treated as off per spec).
	if _, err := s.MatterInvoke(context.Background(), matterCmdToggle, nil); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	// The southbound write carries true.
	if w.lastVal != true {
		t.Errorf("first toggle: southbound value = %v, want true", w.lastVal)
	}
	// Simulate the CCU callback confirming the new state.
	s.OnState(true)

	v, ok := s.MatterRead(matterAttrOnOff)
	if !ok || v != true {
		t.Errorf("OnOff after first toggle = (%v, %v), want (true, true)", v, ok)
	}
}

// TestParityMatterJS_OnOffServer_ToggleObserverSemanticsOff asserts that
// a second toggle (on→off) makes OnOff read false — the second half of
// the matter.js observer sequence test.
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts:23
// (case "properly supports async observers" — second toggle half).
func TestParityMatterJS_OnOffServer_ToggleObserverSemanticsOff(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s := newTestSwitch(t, "VCU0000001:1", "", w)

	// Prime state to on.
	s.OnState(true)

	// Toggle off.
	if _, err := s.MatterInvoke(context.Background(), matterCmdToggle, nil); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if w.lastVal != false {
		t.Errorf("second toggle: southbound value = %v, want false", w.lastVal)
	}
	// Simulate CCU confirmation.
	s.OnState(false)

	v, ok := s.MatterRead(matterAttrOnOff)
	if !ok || v != false {
		t.Errorf("OnOff after second toggle = (%v, %v), want (false, true)", v, ok)
	}
}

// TestParityMatterJS_OnOffServer_ObservedValuesSequence locks the two-toggle
// value sequence [true, false] — the exact assertion in matter.js's
// observer test (observedValues deep equals [true, false]).
//
// Mirrors matter.js packages/node/test/behaviors/on-off/OnOffServerTest.ts:44
// (expect(observedValues).deep.equals([true, false])).
func TestParityMatterJS_OnOffServer_ObservedValuesSequence(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s := newTestSwitch(t, "VCU0000001:1", "", w)
	ctx := context.Background()

	observed := make([]bool, 0, 2)

	// Simulate two toggles and record what each CCU-confirmed state would be.
	for i := range 2 {
		if _, err := s.MatterInvoke(ctx, matterCmdToggle, nil); err != nil {
			t.Fatalf("toggle %d: %v", i, err)
		}
		val, ok := w.lastVal.(bool)
		if !ok {
			t.Fatalf("toggle %d: southbound val type = %T, want bool", i, w.lastVal)
		}
		// Confirm state so next toggle uses the right current value.
		s.OnState(val)
		observed = append(observed, val)
	}

	want := []bool{true, false}
	if len(observed) != len(want) {
		t.Fatalf("observed len=%d, want %d", len(observed), len(want))
	}
	for i, v := range observed {
		if v != want[i] {
			t.Errorf("observed[%d] = %v, want %v", i, v, want[i])
		}
	}
}

// TestParityMatterJS_OnOffServer_DefaultOnOffIsFalse asserts that a
// freshly created server returns false for OnOff before any CCU push.
// matter.js OnOffServer initialises onOff to false (the spec default).
//
// Mirrors matter.js packages/node/src/behaviors/on-off/OnOffServer.ts
// default-state behaviour (non-nullable bool, default false).
func TestParityMatterJS_OnOffServer_DefaultOnOffIsFalse(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	v, ok := s.MatterRead(matterAttrOnOff)
	if !ok {
		t.Fatal("MatterRead(OnOff) ok=false, want true (non-nullable)")
	}
	if v.(bool) != false {
		t.Errorf("default OnOff = %v, want false", v)
	}
}

// TestParityMatterJS_OnOffServer_ClusterRevision6 pins the OnOff cluster
// revision at 6 — the value matter.js HEAD ships in on-off.element.ts.
// A revision drift here makes chip-tool's "validate cluster attributes"
// step fail and Apple's HAP mapper rejects the endpoint.
//
// Mirrors matter.js packages/model/src/standard/elements/on-off.element.ts:12
// (revision: 6).
func TestParityMatterJS_OnOffServer_ClusterRevision6(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	v, ok := s.MatterRead(matterAttrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok=false")
	}
	if got := v.(uint16); got != 6 {
		t.Errorf("ClusterRevision = %d, want 6 (matter.js HEAD on-off.element.ts)", got)
	}
}

// TestParityMatterJS_OnOffServer_WritableLTAttributesAccepted verifies that
// OnTime (0x4001), OffWaitTime (0x4002), and StartUpOnOff (0x4003) are
// accepted by MatterWrite and reflected by MatterRead. These are advertised
// as RW in matter.js on-off.element.ts:31-36, and writing them must not
// return UNSUPPORTED_ATTRIBUTE. StartUpOnOff supports null (nil) writes.
// Mirrors matter.js OnOffServer.ts:80 (offWaitTime), :102 (onTime), :39 (startUpOnOff).
func TestParityMatterJS_OnOffServer_WritableLTAttributesAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("OnTime round-trip", func(t *testing.T) {
		t.Parallel()
		s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
		if err := s.MatterWrite(ctx, matterAttrOnTime, uint16(300)); err != nil {
			t.Fatalf("MatterWrite(OnTime): %v", err)
		}
		v, ok := s.MatterRead(matterAttrOnTime)
		if !ok {
			t.Fatal("MatterRead(OnTime): ok=false")
		}
		if v.(uint16) != 300 {
			t.Errorf("OnTime = %d, want 300", v.(uint16))
		}
	})

	t.Run("OffWaitTime round-trip", func(t *testing.T) {
		t.Parallel()
		s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
		if err := s.MatterWrite(ctx, matterAttrOffWaitTime, uint16(120)); err != nil {
			t.Fatalf("MatterWrite(OffWaitTime): %v", err)
		}
		v, ok := s.MatterRead(matterAttrOffWaitTime)
		if !ok {
			t.Fatal("MatterRead(OffWaitTime): ok=false")
		}
		if v.(uint16) != 120 {
			t.Errorf("OffWaitTime = %d, want 120", v.(uint16))
		}
	})

	t.Run("StartUpOnOff null default", func(t *testing.T) {
		t.Parallel()
		s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
		v, ok := s.MatterRead(matterAttrStartUpOnOff)
		if !ok {
			t.Fatal("MatterRead(StartUpOnOff): ok=false")
		}
		if v != nil {
			t.Errorf("StartUpOnOff default = %v, want nil", v)
		}
	})

	t.Run("StartUpOnOff enum write", func(t *testing.T) {
		t.Parallel()
		s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
		if err := s.MatterWrite(ctx, matterAttrStartUpOnOff, uint8(1)); err != nil {
			t.Fatalf("MatterWrite(StartUpOnOff, 1): %v", err)
		}
		v, ok := s.MatterRead(matterAttrStartUpOnOff)
		if !ok {
			t.Fatal("MatterRead(StartUpOnOff): ok=false after write")
		}
		if v.(uint8) != 1 {
			t.Errorf("StartUpOnOff = %v, want uint8(1)", v)
		}
	})

	t.Run("StartUpOnOff null write restores nil", func(t *testing.T) {
		t.Parallel()
		s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
		// Write enum then reset to null.
		_ = s.MatterWrite(ctx, matterAttrStartUpOnOff, uint8(0))
		if err := s.MatterWrite(ctx, matterAttrStartUpOnOff, nil); err != nil {
			t.Fatalf("MatterWrite(StartUpOnOff, nil): %v", err)
		}
		v, _ := s.MatterRead(matterAttrStartUpOnOff)
		if v != nil {
			t.Errorf("StartUpOnOff after null write = %v, want nil", v)
		}
	})
}

// TestParityMatterJS_OnOffServer_OptionsAbsent locks the absence of
// OnOff.Options (0x000F). The attribute is a Zigbee-Cluster-Library
// holdover that Matter dropped — matter.js HEAD on-off.element.ts and
// chip HEAD's zzz_generated/.../OnOff/AttributeIds.h both omit it.
// Re-introducing it as a SPURIOUS-ATTR drift would surface on Apple's
// strict iOS 18.4+ schema check.
func TestParityMatterJS_OnOffServer_OptionsAbsent(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	if _, ok := s.MatterRead(spuriousOptionsID); ok {
		t.Errorf("OnOff.Options (0x000F) MUST NOT be readable — not in matter.js or chip schema")
	}
}

// TestParityMatterJS_OnOffServer_TlvBooleanWireEncoding verifies that the
// bool value produced by MatterRead(OnOff) encodes to the correct TLV
// control octets: 0x08 for false (TypeBoolFalse) and 0x09 for true
// (TypeBoolTrue), with no additional payload bytes.
//
// This is the wire-level regression guard for the TlvBoolean encoding path:
// a boolean-typed Matter attribute must use the single-byte encoding where
// the element type IS the value (no separate value byte). Using a signed/
// unsigned integer type for a boolean attribute causes chip-tool's TLVReader
// and Apple Home's MTRDevice IM-decoder to reject the attribute silently —
// chip TLVReader::Get(bool&) at src/lib/core/TLVReader.cpp only accepts
// element types 0x08/0x09; any other element type returns WRONG_TLV_TYPE.
//
// Mirrors matter.js packages/types/test/tlv/TlvBooleanTest.ts:14-17
// (encode "true" → 0x09; encode "false" → 0x08) and
// packages/node/test/behaviors/on-off/OnOffServerTest.ts:23
// (the OnOff attribute value that feeds into the cluster report pipeline).
//
// Source-Origin: derived from matter.js TlvBooleanTest.ts:14-17 + OnOffServerTest.ts:23.
func TestParityMatterJS_OnOffServer_TlvBooleanWireEncoding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state       bool
		wantByte    byte // TLV control octet
		wantTypeHex string
	}{
		{
			state:       false,
			wantByte:    0x08, // TypeBoolFalse per Matter Core Spec §A.7.2 Table 73
			wantTypeHex: "08",
		},
		{
			state:       true,
			wantByte:    0x09, // TypeBoolTrue per Matter Core Spec §A.7.2 Table 73
			wantTypeHex: "09",
		},
	}

	for _, tc := range cases {
		t.Run(tc.wantTypeHex, func(t *testing.T) {
			t.Parallel()

			s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
			// Prime the state so MatterRead returns the desired value.
			s.OnState(tc.state)

			val, ok := s.MatterRead(matterAttrOnOff)
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

			// Encode the bool through the TLV encoder and verify the wire byte.
			enc := tlv.NewEncoder()
			enc.PutBool(tlv.AnonymousTag(), boolVal)
			wire, err := enc.Bytes()
			if err != nil {
				t.Fatalf("TLV encode: %v", err)
			}

			// A TLV boolean is a single control byte with no payload.
			// Anything other than one byte is a malformed boolean TLV.
			if len(wire) != 1 {
				t.Fatalf("TLV boolean wire len=%d, want 1 (control-only, no payload)", len(wire))
			}
			if wire[0] != tc.wantByte {
				t.Errorf("TLV boolean control byte=0x%02X, want 0x%02X (%v); chip TLVReader accepts only 0x08/0x09 for bool attributes", wire[0], tc.wantByte, tc.state)
			}
		})
	}

	// Round-trip: invoke On then Off, encode each MatterRead result, verify bytes.
	t.Run("round-trip-on-off", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		s := newTestSwitch(t, "VCU0000001:1", "", w)
		ctx := context.Background()

		// Turn On → read → encode → expect 0x09.
		if _, err := s.MatterInvoke(ctx, matterCmdOn, nil); err != nil {
			t.Fatalf("On: %v", err)
		}
		s.OnState(true)

		onVal, _ := s.MatterRead(matterAttrOnOff)
		enc := tlv.NewEncoder()
		enc.PutBool(tlv.AnonymousTag(), onVal.(bool))
		wire, _ := enc.Bytes()
		if len(wire) != 1 || wire[0] != 0x09 {
			t.Errorf("after On: wire=%x, want [09]", wire)
		}

		// Turn Off → read → encode → expect 0x08.
		if _, err := s.MatterInvoke(ctx, matterCmdOff, nil); err != nil {
			t.Fatalf("Off: %v", err)
		}
		s.OnState(false)

		offVal, _ := s.MatterRead(matterAttrOnOff)
		enc2 := tlv.NewEncoder()
		enc2.PutBool(tlv.AnonymousTag(), offVal.(bool))
		wire2, _ := enc2.Bytes()
		if len(wire2) != 1 || wire2[0] != 0x08 {
			t.Errorf("after Off: wire=%x, want [08]", wire2)
		}
	})
}
