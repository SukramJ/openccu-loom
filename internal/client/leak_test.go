// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// leak_test.go installs goroutine-leak detection for the client package.
// Client components spawn long-lived goroutines (circuit breakers,
// ping/pong loops, retry/throttle workers); goleak fails the package
// test run when any of them outlive the tests that created them.

package client

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
