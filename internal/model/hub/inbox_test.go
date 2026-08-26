// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import "testing"

// TestInboxReplaceStampsFirstSeenOnNewEntryAndCarriesItOverOnRescan is the
// regression guard for the permanently-zero FirstSeen defect: the hub-scan
// caller never populates FirstSeen (the CCU's inbox query reports no
// first-detection timestamp of its own), and Inbox.Replace rebuilds the
// whole list from scratch on every periodic scan — without carry-over the
// timestamp would reset to "now" every 5 minutes instead of reflecting when
// the device actually first appeared.
func TestInboxReplaceStampsFirstSeenOnNewEntryAndCarriesItOverOnRescan(t *testing.T) {
	t.Parallel()
	in := NewInbox()

	in.Replace([]InboxDevice{{Address: "DEV001", Name: "Lamp"}})
	first := in.List()
	if len(first) != 1 {
		t.Fatalf("List = %+v, want 1 entry", first)
	}
	if first[0].FirstSeen == 0 {
		t.Fatal("FirstSeen is 0 on first detection, want the current time")
	}
	stamp := first[0].FirstSeen

	// A rescan with the same address must keep the original stamp, even
	// though the caller passes a fresh InboxDevice literal (FirstSeen==0)
	// exactly as the hub-scan loop does.
	in.Replace([]InboxDevice{{Address: "DEV001", Name: "Lamp"}})
	second := in.List()
	if len(second) != 1 || second[0].FirstSeen != stamp {
		t.Fatalf("FirstSeen after rescan = %+v, want unchanged %d", second, stamp)
	}

	// A genuinely new address on the same rescan gets its own stamp, not
	// the carried-over one.
	in.Replace([]InboxDevice{
		{Address: "DEV001", Name: "Lamp"},
		{Address: "DEV002", Name: "Switch"},
	})
	third := in.List()
	var dev001, dev002 InboxDevice
	for _, d := range third {
		switch d.Address {
		case "DEV001":
			dev001 = d
		case "DEV002":
			dev002 = d
		}
	}
	if dev001.FirstSeen != stamp {
		t.Fatalf("DEV001 FirstSeen changed on an unrelated rescan: got %d, want %d", dev001.FirstSeen, stamp)
	}
	if dev002.FirstSeen == 0 {
		t.Fatal("DEV002 FirstSeen is 0 on first detection, want the current time")
	}
}

// TestInboxSetPendingCreationCarriesFirstSeenOver mirrors the Replace
// guard for the daemon-side deferred-creation queue: PublishPendingDevices
// rebuilds the whole queue from scratch on every change, so an address
// already pending must keep its original FirstSeen stamp.
func TestInboxSetPendingCreationCarriesFirstSeenOver(t *testing.T) {
	t.Parallel()
	in := NewInbox()

	in.SetPendingCreation([]InboxDevice{{Address: "DEV001", Name: "Lamp"}})
	first := in.List()
	if len(first) != 1 || first[0].FirstSeen == 0 {
		t.Fatalf("List = %+v, want 1 entry with a non-zero FirstSeen", first)
	}
	stamp := first[0].FirstSeen

	in.SetPendingCreation([]InboxDevice{
		{Address: "DEV001", Name: "Lamp"},
		{Address: "DEV002", Name: "Switch"},
	})
	second := in.List()
	var dev001, dev002 InboxDevice
	for _, d := range second {
		switch d.Address {
		case "DEV001":
			dev001 = d
		case "DEV002":
			dev002 = d
		}
	}
	if dev001.FirstSeen != stamp {
		t.Fatalf("DEV001 FirstSeen changed on a queue re-publish: got %d, want %d", dev001.FirstSeen, stamp)
	}
	if dev002.FirstSeen == 0 {
		t.Fatal("DEV002 FirstSeen is 0 on first detection, want the current time")
	}
}

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
