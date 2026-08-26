// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"log/slog"
	"net"
	"net/http"
)

// IngressTrust carries the resolved policy for [IngressPassthrough]. The
// composition root fills it from config + the build/supervised stamp so this
// package never imports config.
type IngressTrust struct {
	// Enabled is the operator opt-in (config north.rest.auth.ha_ingress.enabled).
	Enabled bool
	// Supervised is true only when the daemon runs as the supervised HA add-on
	// (build stamp / OPENCCU_LOOM_SUPERVISOR). The Supervisor-subnet trust
	// assumption only holds there, so the passthrough is inert otherwise even
	// when Enabled.
	Supervised bool
	// TrustedCIDR is the network the request's real TCP peer must fall within
	// (the HA Supervisor's Docker subnet). nil disables the passthrough.
	TrustedCIDR *net.IPNet
	// Role is the Loom role granted to a trusted Ingress request.
	Role Role
}

// active reports whether the passthrough can ever inject — all preconditions
// that do not depend on the request itself.
func (t IngressTrust) active() bool {
	return t.Enabled && t.Supervised && t.TrustedCIDR != nil && t.Role != ""
}

// IngressPassthrough is a fallback resolver for the HA Ingress auth passthrough
// (ADR 0044). It must be wired as the INNERMOST auth resolver (closest to the
// handler) so the normal credential resolvers run first: a genuine Bearer token
// or session always wins, and the passthrough only injects an identity when no
// credentials resolved.
//
// It injects [SchemeIngress] admin (or t.Role) ONLY when every condition holds:
//   - the policy is active (enabled + supervised + a trusted CIDR), and
//   - no identity is already on the context (real credentials win), and
//   - the request carries the Supervisor's X-Ingress-Path header, and
//   - the request's real TCP peer (RemoteAddr — never X-Forwarded-For) is
//     inside TrustedCIDR.
//
// The safety contract is that the add-on keeps `panel_admin: true`, so only HA
// admins reach Ingress (HA passes no user/role to the add-on). When the policy
// is active the caller should log that dependency once at startup.
func IngressPassthrough(t IngressTrust, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		if !t.active() {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ingressTrusted(t, r) {
				next.ServeHTTP(w, r)
				return
			}
			id := Identity{Subject: ingressSubject, Scheme: SchemeIngress, Role: t.Role}
			logger.DebugContext(r.Context(), "auth.ingress.passthrough",
				slog.String("role", string(t.Role)), slog.String("remote", r.RemoteAddr))
			next.ServeHTTP(w, r.WithContext(ContextWithIdentity(r.Context(), id)))
		})
	}
}

// ingressSubject is the audit subject for passthrough-authenticated requests,
// distinct from any real user so the audit log shows the request came via HA
// Ingress rather than a login.
const ingressSubject = "ha-ingress"

// ingressTrusted applies the per-request gate. Credentials already on the
// context win (returns false → pass through unchanged).
func ingressTrusted(t IngressTrust, r *http.Request) bool {
	if _, ok := IdentityFrom(r.Context()); ok {
		return false // a real Bearer/session/basic identity wins
	}
	if r.Header.Get("X-Ingress-Path") == "" {
		return false
	}
	ip := remotePeerIP(r) // real TCP peer only — X-Forwarded-For is ignored on purpose
	return ip != nil && t.TrustedCIDR.Contains(ip)
}

// remotePeerIP parses the request's real transport peer from RemoteAddr.
// It deliberately does NOT consult X-Forwarded-For / X-Real-IP, which a client
// on the directly-exposed port could spoof to forge a Supervisor origin.
func remotePeerIP(r *http.Request) net.IP {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(host)
}
