// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sigma

import "testing"

// TestUnmarshalSigma1_InitiatorSessionParams verifies that a Sigma1
// carrying an initiatorSessionParams container (tag 5) surfaces the
// parsed fields via InitiatorSessionParams, and that omitting tag 5
// leaves the field nil. The responder retains these hints so its
// retransmission backoff can honour the peer's advertised idle/active
// intervals — matter.js MRP.ts:129 retransmissionIntervalOf.
func TestUnmarshalSigma1_InitiatorSessionParams(t *testing.T) {
	t.Parallel()
	ephPriv, err := newECDHKey(t)
	if err != nil {
		t.Fatalf("ecdh key: %v", err)
	}

	build := func(withParams bool) []byte {
		enc := sigmaTLVEncoder()
		enc.startStruct()
		enc.putOctets(1, make([]byte, RandomSize))
		enc.putUint16(2, 0x1234)
		enc.putOctets(3, make([]byte, 32))
		enc.putOctets(4, ephPriv)
		if withParams {
			enc.startStructTag(5)
			enc.putUint32(1, 5000) // SessionIdleInterval
			enc.putUint32(2, 1000) // SessionActiveInterval
			enc.putUint16(3, 2000) // SessionActiveThreshold
			enc.endContainer()
		}
		enc.endContainer()
		return enc.bytes()
	}

	t.Run("WithParams", func(t *testing.T) {
		t.Parallel()
		got, err := UnmarshalSigma1(build(true))
		if err != nil {
			t.Fatalf("UnmarshalSigma1: %v", err)
		}
		if got.InitiatorSessionParams == nil {
			t.Fatal("InitiatorSessionParams is nil, want populated struct")
		}
		sp := *got.InitiatorSessionParams
		if sp.SessionIdleInterval != 5000 {
			t.Errorf("SessionIdleInterval = %d, want 5000", sp.SessionIdleInterval)
		}
		if sp.SessionActiveInterval != 1000 {
			t.Errorf("SessionActiveInterval = %d, want 1000", sp.SessionActiveInterval)
		}
		if sp.SessionActiveThreshold != 2000 {
			t.Errorf("SessionActiveThreshold = %d, want 2000", sp.SessionActiveThreshold)
		}
	})

	t.Run("WithoutParams", func(t *testing.T) {
		t.Parallel()
		got, err := UnmarshalSigma1(build(false))
		if err != nil {
			t.Fatalf("UnmarshalSigma1: %v", err)
		}
		if got.InitiatorSessionParams != nil {
			t.Errorf("InitiatorSessionParams = %+v, want nil when tag 5 is omitted", got.InitiatorSessionParams)
		}
	})
}
