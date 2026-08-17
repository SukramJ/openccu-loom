// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package codes

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

// memoCapacity bounds how many resolved codes one scope remembers. The
// map is reset rather than evicted per entry: the cost of a reset is one
// re-derivation of a code that is still in use, and the alternative — an
// LRU in the authentication path — buys nothing at this size.
const memoCapacity = 256

// verifyMemo remembers which enabled code a supplied code matched, for
// one exact candidate set.
//
// Verifying a code means deriving an argon2id key per applicable enabled
// code — deliberately expensive, hundreds of milliseconds each — and a
// code that matches nothing has to be checked against every one of them.
// A household with five codes therefore burned seconds of CPU on a single
// mistyped PIN, and burned them again on the retry.
//
// The memo turns that into one lookup plus at most one derivation. A code
// resolved before against the same candidate set narrows the sweep to the
// row it matched — which is then verified for real, so an accepted code
// is always the result of a hash verification, never of the memo alone —
// or resolves to "matches nothing", which denies without deriving
// anything.
//
// Two properties keep it from becoming a way in. The lookup key is an
// HMAC of the code under a random per-process key, so no PIN is held in a
// form that reads back or survives the process. And every entry is scoped
// by a fingerprint of the candidate set itself, so disabling a code,
// changing a PIN, or a validity window closing yields a different scope
// under which every earlier entry is unreachable: a revoked code can
// never be accepted from the memo.
type verifyMemo struct {
	// key is nil when the process could not obtain randomness. The memo
	// then degrades to a no-op and every attempt runs the full sweep.
	key []byte

	mu    sync.Mutex
	scope string
	rows  map[string]string // hmac(code) → matched row id ("" = no match)
}

// newVerifyMemo returns a memo keyed by fresh process-local randomness.
func newVerifyMemo() *verifyMemo {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return &verifyMemo{}
	}
	return &verifyMemo{key: key, rows: map[string]string{}}
}

// lookup reports the row id code resolved to under scope. known is false
// when this code has not been resolved under this scope.
func (m *verifyMemo) lookup(scope, code string) (rowID string, known bool) {
	if m == nil || m.key == nil {
		return "", false
	}
	k := m.codeKey(code)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scope != scope {
		return "", false
	}
	rowID, known = m.rows[k]
	return rowID, known
}

// remember records the row id code resolved to under scope, dropping
// everything remembered for a different candidate set.
func (m *verifyMemo) remember(scope, code, rowID string) {
	if m == nil || m.key == nil {
		return
	}
	k := m.codeKey(code)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scope != scope || m.rows == nil || len(m.rows) >= memoCapacity {
		m.scope = scope
		m.rows = map[string]string{}
	}
	m.rows[k] = rowID
}

// codeKey derives the lookup key of one code.
func (m *verifyMemo) codeKey(code string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(code))
	return string(mac.Sum(nil))
}

// candidateScope fingerprints a candidate set: its row ids and stored
// hashes in order. Any change to which codes apply, to their secrets, or
// to their order produces a different scope, which is what makes a stale
// memo entry unreachable rather than merely unlikely.
func candidateScope(candidates []pinCandidate) string {
	h := sha256.New()
	var length [8]byte
	write := func(s string) {
		binary.BigEndian.PutUint64(length[:], uint64(len(s)))
		h.Write(length[:])
		h.Write([]byte(s))
	}
	for i := range candidates {
		write(candidates[i].id)
		write(candidates[i].hash)
	}
	return string(h.Sum(nil))
}
