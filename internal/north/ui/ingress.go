// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"net/http"
	"strings"
)

// ingressPrefix returns the HA Ingress base path the Supervisor advertises in
// X-Ingress-Path, or "" when the request is not proxied through Ingress. The
// server-rendered bootstrap surface is reached through that prefix under HA
// Ingress, so every emitted URL (template links/forms via <base>, and every
// server-side redirect via uiRedirect) must carry it — an absolute "/foo"
// would otherwise resolve against the Home Assistant origin and escape the
// add-on iframe.
//
// It mirrors safeIngressPrefix in internal/north/rest: only a single-slash
// local path is honoured (a "//host" or "/\\" form is rejected so the value
// can never become an open redirect), trailing slash trimmed.
func ingressPrefix(r *http.Request) string {
	p := r.Header.Get("X-Ingress-Path")
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	if strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return ""
	}
	return strings.TrimRight(p, "/")
}

// uiRedirect issues a 303 See Other redirect to a daemon-local path, prefixing
// it with the Ingress base path when the request arrived through HA Ingress.
// path must be a local absolute path (e.g. "/login", "/setup?wzerr=admin").
func uiRedirect(w http.ResponseWriter, r *http.Request, path string) {
	//nolint:gosec // G710: path is a caller-supplied local path; ingressPrefix is a validated local prefix — no foreign origin.
	http.Redirect(w, r, ingressPrefix(r)+path, http.StatusSeeOther)
}
