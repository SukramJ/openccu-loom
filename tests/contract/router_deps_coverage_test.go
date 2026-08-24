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
// The classification is measured, not guessed: each field was checked
// against the router for whether it gates a mount (`d.X != nil`) or is
// merely read by a handler. Nine of the seventy-five gate something, and
// all nine gate middleware or the SPA mount rather than a documented API
// route, which is why the router/OpenAPI walk stays green with them
// absent. The other sixty-six sit behind routes that mount regardless and
// answer 503 when their facade is nil.
//
// The reasons are grouped because the groups are the honest granularity. A
// per-field essay for sixty-odd optional facades would have been invention
// dressed as reasoning.
var routerDepsLeftNil = map[string]string{
	"AlarmCodes":                  "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"AuditRecorder":               "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"AuthRequire":                 "the auth chain is mounted only when the dep is non-nil, so a test that measures authorization must set it or it measures a router with no auth at all",
	"AuthResolve":                 "the auth chain is mounted only when the dep is non-nil, so a test that measures authorization must set it or it measures a router with no auth at all",
	"BackupUpload":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Bootstrap":                   "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"CCUHostActions":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CCUPosition":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CCUReboot":                   "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CORS":                        "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"CSRFEnabled":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CSRFSecure":                  "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Capabilities":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CentralCounter":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CentralMetrics":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"CentralName":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"ChannelFlags":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"ChannelFlagsOverlay":         "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"ConfigChanges":               "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"ConfigChannelMeta":           "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"ConfigExport":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"ConfigUIURL":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"DataPointVis":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"DefinitionExport":            "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"DeviceIcons":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"EntityNames":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"FirmwareDownload":            "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Groups":                      "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"GroupsWriter":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Health":                      "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"HealthExtras":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"HealthGauges":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Idempotent":                  "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"KnownCentrals":               "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Labels":                      "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Logger":                      "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"LoginRateLimit":              "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"MatterAuditRecorder":         "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterCandidateProvider":     "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterCommissioning":         "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterCommissioningCloser":   "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterCommissioningOpener":   "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterCompatibilityReporter": "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterDiagnosticEvents":      "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterEndpointInspector":     "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterEventPublisher":        "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterExposureStore":         "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterFabricPurger":          "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterFabricRevoker":         "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterFabricStore":           "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterMdnsReporter":          "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterSessionLister":         "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterStatusReader":          "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"MatterTopologyReassembler":   "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"OpenAPIValidator":            "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"PreUpdateBackup":             "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"RateLimit":                   "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"RequireAdmin":                "the auth chain is mounted only when the dep is non-nil, so a test that measures authorization must set it or it measures a router with no auth at all",
	"RequireOperator":             "the auth chain is mounted only when the dep is non-nil, so a test that measures authorization must set it or it measures a router with no auth at all",
	"RestartPending":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"SPAHandler":                  "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"SectionApplier":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"SessionRevoker":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"Setup":                       "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"StatusMetrics":               "gates a middleware or the SPA mount rather than a documented API route, so its absence narrows the chain the guards walk without removing an operation",
	"SystemCCU":                   "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"TLSCert":                     "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"TokenPurger":                 "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"TokenSockets":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"VisibilityCandidateProvider": "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"VisibilityCentralLister":     "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"VisibilityRegistryLoader":    "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"VisibilityUnIgnoreStore":     "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"WiringManifest":              "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
	"WriteTimeout":                "optional service facade: the route mounts unconditionally and the handler answers 503 when it is absent, which is loud rather than silent",
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
