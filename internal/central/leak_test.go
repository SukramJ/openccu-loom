// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// leak_test.go installs goroutine-leak detection for the central package.
// CentralUnit, the callback servers, and the scheduler spawn background
// goroutines; goleak fails the package test run when any of them outlive
// the tests that created them.

package central

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
