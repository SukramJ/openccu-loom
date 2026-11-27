// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/deployment"
)

func TestNodeAllowedHostsFromPublicURLAndMDNSName(t *testing.T) {
	t.Parallel()
	host := func(name string) func() (string, error) {
		return func() (string, error) { return name, nil }
	}
	cases := []struct {
		name      string
		publicURL string
		hostname  func() (string, error)
		want      []string
	}{
		{"public URL host without port", "https://Loom.Example.test:8443/ui", host("pi"), []string{"loom.example.test", "pi.local"}},
		{"an IP public URL is admitted by the module already", "http://192.168.1.5:8080", host("pi"), []string{"pi.local"}},
		{"a .local hostname is not doubled", "", host("pi.local"), []string{"pi.local"}},
		{"the same name twice is listed once", "http://pi.local", host("PI"), []string{"pi.local"}},
		{"a name the module would refuse is left out", "", host("my_box"), nil},
		{"no hostname", "", func() (string, error) { return "", errors.New("none") }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.North.REST.PublicURL = tc.publicURL
			if got := nodeAllowedHosts(cfg, tc.hostname); !slices.Equal(got, tc.want) {
				t.Fatalf("nodeAllowedHosts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNodeSupervisorAllowHostAdmitsOnlyIngress(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	if nodeSupervisorAllowHost(cfg, deployment.Standalone) != nil {
		t.Fatal("outside the HA add-on the node routes admit by name only")
	}
	allow := nodeSupervisorAllowHost(cfg, deployment.HAAddon)
	if allow == nil {
		t.Fatal("the HA add-on admits the Supervisor's Ingress requests")
	}
	req := func(peer, ingressPath string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/node/status", http.NoBody)
		r.Host = "homeassistant.local:8123"
		r.RemoteAddr = peer
		if ingressPath != "" {
			r.Header.Set("X-Ingress-Path", ingressPath)
		}
		return r
	}
	if !allow(req("172.30.32.2:41234", "/api/hassio_ingress/tok")) {
		t.Error("an Ingress request from the Supervisor is refused")
	}
	if allow(req("172.30.32.2:41234", "")) {
		t.Error("a request without X-Ingress-Path is admitted")
	}
	if allow(req("192.168.1.20:41234", "/api/hassio_ingress/tok")) {
		t.Error("a request from outside the Supervisor's network is admitted on a forged X-Ingress-Path")
	}
}

// TestNodeAllowHostAdmitsTokenAuthenticatedRequests pins which identities
// the node routes admit under a name they do not list: an API token or a
// box API token in every deployment (a reverse proxy such as the HA remote
// add-on injects one and forwards the browser's Host), never a credential a
// browser attaches on its own, and in the HA add-on still the Supervisor's
// Ingress requests.
func TestNodeAllowHostAdmitsTokenAuthenticatedRequests(t *testing.T) {
	t.Parallel()
	req := func(scheme auth.Scheme) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/node/status", http.NoBody)
		r.Host = "homeassistant.local:8123"
		r.RemoteAddr = "192.168.1.20:41234"
		if scheme != "" {
			r = r.WithContext(auth.ContextWithIdentity(r.Context(), auth.Identity{Subject: "x", Scheme: scheme}))
		}
		return r
	}
	cfg := config.Default()
	kinds := []deployment.Kind{deployment.Standalone, deployment.CCUAddon, deployment.LiteAddon, deployment.HAAddon}
	for _, kind := range kinds {
		allow := nodeAllowHost(cfg, kind)
		if allow == nil {
			t.Fatalf("%s: no predicate", kind)
		}
		for _, scheme := range []auth.Scheme{auth.SchemeBearer, auth.SchemeOcculiteToken} {
			if !allow(req(scheme)) {
				t.Errorf("%s: a %s request under an unlisted name is refused", kind, scheme)
			}
		}
		refused := []auth.Scheme{"", auth.SchemeSession, auth.SchemeBasic, auth.SchemeOIDC, auth.SchemeIngress, auth.SchemeOcculite}
		for _, scheme := range refused {
			if allow(req(scheme)) {
				t.Errorf("%s: a %q request under an unlisted name is admitted", kind, scheme)
			}
		}
	}

	ingress := httptest.NewRequest(http.MethodGet, "/api/v1/node/status", http.NoBody)
	ingress.Host = "homeassistant.local:8123"
	ingress.RemoteAddr = "172.30.32.2:41234"
	ingress.Header.Set("X-Ingress-Path", "/api/hassio_ingress/tok")
	if !nodeAllowHost(cfg, deployment.HAAddon)(ingress) {
		t.Error("the HA add-on no longer admits the Supervisor's Ingress requests")
	}
	if nodeAllowHost(cfg, deployment.Standalone)(ingress) {
		t.Error("outside the HA add-on an Ingress-looking request without a token is admitted")
	}
}
