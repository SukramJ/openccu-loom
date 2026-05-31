// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Extra coverage tests targeting the uncovered addr == "" branches in
// listener.go:New.

package udp

import (
	"testing"
)

// TestNew_DefaultAddr_IPv4 verifies that New binds successfully when
// LocalAddr is empty and PreferIPv4 is true (uses "0.0.0.0:5540").
func TestNew_DefaultAddr_IPv4(t *testing.T) {
	t.Parallel()
	l, err := New(Config{LocalAddr: "", PreferIPv4: true})
	if err != nil {
		t.Fatalf("New(PreferIPv4=true, default addr): %v", err)
	}
	defer func() { _ = l.Close() }()
	if l.LocalAddr() == nil {
		t.Error("LocalAddr() returned nil")
	}
}

// TestNew_DefaultAddr_IPv6 verifies that New binds successfully when
// LocalAddr is empty and PreferIPv4 is false (uses "[::]:5540").
func TestNew_DefaultAddr_IPv6(t *testing.T) {
	t.Parallel()
	l, err := New(Config{LocalAddr: "", PreferIPv4: false})
	if err != nil {
		t.Skipf("IPv6 not available on this host: %v", err)
	}
	defer func() { _ = l.Close() }()
	if l.LocalAddr() == nil {
		t.Error("LocalAddr() returned nil")
	}
}
