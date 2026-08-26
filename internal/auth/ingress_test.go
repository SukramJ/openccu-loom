// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// parseCIDR is a test-local helper that panics on invalid input so we
// catch mis-spellings at test compile time rather than silently returning nil.
func parseCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("parseCIDR(%q): %v", cidr, err)
	}
	return ipNet
}

// buildIngress returns a handler chain: IngressPassthrough wrapping a terminal
// handler that records the presence / value of the injected identity. The
// terminal handler writes 200 and sets a response header so we can inspect the
// result without depending on the response body format.
func buildIngress(t *testing.T, trust IngressTrust) (http.Handler, *bool, *Identity) {
	t.Helper()
	var (
		gotIdentity bool
		gotID       Identity
	)
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := IdentityFrom(r.Context()); ok {
			gotIdentity = true
			gotID = id
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := IngressPassthrough(trust, nil)(terminal)
	return handler, &gotIdentity, &gotID
}

// TestIngressPassthrough_Happy verifies that a request with all preconditions
// satisfied injects a SchemeIngress identity with the configured role.
func TestIngressPassthrough_Happy(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}
	handler, gotIdentity, gotID := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !*gotIdentity {
		t.Fatal("expected identity injected, got none")
	}
	if gotID.Role != RoleAdmin {
		t.Errorf("Role=%q, want %q", gotID.Role, RoleAdmin)
	}
	if gotID.Scheme != SchemeIngress {
		t.Errorf("Scheme=%q, want %q", gotID.Scheme, SchemeIngress)
	}
	if gotID.Subject != "ha-ingress" {
		t.Errorf("Subject=%q, want %q", gotID.Subject, "ha-ingress")
	}
}

// TestIngressPassthrough_Disabled verifies that a disabled trust is a no-op.
func TestIngressPassthrough_Disabled(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     false, // disabled
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("disabled trust must not inject identity")
	}
}

// TestIngressPassthrough_NotSupervised verifies that unsupervised builds are a no-op.
func TestIngressPassthrough_NotSupervised(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  false, // not supervised
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("unsupervised trust must not inject identity")
	}
}

// TestIngressPassthrough_NilCIDR verifies that a nil CIDR is a no-op.
func TestIngressPassthrough_NilCIDR(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: nil, // nil → inactive
		Role:        RoleAdmin,
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("nil CIDR trust must not inject identity")
	}
}

// TestIngressPassthrough_EmptyRole verifies that an empty Role is a no-op.
func TestIngressPassthrough_EmptyRole(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        "", // empty → inactive
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("empty Role trust must not inject identity")
	}
}

// TestIngressPassthrough_RemoteAddrOutsideCIDR verifies that a request from
// outside the trusted subnet is not injected, even with the header present.
func TestIngressPassthrough_RemoteAddrOutsideCIDR(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "192.168.1.50:5000" // outside the CIDR
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("request from outside CIDR must not be injected")
	}
}

// TestIngressPassthrough_HeaderMissing verifies that a request without the
// X-Ingress-Path header is not injected, even when RemoteAddr is in the CIDR.
func TestIngressPassthrough_HeaderMissing(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	// No X-Ingress-Path header

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("missing X-Ingress-Path header must prevent injection")
	}
}

// TestIngressPassthrough_XFFSpoofIgnored verifies that a request with
// X-Forwarded-For faking a trusted IP but a real RemoteAddr outside the CIDR
// is not injected. The passthrough must consult only RemoteAddr.
func TestIngressPassthrough_XFFSpoofIgnored(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}
	handler, gotIdentity, _ := buildIngress(t, trust)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "192.168.99.1:5000"             // outside CIDR
	req.Header.Set("X-Forwarded-For", "172.30.32.2") // spoofed trusted IP
	req.Header.Set("X-Real-IP", "172.30.32.2")       // also spoofed
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if *gotIdentity {
		t.Fatal("X-Forwarded-For spoof must not bypass the CIDR check (RemoteAddr is authoritative)")
	}
}

// TestIngressPassthrough_ExistingIdentityWins verifies that when an identity is
// already present on the context before the passthrough runs, the passthrough
// does not overwrite it.
func TestIngressPassthrough_ExistingIdentityWins(t *testing.T) {
	t.Parallel()
	trust := IngressTrust{
		Enabled:     true,
		Supervised:  true,
		TrustedCIDR: parseCIDR(t, "172.30.32.0/23"),
		Role:        RoleAdmin,
	}

	// Seed an identity in the context before the middleware chain.
	priorID := Identity{Subject: "real", Scheme: SchemeBearer, Role: RoleViewer}

	var (
		gotIdentity bool
		gotID       Identity
	)
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := IdentityFrom(r.Context()); ok {
			gotIdentity = true
			gotID = id
		}
		w.WriteHeader(http.StatusOK)
	})
	// Outer handler seeds the identity; inner is the passthrough.
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := ContextWithIdentity(r.Context(), priorID)
		IngressPassthrough(trust, nil)(terminal).ServeHTTP(w, r.WithContext(ctx))
	})

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "172.30.32.2:5000"
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/x")

	rr := httptest.NewRecorder()
	outer.ServeHTTP(rr, req)

	if !gotIdentity {
		t.Fatal("expected prior identity to survive; got none")
	}
	if gotID.Subject != "real" {
		t.Errorf("Subject=%q, want %q (prior identity must not be overwritten)", gotID.Subject, "real")
	}
	if gotID.Role != RoleViewer {
		t.Errorf("Role=%q, want viewer (prior identity must not be elevated)", gotID.Role)
	}
}

// TestIngressSchemeCSRFExempt verifies that SchemeIngress is classified as
// CSRF-exempt (per-request proxy-trusted, not a browser-ambient cookie).
func TestIngressSchemeCSRFExempt(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	if !csrfExempt(r, SchemeIngress) {
		t.Error("SchemeIngress must be CSRF-exempt")
	}
}
