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
