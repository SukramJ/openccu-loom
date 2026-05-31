// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import "testing"

func TestMemoryTokenStore_PutAndDeleteByID_RoundTrip(t *testing.T) {
	t.Parallel()
	store := NewMemoryTokenStore(nil)
	id := store.Put("token-abc-1234567890", Identity{Subject: "alice", Role: RoleOperator})
	if id == "" {
		t.Fatal("Put must return a stable id")
	}
	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("len=%d, want 1", len(entries))
	}
	if !store.DeleteByID(id) {
		t.Fatal("DeleteByID must remove the token")
	}
	if len(store.List()) != 0 {
		t.Fatal("post-delete list must be empty")
	}
	// Second delete = false (idempotent rejection).
	if store.DeleteByID(id) {
		t.Fatal("second delete must return false")
	}
}

func TestMemoryTokenStore_PutOverwritesExisting(t *testing.T) {
	t.Parallel()
	store := NewMemoryTokenStore(nil)
	store.Put("same-token-1234567890", Identity{Subject: "alice", Role: RoleViewer})
	store.Put("same-token-1234567890", Identity{Subject: "alice", Role: RoleAdmin})
	entries := store.List()
	if len(entries) != 1 || entries[0].Role != RoleAdmin {
		t.Fatalf("overwrite failed: %+v", entries)
	}
}

func TestMemoryTokenStore_DeleteByID_UnknownReturnsFalse(t *testing.T) {
	t.Parallel()
	store := NewMemoryTokenStore(nil)
	if store.DeleteByID("never-stored") {
		t.Fatal("unknown id must return false")
	}
}

func TestMemoryTokenStore_PutOnNilMap_BootstrapsLazily(t *testing.T) {
	t.Parallel()
	// Direct field init with nil map exercises the lazy-bootstrap
	// branch in Put. NewMemoryTokenStore always initialises the map,
	// so we construct the store manually.
	store := &MemoryTokenStore{}
	id := store.Put("tok-1234567890", Identity{Subject: "x", Role: RoleViewer})
	if id == "" {
		t.Fatal("Put must bootstrap nil map and return id")
	}
}

func TestMemoryUserStore_List_SortsByUsername(t *testing.T) {
	t.Parallel()
	store := NewMemoryUserStore()
	store.Put("charlie", "s", RoleViewer)
	store.Put("alice", "s", RoleAdmin)
	store.Put("bob", "s", RoleOperator)
	got := store.List()
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	want := []string{"alice", "bob", "charlie"}
	for i, u := range want {
		if got[i].Username != u {
			t.Errorf("got[%d].Username = %q, want %q", i, got[i].Username, u)
		}
	}
}
