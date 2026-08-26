// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// leak_test.go installs goroutine-leak detection for the coordinators
// package. Coordinators drive periodic work and event-bus subscriptions
// that run on their own goroutines; goleak fails the package test run
// when any of them outlive the tests that created them.

package coordinators

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
