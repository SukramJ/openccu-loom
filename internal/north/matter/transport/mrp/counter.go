// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
)

// Counter is a Matter MRP message counter — a 32-bit monotonic value
// that wraps after 2^32 messages. Per Core Spec §4.5.4 the counter is
// initialised to a random value at session creation so a peer cannot
// trivially infer how many messages a node has sent.
//
// The zero value is *not* usable — use [NewCounter] or
// [NewCounterFromSeed].
type Counter struct {
	v atomic.Uint32
}

// NewCounter returns a counter seeded from crypto/rand.
func NewCounter() (*Counter, error) {
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	c := &Counter{}
	c.v.Store(binary.LittleEndian.Uint32(seed[:]))
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
// well-defined for unsigned 32-bit overflow.
func (c *Counter) Next() uint32 {
	// atomic.Add returns the *new* value, so subtract 1 to retrieve
	// the value that should appear on the wire.
	return c.v.Add(1) - 1
}

// Peek returns the next-issued value without advancing.
func (c *Counter) Peek() uint32 { return c.v.Load() }
