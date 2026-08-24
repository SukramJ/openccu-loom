// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// routerDepsLeftNil records every rest.Deps field fullyWiredRouterDeps
// does not fill, with the reason its absence is harmless there.
//
// The classification is measured per field against internal/north/rest/
// router.go, not pattern-matched from the reason text — which is how the
// first version of this map went wrong. It grouped every field that was
// not nil-gated as an "optional service facade whose handler answers 503",
// and thirteen of them were nothing of the kind: two booleans gating
// middleware, two values NewRouter defaults, three plain parameters, and
// the two role gates that fall back to AuthRequire rather than
// disappearing. Several facades do not answer 503 either — a nil
// Capabilities detector silently shrinks the list, a nil DeviceIcons proxy
// answers 404, and handlers.Health does not nil-check its tracker at all.
//
// So the facade reason no longer claims a status code. It claims what can
// be shown for every one of them: the route mounts regardless, and what an
// absent facade means is the handler's decision, not the router's. A
// stronger claim would need sixty handlers read, and the first version
// shows what happens when it is asserted instead.
var routerDepsLeftNil = map[string]string{
	"AlarmCodes":                  "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"AuditRecorder":               "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"AuthRequire":                 "the router mounts this middleware only when the dep is non-nil, so a test that measures authorization has to set it or it measures a router with no auth at all",
	"AuthResolve":                 "the router mounts this middleware only when the dep is non-nil, so a test that measures authorization has to set it or it measures a router with no auth at all",
	"BackupUpload":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Bootstrap":                   "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"CCUHostActions":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"CCUPosition":                 "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"CCUReboot":                   "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"CORS":                        "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"CSRFEnabled":                 "a boolean that gates a middleware: false skips it, and there is no facade and no route to answer for",
	"CSRFSecure":                  "passed by value into a middleware or handler; it is a parameter, not a collaborator whose absence stops a feature",
	"Capabilities":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"CentralCounter":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"CentralMetrics":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"CentralName":                 "passed by value into a middleware or handler; it is a parameter, not a collaborator whose absence stops a feature",
	"ChannelFlags":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"ChannelFlagsOverlay":         "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"ConfigChanges":               "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"ConfigChannelMeta":           "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"ConfigExport":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"ConfigUIURL":                 "passed by value into a middleware or handler; it is a parameter, not a collaborator whose absence stops a feature",
	"DataPointVis":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"DefinitionExport":            "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"DeviceIcons":                 "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"EntityNames":                 "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"FirmwareDownload":            "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Groups":                      "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"GroupsWriter":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Health":                      "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"HealthExtras":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"HealthGauges":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Idempotent":                  "a boolean that gates a middleware: false skips it, and there is no facade and no route to answer for",
	"KnownCentrals":               "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Labels":                      "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Logger":                      "a value NewRouter substitutes a default for when it is unset, so nil is a configuration choice rather than a missing collaborator",
	"LoginRateLimit":              "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"MatterAuditRecorder":         "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterCandidateProvider":     "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterCommissioning":         "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterCommissioningCloser":   "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterCommissioningOpener":   "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterCompatibilityReporter": "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterDiagnosticEvents":      "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterEndpointInspector":     "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterEventPublisher":        "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterExposureStore":         "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterFabricPurger":          "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterFabricRevoker":         "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterFabricStore":           "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterMdnsReporter":          "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterSessionLister":         "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterStatusReader":          "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"MatterTopologyReassembler":   "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"OpenAPIValidator":            "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"PreUpdateBackup":             "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"RateLimit":                   "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"RequireAdmin":                "nil falls back to AuthRequire rather than removing the gate, so an unset value narrows a route's tier instead of opening it; a test measuring tiers must set it to tell the two apart",
	"RequireOperator":             "nil falls back to AuthRequire rather than removing the gate, so an unset value narrows a route's tier instead of opening it; a test measuring tiers must set it to tell the two apart",
	"RestartPending":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"SPAHandler":                  "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"SectionApplier":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"SessionRevoker":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"Setup":                       "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"StatusMetrics":               "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"SystemCCU":                   "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"TLSCert":                     "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"TokenPurger":                 "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"TokenSockets":                "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"VisibilityCandidateProvider": "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"VisibilityCentralLister":     "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"VisibilityRegistryLoader":    "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"VisibilityUnIgnoreStore":     "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"WiringManifest":              "an optional service facade: the route mounts regardless and the handler decides what an absent facade means, so nothing the guards walk changes shape",
	"WriteTimeout":                "a value NewRouter substitutes a default for when it is unset, so nil is a configuration choice rather than a missing collaborator",
}

