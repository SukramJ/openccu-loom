// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/ui/surface"
)

// embeddedPolicy builds a live policy for the embedded profile with the
// given overrides.
func embeddedPolicy(overrides map[string]config.SurfaceState) *surface.Policy {
	on := true
	return surface.NewPolicy(config.NorthUI{
		Embedded: &on,
		Profiles: map[string]map[string]config.SurfaceState{config.ProfileEmbedded: overrides},
	}, nil)
}

// serve runs one request through the middleware and reports the status
// plus whether the wrapped handler ran.
func serve(t *testing.T, policy middleware.SurfacePolicy, id *auth.Identity, method, path string) (int, bool) {
	t.Helper()
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if id != nil {
		req = req.WithContext(auth.ContextWithIdentity(req.Context(), *id))
	}
	rr := httptest.NewRecorder()
	middleware.SurfaceWrites(policy, "/api/v1")(next).ServeHTTP(rr, req)
	return rr.Code, reached
}

var (
	ingress = auth.Identity{Subject: "ha-ingress", Scheme: auth.SchemeIngress, Role: auth.RoleAdmin}
	bearer  = auth.Identity{Subject: "svc", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin}
	session = auth.Identity{Subject: "markus", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}
)

// TestSurfaceWritesRefusesHiddenSurfaceForIngress is the boundary
// itself: in the embedded profile a hidden surface refuses the writes
// Home Assistant makes through the Ingress passthrough.
func TestSurfaceWritesRefusesHiddenSurfaceForIngress(t *testing.T) {
	t.Parallel()

	policy := embeddedPolicy(nil) // shipped embedded defaults: Configure hidden
	code, reached := serve(t, policy, &ingress, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER")
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", code)
	}
	if reached {
		t.Error("the handler ran despite the refusal")
	}
}

// TestSurfaceWritesScopesOnlyTheIngressIdentity is the containment that
// keeps this from becoming an authorization back door. A navigation
// switch must never change what a token or a Loom account may do.
func TestSurfaceWritesScopesOnlyTheIngressIdentity(t *testing.T) {
	t.Parallel()

	policy := embeddedPolicy(nil)
	for _, tc := range []struct {
		name string
		id   *auth.Identity
	}{
		{"bearer token", &bearer},
		{"browser session", &session},
		{"no identity at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, reached := serve(t, policy, tc.id, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER")
			if code != http.StatusOK || !reached {
				t.Errorf("status = %d, reached = %v — want the request to pass untouched", code, reached)
			}
		})
	}
}

// TestSurfaceWritesFollowsTheProfile pins the decision this feature
// rests on: showing the surface hands the write back.
func TestSurfaceWritesFollowsTheProfile(t *testing.T) {
	t.Parallel()

	policy := embeddedPolicy(map[string]config.SurfaceState{
		"device.configure":               config.SurfaceVisible,
		"device.configure.device-config": config.SurfaceVisible,
	})
	code, reached := serve(t, policy, &ingress, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER")
	if code != http.StatusOK || !reached {
		t.Errorf("status = %d, reached = %v — want the write allowed once the surface is shown", code, reached)
	}
}

// TestSurfaceWritesLeavesStandaloneAlone pins that the profile is purely
// navigational outside embedded mode.
func TestSurfaceWritesLeavesStandaloneAlone(t *testing.T) {
	t.Parallel()

	policy := surface.NewPolicy(config.NorthUI{
		Profiles: map[string]map[string]config.SurfaceState{
			config.ProfileStandalone: {"device.configure": config.SurfaceHidden},
		},
	}, nil)
	code, reached := serve(t, policy, &ingress, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER")
	if code != http.StatusOK || !reached {
		t.Errorf("status = %d, reached = %v — standalone must not refuse anything", code, reached)
	}
}

// TestSurfaceWritesNeverGatesReads pins that only writes are scoped —
// the HA panel reads the devices it no longer edits.
func TestSurfaceWritesNeverGatesReads(t *testing.T) {
	t.Parallel()

	policy := embeddedPolicy(nil)
	code, reached := serve(t, policy, &ingress, http.MethodGet, "/api/v1/devices/ABC/paramsets/MASTER")
	if code != http.StatusOK || !reached {
		t.Errorf("status = %d, reached = %v — reads must pass", code, reached)
	}
}

// TestSurfaceWritesLeavesUngatedEndpointsAlone pins the scope of the
// route table: an endpoint no hidden surface owns is untouched even for
// the Ingress identity, so embedded mode does not quietly become
// read-only for everything.
func TestSurfaceWritesLeavesUngatedEndpointsAlone(t *testing.T) {
	t.Parallel()

	policy := embeddedPolicy(nil)
	for _, path := range []string{
		"/api/v1/devices/ABC/channels/1/data-points/STATE/value", // device control
		"/api/v1/sysvars/foo",                // system variables
		"/api/v1/config/sections/north.mqtt", // a section embedded keeps editable
	} {
		code, reached := serve(t, policy, &ingress, http.MethodPut, path)
		if code != http.StatusOK || !reached {
			t.Errorf("PUT %s: status = %d, reached = %v — want it to pass", path, code, reached)
		}
	}
}

// TestSurfaceWritesNilPolicyIsInert keeps a router without the wiring
// serving normally rather than refusing everything.
func TestSurfaceWritesNilPolicyIsInert(t *testing.T) {
	t.Parallel()

	code, reached := serve(t, nil, &ingress, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER")
	if code != http.StatusOK || !reached {
		t.Errorf("status = %d, reached = %v — an unwired policy must not gate", code, reached)
	}
}

// TestSurfacePolicyUpdateTakesEffectImmediately pins the property that
// makes the editor honest: a saved profile moves the boundary in the
// running daemon, not at the next restart.
func TestSurfacePolicyUpdateTakesEffectImmediately(t *testing.T) {
	t.Parallel()

	policy := embeddedPolicy(nil)
	if code, _ := serve(t, policy, &ingress, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER"); code != http.StatusForbidden {
		t.Fatalf("precondition: status = %d, want 403", code)
	}

	on := true
	policy.Set(config.NorthUI{
		Embedded: &on,
		Profiles: map[string]map[string]config.SurfaceState{
			config.ProfileEmbedded: {
				"device.configure":               config.SurfaceVisible,
				"device.configure.device-config": config.SurfaceVisible,
			},
		},
	})
	if code, reached := serve(t, policy, &ingress, http.MethodPut, "/api/v1/devices/ABC/paramsets/MASTER"); code != http.StatusOK || !reached {
		t.Errorf("after the save: status = %d, reached = %v — want the new profile in force", code, reached)
	}
}
