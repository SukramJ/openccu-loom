// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// unsecuredHeader builds a minimal SessionID==0 (PASE / unsecured) message
// header carrying a source node id + counter — the fields decryptIfNeeded
// keys its per-source duplicate detector on.
func unsecuredHeader(sourceNodeID uint64, counter uint32) message.Header {
	return message.Header{
		SessionID:       0,
		HasSourceNodeID: true,
		SourceNodeID:    sourceNodeID,
		MessageCounter:  counter,
	}
}

// TestDecryptIfNeeded_UnsecuredDuplicateDetection verifies the C4 Part 2
// session-0 duplicate detector: a retransmitted unsecured message (same
// source node id + counter) is flagged duplicate so the caller acks it
// without re-invoking the PASE handshake handler. Mirrors matter.js
// UnsecuredSession's MessageReceptionState.
func TestDecryptIfNeeded_UnsecuredDuplicateDetection(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const src = uint64(0x0000_0000_00A1_B2C3)
	body := []byte{0x01, 0x02, 0x03}

	// First arrival of counter 5 → fresh.
	h := unsecuredHeader(src, 5)
	if _, dup, err := b.decryptIfNeeded(&h, body); err != nil || dup {
		t.Fatalf("first counter=5: dup=%v err=%v, want dup=false", dup, err)
	}
	// Retransmit of counter 5 → duplicate.
	if _, dup, err := b.decryptIfNeeded(&h, body); err != nil || !dup {
		t.Fatalf("retransmit counter=5: dup=%v err=%v, want dup=true", dup, err)
	}
	// A fresh counter from the same source → not duplicate.
	h6 := unsecuredHeader(src, 6)
	if _, dup, err := b.decryptIfNeeded(&h6, body); err != nil || dup {
		t.Fatalf("counter=6: dup=%v err=%v, want dup=false", dup, err)
	}
	// Same counter from a DIFFERENT source → not duplicate (independent window).
	hOther := unsecuredHeader(0xDEADBEEF, 5)
	if _, dup, err := b.decryptIfNeeded(&hOther, body); err != nil || dup {
		t.Fatalf("other-source counter=5: dup=%v err=%v, want dup=false", dup, err)
	}
}

// TestDecryptIfNeeded_UnsecuredNoSourceNodeID confirms that an unsecured
// message without a source node id is never flagged duplicate — there is no
// stable key to dedup on, and the handshake handler's own replay guard
// covers it.
func TestDecryptIfNeeded_UnsecuredNoSourceNodeID(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	body := []byte{0x01}
	// HasSourceNodeID defaults to false — no stable dedup key.
	h := message.Header{SessionID: 0, MessageCounter: 9}

	for i := range 3 {
		if _, dup, err := b.decryptIfNeeded(&h, body); err != nil || dup {
			t.Fatalf("no-source-node-id call %d: dup=%v err=%v, want dup=false", i, dup, err)
		}
	}
}

// TestResetPaseFailures_ClearsUnsecuredWindows verifies the window-boundary
// reset also clears the dedup state, so a counter seen in a previous window
// is fresh again after a new PASE acceptor is installed.
func TestResetPaseFailures_ClearsUnsecuredWindows(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const src = uint64(0x1122_3344)
	body := []byte{0x01}

	h := unsecuredHeader(src, 7)
	if _, dup, _ := b.decryptIfNeeded(&h, body); dup {
		t.Fatal("first counter=7 unexpectedly duplicate")
	}
	if _, dup, _ := b.decryptIfNeeded(&h, body); !dup {
		t.Fatal("retransmit counter=7 should be duplicate before reset")
	}
	// New window boundary (fresh PASE acceptor) clears the dedup windows.
	b.AttachPaseHandler(nil)
	if _, dup, _ := b.decryptIfNeeded(&h, body); dup {
		t.Fatal("counter=7 should be fresh again after the window-boundary reset")
	}
}
