// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mrp

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync/atomic"
)

// ErrCounterExhausted is returned by [Counter.NextNoRollover] when the
// 32-bit space is used up. A secure session whose counter is exhausted
// must be replaced, never wrapped — reusing a counter value under the
// same key reuses an AES-CCM nonce. matter.js expires the session
// before the rollover (NodeSession.ts:111 onRollover → initiateClose)
// and throws when a forbidden rollover is attempted
// (MessageCounter.ts:64-67).
var ErrCounterExhausted = errors.New("mrp: message counter exhausted")

// Counter is a Matter MRP message counter — a 32-bit monotonic value.
// Per Core Spec §4.5.4 the counter is initialised to a random value at
// session creation so a peer cannot trivially infer how many messages
// a node has sent.
//
// The zero value is *not* usable — use [NewCounter] or
// [NewCounterFromSeed].
type Counter struct {
	v atomic.Uint32
}

// NewCounter returns a counter seeded from crypto/rand. The seed is a
// 28-bit random plus 1 (range [1..2^28]) — matter.js
// MessageCounter.ts:60 `(crypto.randomUint32 >>> 4) + 1` — so a fresh
// counter never starts near the 32-bit ceiling; a full-width random
// seed could begin within a handful of messages of exhaustion.
func NewCounter() (*Counter, error) {
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	c := &Counter{}
	c.v.Store((binary.LittleEndian.Uint32(seed[:]) >> 4) + 1)
	return c, nil
}

// NewCounterFromSeed returns a counter primed with `seed`. Used in
// tests so the counter trajectory is deterministic.
func NewCounterFromSeed(seed uint32) *Counter {
	c := &Counter{}
	c.v.Store(seed)
	return c
}

// Next returns the current value and advances by 1. Wrap-around is
// well-defined for unsigned 32-bit overflow. Only the UNENCRYPTED
// (session-0) counter may use this — matter.js allows rollover solely
// for the global unencrypted counter; secure sessions use
// [Counter.NextNoRollover].
func (c *Counter) Next() uint32 {
	// atomic.Add returns the *new* value, so subtract 1 to retrieve
	// the value that should appear on the wire.
	return c.v.Add(1) - 1
}

// NextNoRollover returns the current value and advances by 1, refusing
// to wrap: once the counter has reached 0xFFFFFFFF every further call
// returns [ErrCounterExhausted]. Secure sessions MUST use this variant
// — a wrapped counter reuses an AES-CCM nonce under the same session
// key (Matter §4.6.6 forbids rollover for encrypted sessions).
// Mirrors matter.js NodeSession.ts:111 (secure-session counter closes
// the session instead of rolling over) + MessageCounter.ts:64-67.
func (c *Counter) NextNoRollover() (uint32, error) {
	for {
		cur := c.v.Load()
		if cur == 0xFFFFFFFF {
			return 0, ErrCounterExhausted
		}
		if c.v.CompareAndSwap(cur, cur+1) {
			return cur, nil
		}
	}
}

// Peek returns the next-issued value without advancing.
func (c *Counter) Peek() uint32 { return c.v.Load() }