// TestFullyWiredRouterDepsCoversEveryDep pins that the helper every
// router-level contract guard builds on either fills a dep or records why
// it does not.
//
// The name `fullyWiredRouterDeps` is a claim, and it is only two-thirds
// true: the helper fills 68 of rest.Deps' 140 fields. Every guard built on
// it is blind to whatever the rest govern, and that is not hypothetical —
// a first attempt at pinning the Basic-auth throttle was green in both
// directions because AuthResolve is nil here, so the middleware under test
// was never mounted in either arrangement.
//
// A nil dep is often correct: most routes mount unconditionally and their
// handler answers 503 when its service is absent, which is loud and
// documented. What must not happen is a *new* dep quietly joining that
// set. So the rule is not "fill everything" — it is "every unfilled dep is
// a decision somebody wrote down".
func TestFullyWiredRouterDepsCoversEveryDep(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	declared := routerDepsFields(t, filepath.Join(root, "internal", "north", "rest", "router.go"))
	filled := fullyWiredDepsFields(t, filepath.Join(root, "tests", "contract", "rest_router_openapi_walk_test.go"))

	if len(declared) == 0 || len(filled) == 0 {
		t.Fatal("parsed no fields — the guard is measuring nothing")
	}

	var gap []string
	for _, name := range declared {
		if filled[name] {
			continue
		}
		if _, known := routerDepsLeftNil[name]; !known {
			gap = append(gap, name)
		}
	}
	sort.Strings(gap)
	if len(gap) > 0 {
		t.Errorf("fullyWiredRouterDeps leaves %d dep(s) nil with no reason recorded:\n  %s\n\n"+
			"Either fill the field, so the guards built on this helper exercise what it governs, or "+
			"add it to routerDepsLeftNil with the reason its absence is harmless. A dep that quietly "+
			"joins the nil set takes every router guard's coverage of it away.",
			len(gap), strings.Join(gap, "\n  "))
	}

	// The other direction: an entry for a dep that no longer exists, or that
	// the helper now fills, is stale bookkeeping that hides the next gap
	// behind a name nobody checks.
	declaredSet := map[string]bool{}
	for _, n := range declared {
		declaredSet[n] = true
	}
	var stale []string
	for name := range routerDepsLeftNil {
		switch {
		case !declaredSet[name]:
			stale = append(stale, name+" (no such field on rest.Deps)")
		case filled[name]:
			stale = append(stale, name+" (the helper fills it now)")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("routerDepsLeftNil carries %d stale entr(ies):\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// routerDepsFields returns the exported field names of rest.Deps.
func routerDepsFields(t *testing.T, routerFile string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), routerFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", routerFile, err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name == nil || ts.Name.Name != "Deps" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if name.IsExported() {
					out = append(out, name.Name)
				}
			}
		}
		return false
	})
	return out
}

// fullyWiredDepsFields returns the rest.Deps fields the helper assigns a
// non-nil value to. A field assigned the literal nil counts as unfilled:
// writing it out documents intent but exercises nothing.
func fullyWiredDepsFields(t *testing.T, helperFile string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), helperFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", helperFile, err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "fullyWiredRouterDeps" {
			return true
		}
		ast.Inspect(fn, func(inner ast.Node) bool {
			cl, ok := inner.(*ast.CompositeLit)
			if !ok || exprTypeName(cl.Type) != "rest.Deps" {
				return true
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if id, isIdent := kv.Value.(*ast.Ident); isIdent && id.Name == "nil" {
					continue
				}
				out[key.Name] = true
			}
			return true
		})
		return false
	})
	return out
}
