// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestCSRFDefaultEnabled pins that the config default has CSRFEnabled set to
// true. A nil pointer in NorthREST.CSRFEnabled must be treated as true by
// CSRFIsEnabled so that any deployment without an explicit config entry is
// protected by default.
func TestCSRFDefaultEnabled(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	if !cfg.North.REST.CSRFIsEnabled() {
		t.Error("config.Default(): CSRFIsEnabled() = false, want true — browser-facing deployments must be CSRF-protected by default")
	}
}

// TestCSRFExplicitFalseOptOut pins that setting CSRFEnabled to false in
// the config (opt-out path for API-token deployments) is honoured by
// CSRFIsEnabled.
func TestCSRFExplicitFalseOptOut(t *testing.T) {
	t.Parallel()
	f := false
	var cfg config.NorthREST
	cfg.CSRFEnabled = &f
	if cfg.CSRFIsEnabled() {
		t.Error("NorthREST.CSRFIsEnabled(): returned true after explicit false opt-out, want false")
	}
}

// TestWSHandlerOriginCheckWiredInDaemon pins that cmd/openccu-loom passes a
// non-nil origin list to ws.Handler via the wsAllowedOrigins helper. Without
// this wiring the WebSocket upgrade path has no Origin check even when CSRF
// is active, leaving a CSRF vector on the WebSocket surface.
//
// wsAllowedOrigins is not a method and not a member of package
// internal/north/rest/ws — it is an unexported helper defined and called
// within cmd/openccu-loom itself (daemon_north.go / daemon_infra.go), so the
// call carries no package qualifier. calleePackage is therefore "": the
// same shape MustFindCallerInFile uses for any unqualified same-package
// call.
func TestWSHandlerOriginCheckWiredInDaemon(t *testing.T) {
	t.Parallel()
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"",
		"wsAllowedOrigins",
	)
}

// TestWSHandlerAcceptsAllowedOrigins pins that ws.Handler takes an
// allowedOrigins parameter (the third argument). The pin verifies that the
// identifier "allowedOrigins" appears in the handler source file, which is only
// true when the parameter is present.
func TestWSHandlerAcceptsAllowedOrigins(t *testing.T) {
	t.Parallel()
	contract.MustFindCallerInFile(
		t,
		"internal/north/rest/ws/handler.go",
		"internal/north/rest/ws",
		"allowedOrigins",
	)
}
