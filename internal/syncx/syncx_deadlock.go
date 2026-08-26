// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build deadlock

package syncx

import (
	"time"

	"github.com/sasha-s/go-deadlock"
)

// Mutex is go-deadlock's lock-order-checking mutex under the `deadlock`
// build tag.
type Mutex = deadlock.Mutex

// RWMutex is go-deadlock's lock-order-checking RWMutex under the
// `deadlock` build tag.
type RWMutex = deadlock.RWMutex

func init() {
	// A lock held longer than this is reported as a potential deadlock.
	// Generous enough that slow-but-legitimate critical sections (SQLite
	// writes, CCU round-trips behind a lock) do not produce false reports,
	// while a genuinely stuck lock still surfaces within the run.
	deadlock.Opts.DeadlockTimeout = 60 * time.Second
}
