// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestHAComponent verifies that Lock always reports "lock" as the
// HA-Discovery component name regardless of kind or capabilities.
func TestHAComponent(t *testing.T) {
	t.Parallel()
	for _, kind := range []Kind{KindIP, KindRF, KindButton} {
		r := newRig(t, "HmIP-DLD:1", kind, &stubWriter{}, custom.LockCapabilities{})
		if got := r.lock.HAComponent(); got != "lock" {
			t.Errorf("kind=%d: HAComponent() = %q, want %q", kind, got, "lock")
		}
	}
}

// TestTopicSlot_ParseableAddress verifies TopicSlot for a well-formed
// channel address (format "DEVICE_ADDR:CHANNEL").
func TestTopicSlot_ParseableAddress(t *testing.T) {
	t.Parallel()
	r := newRig(t, "VCU1234567:3", KindIP, &stubWriter{}, custom.LockCapabilities{})
	slot := r.lock.TopicSlot()
	if slot.Address != "VCU1234567" {
		t.Errorf("TopicSlot.Address = %q, want %q", slot.Address, "VCU1234567")
	}
	if slot.Channel != 3 {
		t.Errorf("TopicSlot.Channel = %d, want 3", slot.Channel)
	}
	if slot.Parameter != "lock" {
		t.Errorf("TopicSlot.Parameter = %q, want %q", slot.Parameter, "lock")
	}
}

// TestTopicSlot_UnparsableAddress verifies TopicSlot degrades gracefully
// when the address does not contain a colon separator.
func TestTopicSlot_UnparsableAddress(t *testing.T) {
	t.Parallel()
	r := newRig(t, "PLAIN", KindIP, &stubWriter{}, custom.LockCapabilities{})
	slot := r.lock.TopicSlot()
	// Falls back to full address in the slot, channel 0.
	if slot.Address != "PLAIN" {
		t.Errorf("TopicSlot.Address = %q, want %q", slot.Address, "PLAIN")
	}
	if slot.Channel != 0 {
		t.Errorf("TopicSlot.Channel = %d, want 0 for un-parsable address", slot.Channel)
	}
}

// TestIsLocking_True verifies IsLocking returns true when DIRECTION=LOCKING.
func TestIsLocking_True(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.dirDP.OnEvent(string(DirectionLocking))
	if !r.lock.IsLocking() {
		t.Error("IsLocking must be true after LOCKING direction event")
	}
	if r.lock.IsUnlocking() {
		t.Error("IsUnlocking must be false when direction is LOCKING")
	}
}

// TestIsUnlocking_True verifies IsUnlocking returns true when DIRECTION=UNLOCKING.
func TestIsUnlocking_True(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.dirDP.OnEvent(string(DirectionUnlock))
	if !r.lock.IsUnlocking() {
		t.Error("IsUnlocking must be true after UNLOCKING direction event")
	}
	if r.lock.IsLocking() {
		t.Error("IsLocking must be false when direction is UNLOCKING")
	}
}

// TestIsLocking_NoDirection verifies that IsLocking returns false when
// the direction DP has not been observed.
func TestIsLocking_NoDirection(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if r.lock.IsLocking() {
		t.Error("IsLocking must be false when direction not yet observed")
	}
}

// TestIsUnlocking_NoDirection verifies that IsUnlocking returns false
// when the direction DP has not been observed.
func TestIsUnlocking_NoDirection(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if r.lock.IsUnlocking() {
		t.Error("IsUnlocking must be false when direction not yet observed")
	}
}

// TestDirection_NilDP verifies Direction returns (DirectionNone, false)
// when the direction DP is absent (e.g. KindRF lock).
func TestDirection_NilDP(t *testing.T) {
	t.Parallel()
	r := newRFRig(t)
	d, ok := r.lock.Direction()
	if ok {
		t.Error("Direction must not be observed on KindRF lock (no DIRECTION DP)")
	}
	if d != DirectionNone {
		t.Errorf("Direction = %q, want DirectionNone", d)
	}
}
