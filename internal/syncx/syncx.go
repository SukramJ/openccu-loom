// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build !deadlock

// Package syncx provides drop-in mutex types that compile to the standard
// library's sync.Mutex / sync.RWMutex by default, and to go-deadlock's
// lock-order-checking equivalents under the `deadlock` build tag.
//
// In the default build these are plain type aliases, so they carry zero
// runtime cost and behave identically to sync types (zero value usable,
// must not be copied after first use). Build with `-tags deadlock`
// (see `make deadlock-test`) to swap in runtime lock-order and stuck-lock
// detection for the packages that use these types — the test suite then
// aborts with a goroutine dump if a lock-order cycle or a lock held past
// the configured timeout is observed.
//
// Migration is incremental: a package opts in by declaring its mutex
// fields as syncx.Mutex / syncx.RWMutex instead of sync.Mutex /
// sync.RWMutex. Only opted-in locks participate in the cross-package lock
// graph under the deadlock tag.
package syncx

import "sync"

// Mutex is sync.Mutex in the default build.
type Mutex = sync.Mutex

// RWMutex is sync.RWMutex in the default build.
type RWMutex = sync.RWMutex
