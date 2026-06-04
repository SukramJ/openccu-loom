// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// leak_test.go installs goroutine-leak detection for the sqlite store
// package. The persistence layer and its driver maintain background
// goroutines; goleak fails the package test run when any of them outlive
// the tests that created them.

package sqlite

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
