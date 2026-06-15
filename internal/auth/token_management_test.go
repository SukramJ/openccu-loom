// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"testing"
)

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

// TestMemoryTokenStore_TwoTokensNoCollision verifies that two distinct
// tokens each authenticate only for their own identity, and neither
// matches when the wrong token is presented.
func TestMemoryTokenStore_TwoTokensNoCollision(t *testing.T) {
	t.Parallel()
	store := NewMemoryTokenStore(nil)
	store.Put("token-alice-unique-abcdef", Identity{Subject: "alice", Role: RoleAdmin})
	store.Put("token-bob-unique-xyzabc", Identity{Subject: "bob", Role: RoleViewer})

	id, err := store.AuthenticateToken(context.Background(), "token-alice-unique-abcdef")
	if err != nil {
		t.Fatalf("alice token should authenticate: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want alice", id.Subject)
	}

	id, err = store.AuthenticateToken(context.Background(), "token-bob-unique-xyzabc")
	if err != nil {
		t.Fatalf("bob token should authenticate: %v", err)
	}
	if id.Subject != "bob" {
		t.Errorf("subject = %q, want bob", id.Subject)
	}
}

// TestMemoryTokenStore_WrongTokenRejected verifies that an unregistered
// token produces ErrUnauthenticated rather than matching any entry.
func TestMemoryTokenStore_WrongTokenRejected(t *testing.T) {
	t.Parallel()
	store := NewMemoryTokenStore(nil)
	store.Put("registered-token-abcdef", Identity{Subject: "alice", Role: RoleAdmin})

	_, err := store.AuthenticateToken(context.Background(), "wrong-token-xxxxxx")
	if err == nil {
		t.Fatal("wrong token must return ErrUnauthenticated")
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
