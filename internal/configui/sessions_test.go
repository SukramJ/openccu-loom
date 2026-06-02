// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func makeKey(centralName, channel string) SessionKey {
	return SessionKey{
		CentralName:    centralName,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	}
}

// TestSessionStoreNewIsEmpty verifies that a fresh store has Len == 0.
func TestSessionStoreNewIsEmpty(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	if st.Len() != 0 {
		t.Fatalf("Len()=%d, want 0", st.Len())
	}
}

// TestSessionStorePutAndGet verifies that a Put'd session can be
// retrieved by the same key.
func TestSessionStorePutAndGet(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	key := makeKey("ccu1", "000ABCDE:1")
	sess := NewSession(nil, nil)
	st.Put(key, sess)

	got := st.Get(key)
	if got != sess {
		t.Fatalf("Get returned %v, want the stored session", got)
	}
	if st.Len() != 1 {
		t.Fatalf("Len()=%d, want 1", st.Len())
	}
}

// TestSessionStorePutNilIsNoOp verifies that Put(nil) does not increase Len.
func TestSessionStorePutNilIsNoOp(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	key := makeKey("ccu1", "000ABCDE:1")
	st.Put(key, nil)
	if st.Len() != 0 {
		t.Fatalf("Put(nil) should be a no-op, Len()=%d", st.Len())
	}
}

// TestSessionStoreGetMissingReturnsNil verifies that Get on an unknown
// key returns nil without panicking.
func TestSessionStoreGetMissingReturnsNil(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	if got := st.Get(makeKey("ccu1", "0MISSING:1")); got != nil {
		t.Fatalf("Get for missing key = %v, want nil", got)
	}
}

// TestSessionStoreDeletePresentKey verifies that Delete returns true and
// removes the session.
func TestSessionStoreDeletePresentKey(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	key := makeKey("ccu1", "000ABCDE:1")
	st.Put(key, NewSession(nil, nil))

	if ok := st.Delete(key); !ok {
		t.Fatal("Delete returned false for a present key, want true")
	}
	if st.Len() != 0 {
		t.Fatalf("Len()=%d after delete, want 0", st.Len())
	}
}

// TestSessionStoreDeleteMissingKey verifies that Delete returns false
// when the key is absent.
func TestSessionStoreDeleteMissingKey(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	if ok := st.Delete(makeKey("ccu1", "0MISSING:1")); ok {
		t.Fatal("Delete on absent key should return false")
	}
}

// TestSessionStoreKeys verifies that Keys returns all stored keys.
func TestSessionStoreKeys(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	k1 := makeKey("ccu1", "AAAA:1")
	k2 := makeKey("ccu2", "BBBB:1")
	st.Put(k1, NewSession(nil, nil))
	st.Put(k2, NewSession(nil, nil))

	keys := st.Keys()
	if len(keys) != 2 {
		t.Fatalf("Keys() len=%d, want 2", len(keys))
	}
	found := map[SessionKey]bool{k1: false, k2: false}
	for _, k := range keys {
		found[k] = true
	}
	for k, ok := range found {
		if !ok {
			t.Fatalf("Keys() missing %+v", k)
		}
	}
}

// TestSessionStoreReplaceSession verifies that a second Put overwrites
// the previous session for the same key.
func TestSessionStoreReplaceSession(t *testing.T) {
	t.Parallel()

	st := NewSessionStore()
	key := makeKey("ccu1", "000ABCDE:1")
	sess1 := NewSession(nil, nil)
	sess2 := NewSession(nil, nil)
	st.Put(key, sess1)
	st.Put(key, sess2)

	got := st.Get(key)
	if got != sess2 {
		t.Fatal("second Put should overwrite the first session")
	}
	if st.Len() != 1 {
		t.Fatalf("Len()=%d after replace, want 1", st.Len())
	}
}
