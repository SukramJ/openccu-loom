// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
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

// TestSessionStorePutSweepsSessionsPastMaxAge pins the fix for the
// unbounded leak: a session nobody saved or discarded (no explicit
// Delete) must still be reclaimed once it has sat past [sessionMaxAge],
// via the amortised sweep on the next Put — the only path sessions
// currently grow through.
func TestSessionStorePutSweepsSessionsPastMaxAge(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Unix(0, 0))
	st := NewSessionStore()
	st.clk = fake

	abandoned := makeKey("ccu1", "000ABCDE:1")
	st.Put(abandoned, NewSession(nil, nil))
	if st.Len() != 1 {
		t.Fatalf("Len()=%d after first Put, want 1", st.Len())
	}

	// Just under the TTL: the abandoned session must survive an unrelated
	// Put.
	fake.Advance(sessionMaxAge - time.Second)
	other := makeKey("ccu1", "000ABCDE:2")
	st.Put(other, NewSession(nil, nil))
	if st.Get(abandoned) == nil {
		t.Fatal("abandoned session swept before reaching sessionMaxAge")
	}

	// Now just past the TTL for `abandoned` (age = TTL+1s) but well under
	// it for `other` (age = 2s): the next Put must sweep only the former,
	// even though nothing ever called Delete for it.
	fake.Advance(2 * time.Second)
	yetAnother := makeKey("ccu1", "000ABCDE:3")
	st.Put(yetAnother, NewSession(nil, nil))

	if st.Get(abandoned) != nil {
		t.Error("abandoned session survived past sessionMaxAge — the leak this guard exists to catch")
	}
	if st.Get(other) == nil {
		t.Error("session younger than sessionMaxAge was swept along with the abandoned one")
	}
	if st.Get(yetAnother) == nil {
		t.Error("the just-opened session that triggered the sweep must itself survive it")
	}
}
