// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package bootid exposes a process-lifetime 16-byte random value that
// can optionally be mixed into Matter UniqueID derivations. The dev /
// debug use-case is breaking out of Apple Home's HAP-service mapper
// cache after a heavy pair-iteration session has corrupted the iPad's
// HMHome state — rotating the salt at every daemon boot guarantees
// Apple sees an unambiguously new bridge each time.
//
// **Default: rotation disabled.** matter.js (`StorageService`-persisted
// UniqueID) and chip (`GenerateUniqueId()` persistent storage) both serve
// a stable UniqueID across restarts. Per-boot rotation breaks Apple Home's
// post-restart accessory recognition: every HmIP device looks like a fresh
// accessory after the daemon restarts and the user has to re-link them in
// the Home app. The previous "rotate by default" behaviour optimised the
// dev cycle at the cost of production UX.
//
// Tests that need rotation must call [EnableRotation] explicitly.
// Production builds that need it (e.g. a forced fresh-pair after Apple
// HMHome corruption) toggle it via the matter.dev_rotate_unique_ids
// config knob.
package bootid

import (
	cryptorand "crypto/rand"
	"sync"
)

var (
	once            sync.Once
	salt            [16]byte
	rotationEnabled bool
	rotationGuard   sync.Mutex
)

func ensure() {
	once.Do(func() {
		if _, err := cryptorand.Read(salt[:]); err != nil {
			// crypto/rand failure is catastrophic on macOS/Linux; fall
			// back to a fixed sentinel so we never panic, accept the
			// loss of cache-rotation in this unlikely path.
			for i := range salt {
				salt[i] = 0x42
			}
		}
	})
}

// Salt returns the process-lifetime salt when rotation has been
// enabled, otherwise [16]byte{} (i.e. a zeroed value that contributes
// nothing to the hash). Callers should treat an all-zero result as
// "no rotation in effect" and mix it in unconditionally — the
// resulting UniqueIDs are stable across daemon restarts when the salt
// is zeroed, matching matter.js + chip behaviour.
func Salt() [16]byte {
	rotationGuard.Lock()
	enabled := rotationEnabled
	rotationGuard.Unlock()
	if !enabled {
		return [16]byte{}
	}
	ensure()
	return salt
}

// EnableRotation flips on the per-boot salt mixing. Intended for tests
// that exercise the dev-mode rotation path and for a future config
// knob `matter.dev_rotate_unique_ids` that the daemon can flip in
// dev/debug builds.
func EnableRotation() {
	rotationGuard.Lock()
	rotationEnabled = true
	rotationGuard.Unlock()
}

// SetForTest pins the salt so callers get deterministic UniqueIDs and
// implicitly enables rotation. Test-only — callers in production must
// not invoke this.
func SetForTest(s [16]byte) {
	rotationGuard.Lock()
	rotationEnabled = true
	rotationGuard.Unlock()
	once.Do(func() {})
	salt = s
}
