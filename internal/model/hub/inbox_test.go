// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import "testing"

// TestInboxRemove verifies Remove drops exactly the named entry, fires the
// change callback only when something was actually removed, and is a no-op for
// an unknown address.
func TestInboxRemove(t *testing.T) {
	t.Parallel()
	in := NewInbox()
	in.Replace([]InboxDevice{
		{Address: "DEV001", Name: "Lamp"},
		{Address: "INT0000012", Name: "Group"},
	})

	var fired int
	in.OnUpdate(func([]InboxDevice) { fired++ })

	// Removing an unknown address changes nothing and fires no callback.
	in.Remove("MISSING")
	if fired != 0 {
		t.Fatalf("callback fired for a no-op remove: %d", fired)
	}
	if in.Count() != 2 {
		t.Fatalf("Count after no-op remove = %d, want 2", in.Count())
	}

	// Removing a present address drops it and fires exactly once.
	in.Remove("INT0000012")
	if fired != 1 {
		t.Fatalf("callback fired %d times, want 1", fired)
	}
	got := in.List()
	if len(got) != 1 || got[0].Address != "DEV001" {
		t.Fatalf("List after remove = %+v, want only DEV001", got)
	}

	// Removing the same address again is a no-op.
	in.Remove("INT0000012")
	if fired != 1 {
		t.Fatalf("callback fired again for an already-removed address: %d", fired)
	}
}
