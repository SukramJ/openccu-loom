// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package channelflags holds the in-memory overlay of operator-set
// per-channel overrides (G12): hidden (drop the channel from operation
// lists / MQTT / Matter) and locked (block control writes). The SQLite
// [ChannelFlagsStore] is the durable source of truth; this overlay is a
// save-through read cache consulted on the ingest and control-write hot
// paths, so neither imports the model nor the store package (no cycle).
package channelflags

import "sync"

// Flags is the pair of operator overrides for one channel. The zero value
// (both false) means "no override" — a channel with default behaviour.
type Flags struct {
	Hidden bool
	Locked bool
}

// Set reports whether either flag is on.
func (f Flags) Set() bool { return f.Hidden || f.Locked }

// Overlay is a concurrency-safe (central, channel-address) → [Flags] map.
// A missing entry is the zero [Flags] (no override).
type Overlay struct {
	mu sync.RWMutex
	m  map[string]map[string]Flags // central -> channel address -> flags
}

// New returns an empty overlay.
func New() *Overlay {
	return &Overlay{m: make(map[string]map[string]Flags)}
}

// Get returns the flags for one channel, or the zero value when unset.
func (o *Overlay) Get(central, address string) Flags {
	if o == nil {
		return Flags{}
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.m[central][address]
}

// Set replaces the flags for one channel. Clearing both flags removes the
// entry so an unset channel never lingers in the map.
func (o *Overlay) Set(central, address string, f Flags) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !f.Set() {
		delete(o.m[central], address)
		if len(o.m[central]) == 0 {
			delete(o.m, central)
		}
		return
	}
	if o.m[central] == nil {
		o.m[central] = make(map[string]Flags)
	}
	o.m[central][address] = f
}

// Replace atomically swaps the full contents for one central. Used on the
// boot-time load and after a bulk refresh; entries for other centrals are
// left untouched.
func (o *Overlay) Replace(central string, flags map[string]Flags) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(flags) == 0 {
		delete(o.m, central)
		return
	}
	cp := make(map[string]Flags, len(flags))
	for a, f := range flags {
		if f.Set() {
			cp[a] = f
		}
	}
	o.m[central] = cp
}
