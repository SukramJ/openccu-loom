// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"net/http"
	"testing"
)

// TestDepsAssertAuthWiredNilAuthRequire verifies that a zero-value Deps
// (no AuthRequire middleware wired) reports an error. The composition root
// calls AssertAuthWired before serving so a mis-wired production build
// fails fast instead of silently exposing protected routes to anonymous
// callers.
func TestDepsAssertAuthWiredNilAuthRequire(t *testing.T) {
	d := Deps{}
	if err := d.AssertAuthWired(); err == nil {
		t.Fatal("AssertAuthWired: want non-nil error when AuthRequire is nil, got nil")
	}
}

// TestDepsAssertAuthWiredSet verifies that AssertAuthWired returns nil once
// AuthRequire is wired to any middleware, regardless of what the middleware
// itself does.
func TestDepsAssertAuthWiredSet(t *testing.T) {
	d := Deps{AuthRequire: func(h http.Handler) http.Handler { return h }}
	if err := d.AssertAuthWired(); err != nil {
		t.Fatalf("AssertAuthWired: want nil error when AuthRequire is set, got %v", err)
	}
}
