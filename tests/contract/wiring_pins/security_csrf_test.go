// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestCSRFMiddlewareMountedInRouter pins that NewRouter calls
// auth.CSRFMiddleware when Deps.CSRFEnabled is true. Without this wiring,
// mutating REST endpoints would be CSRF-vulnerable even with the config flag
// enabled.
func TestCSRFMiddlewareMountedInRouter(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/north/rest/router.go",
		"internal/auth",
		"CSRFMiddleware",
	)
}
