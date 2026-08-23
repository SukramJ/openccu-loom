// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest"
)

// Authorization tiers, ordered least to most privileged. The zero value is
// tierPublic: a route that no gate wraps at all.
const (
	tierPublic = iota
	tierViewer
	tierOperator
	tierAdmin
)

func tierName(t int) string {
	switch t {
	case tierViewer:
		return "viewer"
	case tierOperator:
		return "operator"
	case tierAdmin:
		return "admin"
	default:
		return "public"
	}
}

// authzScopeExemptions lists routes whose mounted tier deliberately differs
// from the scope the specification declares, with the reason. An entry here
// is a decision, not a deferral: it says the divergence was looked at and
// kept. Key is "METHOD /path" with the /api/v1 prefix stripped.
//
// What this test can and cannot see: it measures the three gates [rest.Deps]
// injects — AuthRequire, RequireOperator, RequireAdmin. A route that carries
// its own middleware constructed inside NewRouter reads as "public" here even
// when it is fully gated. Those routes are exempt, and each entry says which
// middleware actually guards it, so a future reader can check that the gate is
// still there rather than trusting the word "exempt".
var authzScopeExemptions = map[string]string{
	// The handler is the gate: /auth/me answers 401 when no session
	// resolves, which is exactly the probe the SPA issues on startup to
	// decide whether to render the login page. Mounting it behind
	// AuthRequire would make the probe indistinguishable from a network
	// failure.
	"GET /auth/me": "handler answers 401 itself; it is the SPA's pre-login session probe",
	// Logging out of an already-invalid session must succeed, not 401 —
	// otherwise a client holding a stale cookie can never clear it.
	"POST /auth/logout": "must work with an expired or absent session so a stale cookie can be cleared",
	// Gated by handlers.InboundWebhookAuth, mounted at the route rather
	// than injected through rest.Deps: it admits an operator identity from
	// the normal chain OR the configured inbound bearer token, compared in
	// constant time. The token path is the point — a doorbell posts a
	// header, not a session.
	"POST /webhook/value":   "gated by handlers.InboundWebhookAuth (operator identity or inbound bearer token)",
	"POST /webhook/program": "gated by handlers.InboundWebhookAuth (operator identity or inbound bearer token)",
}

// TestRESTRouteTiersMatchOpenAPIScopes pins the two halves of the
// authorization contract to each other: the scope assets/openapi.yaml
// publishes for an operation, and the gate the chi router actually wraps
// that route in.
//
// Why this guard exists: the two halves had drifted in both directions and
// nothing noticed. `GET /service-messages/suppressed` was published as
// operator-tier and mounted open to any authenticated caller, so the list of
// faults an operator had chosen to silence was readable by a viewer.
// `GET /devices/{addr}/icon` was published as authenticated and mounted
// ahead of the auth gate entirely, which made it an unauthenticated
// existence oracle for the whole device inventory — it answers differently
// for a known and an unknown address. Both were reachable in production and
// both were invisible to every existing test, because the router tests
// checked which routes exist and the spec tests checked what the document
// says, and no test compared the two.
//
// How the tier is measured: chi.Walk hands every route its middleware chain.
// The chain is composed around a sentinel handler of this test's own — the
// production handler never runs — and a synthetic request is served through
// it. The gates in [rest.Deps] are replaced by markers that record which one
// they are, so the observed tier is the one a real request would actually
// pass through, not a name matched out of the source text.
func TestRESTRouteTiersMatchOpenAPIScopes(t *testing.T) {
	spec := loadOpenAPISpec(t)

	// The marker records into observed, keyed by nothing: each route is
	// walked and served in isolation, so a single cell is enough.
	var observed int
	marker := func(tier int) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tier > observed {
					observed = tier
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	deps := fullyWiredRouterDeps()
	deps.AuthRequire = marker(tierViewer)
	deps.RequireOperator = marker(tierOperator)
	deps.RequireAdmin = marker(tierAdmin)
	router := rest.NewRouter(deps)

	type route struct {
		method string
		path   string
		tier   int
	}
	var routes []route

	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	err := chi.Walk(router, func(method, pattern string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/v1") {
			return nil
		}
		if method == "*" {
			// chi's catch-all registration (the /events upgrade route); its
			// GET twin is walked separately and carries the same chain.
			return nil
		}
		path := strings.TrimPrefix(pattern, "/api/v1")
		if path == "" {
			path = "/"
		}

		// Compose the route's real middleware chain around our own
		// terminal handler so no production handler is ever invoked.
		h := http.Handler(sentinel)
		for i := len(middlewares) - 1; i >= 0; i-- {
			h = middlewares[i](h)
		}

		observed = tierPublic
		req := httptest.NewRequest(method, "/api/v1"+fillPathParams(path), http.NoBody)
		h.ServeHTTP(httptest.NewRecorder(), req)
		routes = append(routes, route{method: method, path: path, tier: observed})
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("walked zero /api/v1 routes — the harness is measuring nothing")
	}

	var drift []string
	for _, rt := range routes {
		key := rt.method + " " + rt.path
		if _, exempt := authzScopeExemptions[key]; exempt {
			continue
		}
		item := spec.Paths.Find(rt.path)
		if item == nil {
			// Route-vs-spec coverage is TestRESTRouterMatchesOpenAPISpec's
			// job; not repeating its failures here keeps each guard's
			// message about one thing.
			continue
		}
		op := item.GetOperation(rt.method)
		if op == nil {
			continue
		}
		declared := declaredScope(spec, op)
		if declared != rt.tier {
			drift = append(drift, fmt.Sprintf(
				"%s: openapi.yaml declares %q, the router mounts it %q",
				key, tierName(declared), tierName(rt.tier),
			))
		}
	}

	if len(drift) > 0 {
		sort.Strings(drift)
		t.Errorf("published authorization scope and mounted gate disagree on %d route(s):\n  %s\n\n"+
			"Fix the side that is wrong. A route mounted below its published scope is a\n"+
			"privilege leak; one mounted above it breaks a client that followed the spec.\n"+
			"If the divergence is deliberate, record it in authzScopeExemptions with the reason.",
			len(drift), strings.Join(drift, "\n  "))
	}
}

// declaredScope reduces an operation's security requirements to the tier this
// test measures. An operation with its own `security` overrides the document
// default; an explicitly empty list means the operation is public.
func declaredScope(spec *openapi3.T, op *openapi3.Operation) int {
	reqs := spec.Security
	if op.Security != nil {
		reqs = *op.Security
	}
	if len(reqs) == 0 {
		return tierPublic
	}
	best := tierPublic
	for _, req := range reqs {
		for scheme, scopes := range req {
			if scheme != "openIdConnect" {
				// basicAuth / bearerAuth carry no scope list; they say
				// "authenticated", which is the viewer floor.
				if best < tierViewer {
					best = tierViewer
				}
				continue
			}
			for _, s := range scopes {
				switch s {
				case "admin":
					if best < tierAdmin {
						best = tierAdmin
					}
				case "operator":
					if best < tierOperator {
						best = tierOperator
					}
				case "viewer":
					if best < tierViewer {
						best = tierViewer
					}
				}
			}
		}
	}
	return best
}

// fillPathParams replaces chi/OpenAPI path templates with a literal so the
// synthetic request matches the route it was built from.
func fillPathParams(path string) string {
	var b strings.Builder
	depth := 0
	for _, r := range path {
		switch r {
		case '{':
			depth++
			if depth == 1 {
				b.WriteString("x")
			}
		case '}':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
