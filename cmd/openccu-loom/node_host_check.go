// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/deployment"
)

// dnsHostName is a host name go-fabric-ui accepts in its AllowedHosts:
// labels of letters, digits and hyphens joined by dots, nothing else. The
// module refuses a scheme, port, path, wildcard or white space at mount,
// which would take the node routes down with it.
var dnsHostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// nodeAllowedHosts are the DNS names the node routes answer under besides
// loopback names and IP addresses, which go-fabric-ui admits on its own:
// the host of north.rest.public_url, the name the operator states the
// daemon is reached under, and <hostname>.local, the name the host's mDNS
// answers for. Every other name is refused there (403 host_not_allowed).
// hostname is os.Hostname in production.
func nodeAllowedHosts(cfg *config.Config, hostname func() (string, error)) []string {
	var out []string
	add := func(name string) {
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if name == "" || net.ParseIP(name) != nil || !dnsHostName.MatchString(name) {
			return
		}
		for _, have := range out {
			if have == name {
				return
			}
		}
		out = append(out, name)
	}
	if cfg != nil && cfg.North.REST.PublicURL != "" {
		if u, err := url.Parse(cfg.North.REST.PublicURL); err == nil {
			add(u.Hostname())
		}
	}
	if hostname != nil {
		if h, err := hostname(); err == nil {
			if h = strings.TrimSuffix(strings.TrimSuffix(h, "."), ".local"); h != "" {
				add(h + ".local")
			}
		}
	}
	return out
}

// nodeAllowHost is the predicate go-fabric-ui consults for a request under a
// name nodeAllowedHosts does not list. It admits a request an API token
// authenticated (nodeTokenAuthenticated) in every deployment, and in the HA
// add-on also a request the Supervisor's Ingress proxied
// (nodeSupervisorAllowHost).
func nodeAllowHost(cfg *config.Config, kind deployment.Kind) func(*http.Request) bool {
	supervisor := nodeSupervisorAllowHost(cfg, kind)
	if supervisor == nil {
		return nodeTokenAuthenticated
	}
	return func(r *http.Request) bool {
		return nodeTokenAuthenticated(r) || supervisor(r)
	}
}

// nodeTokenAuthenticated reports whether an API token (`bearer`) or a box
// API token (`occulite-token`) authenticated r. A reverse proxy that
// forwards the browser's own Host holds such a token: the Home Assistant
// remote add-on injects its token and hands on the name Home Assistant is
// reached under in X-Forwarded-Host, which no loom setting names. Without
// this every Matter page behind it answers 403 host_not_allowed, and the
// SPA reads the missing node status as a bridge that is switched off.
//
// The Host check defends against DNS rebinding: a foreign page that rebinds
// its own name to the daemon's address and drives the visitor's browser.
// That page cannot present a token — a browser attaches none on its own —
// so admitting a token-authenticated request keeps the defence intact. A
// session cookie, a Basic header the browser cached or an Ingress identity
// is still held to the name rules. The identity is resolved before the node
// routes run (the router's global auth resolver), and a token wins over a
// session cookie riding along with it ([auth.SessionMiddleware]).
func nodeTokenAuthenticated(r *http.Request) bool {
	id, ok := auth.IdentityFrom(r.Context())
	return ok && (id.Scheme == auth.SchemeBearer || id.Scheme == auth.SchemeOcculiteToken)
}

// nodeSupervisorAllowHost admits a node request Home Assistant's Ingress
// proxied when the daemon runs as the HA add-on. Ingress hands on the
// browser's own Host — the name Home Assistant is reached under, which no
// loom setting names — so without this every Matter page behind Ingress
// would answer 403. The decision rests on the request's real TCP peer and
// the Supervisor's X-Ingress-Path ([auth.SupervisorProxied]), never on the
// Host header, and it holds whether or not the Ingress login passthrough is
// on. Outside the add-on it is nil: names only.
func nodeSupervisorAllowHost(cfg *config.Config, kind deployment.Kind) func(*http.Request) bool {
	if kind != deployment.HAAddon {
		return nil
	}
	cidrStr := ""
	if cfg != nil {
		cidrStr = cfg.North.REST.Auth.HAIngress.TrustedProxyCIDR
	}
	if cidrStr == "" {
		cidrStr = defaultSupervisorCIDR
	}
	_, cidr, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return nil
	}
	return func(r *http.Request) bool { return auth.SupervisorProxied(cidr, r) }
}
