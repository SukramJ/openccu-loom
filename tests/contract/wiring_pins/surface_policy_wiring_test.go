// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// The surface profile has two consumers that a test constructing them
// itself would never notice were unwired.
//
// If the composition root never builds a policy, the write middleware is
// never mounted: every unit test around the resolver still passes, the
// editor still saves, and the embedded profile silently refuses nothing
// — a security-relevant setting that reads as configured and does
// nothing. And if the write handler never pushes the saved config into
// the live policy, the boundary only moves at the next restart while the
// UI reports success.

// TestPin_SurfacePolicy_ConstructedInDaemon pins that the daemon builds
// the live policy at all.
func TestPin_SurfacePolicy_ConstructedInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom/daemon_rest_mount.go",
		"surface", "NewPolicy")
}

// TestPin_SurfacePolicy_HandedToTheRouter pins that the constructed
// policy actually reaches rest.Deps — a policy nobody passes on gates
// nothing.
func TestPin_SurfacePolicy_HandedToTheRouter(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom/daemon_rest_mount.go",
		"rest.Deps", "SurfacePolicy")
}

// TestPin_SurfaceWrites_MountedOnProtectedRoutes pins that the router
// mounts the middleware. The route table alone proves nothing: the
// endpoints exist either way, and only this call makes the profile
// decide anything about them.
func TestPin_SurfaceWrites_MountedOnProtectedRoutes(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/north/rest/router.go",
		"middleware", "SurfaceWrites")
}

// TestPin_SurfacePolicy_RefreshedOnSave pins the second half: the write
// handler pushes the new configuration into the live policy, so a saved
// profile is in force for the next request rather than the next boot.
func TestPin_SurfacePolicy_RefreshedOnSave(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/north/rest/handlers/ui_surfaces.go",
		"policy", "Set")
}
