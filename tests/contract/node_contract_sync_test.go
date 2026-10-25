// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestNodeContractMatchesPinnedGoFabricUI holds the /node/* part of
// assets/openapi.yaml to the contract of the go-fabric-ui module go.mod
// pins (contract/openapi.yaml in the module), because the routes are
// served by that module's handler, not by code in this repository: the
// router-walk guards see one mounted catch-all (/node/*) and cannot tell a
// documented node operation from an undocumented one. This guard is what
// does: every path and method the module's contract declares under /node
// is documented here with the same operationId and the same response
// codes, nothing under /node is documented here that the module does not
// serve, and every schema the module's contract defines exists here under
// its name (Warning as NodeWarning, Problem as loom's own). A go-fabric-ui
// bump that adds a route or a field turns this red until the copy is
// refreshed; a hand edit of the copy turns it red at once.
func TestNodeContractMatchesPinnedGoFabricUI(t *testing.T) {
	t.Parallel()

	loom := loadOpenAPISpec(t)
	module := loadGoFabricUIContract(t)

	type op struct{ method, path string }
	ops := func(doc *openapi3.T, onlyNode bool) map[op]*openapi3.Operation {
		out := map[op]*openapi3.Operation{}
		for path, item := range doc.Paths.Map() {
			if !strings.HasPrefix(path, "/node/") {
				continue
			}
			for method, o := range item.Operations() {
				if onlyNode && o.Extensions["x-status"] != nil {
					continue
				}
				out[op{method, path}] = o
			}
		}
		return out
	}
	want := ops(module, true)
	got := ops(loom, false)
	var problems []string
	for k, wo := range want {
		lo, ok := got[k]
		if !ok {
			problems = append(problems, k.method+" "+k.path+": in go-fabric-ui's contract, not in assets/openapi.yaml")
			continue
		}
		if lo.OperationID != wo.OperationID {
			problems = append(problems, k.method+" "+k.path+": operationId "+lo.OperationID+", go-fabric-ui says "+wo.OperationID)
		}
		if lc, wc := responseCodes(lo), responseCodes(wo); lc != wc {
			problems = append(problems, k.method+" "+k.path+": responses "+lc+", go-fabric-ui says "+wc)
		}
		if (wo.Extensions["x-admin"] != nil) != (lo.Extensions["x-admin"] != nil) {
			problems = append(problems, k.method+" "+k.path+": x-admin differs from go-fabric-ui's contract")
		}
		if wo.Extensions["x-admin"] != nil && !declaresAdminScope(lo) {
			problems = append(problems, k.method+" "+k.path+": go-fabric-ui gates it as admin, assets/openapi.yaml declares no admin scope")
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			problems = append(problems, k.method+" "+k.path+": documented in assets/openapi.yaml, not served by go-fabric-ui's handler")
		}
	}

	for name := range module.Components.Schemas {
		local := name
		switch name {
		case "Warning":
			local = "NodeWarning"
		case "Problem":
			// loom's own Problem schema stands in for the module's.
		}
		if _, ok := loom.Components.Schemas[local]; !ok {
			problems = append(problems, "schema "+name+": in go-fabric-ui's contract, missing from assets/openapi.yaml as "+local)
			continue
		}
		if name == "Problem" {
			continue
		}
		lp, wp := propertyNames(loom.Components.Schemas[local]), propertyNames(module.Components.Schemas[name])
		if lp != wp {
			problems = append(problems, "schema "+local+": properties "+lp+", go-fabric-ui says "+wp)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("assets/openapi.yaml's /node contract drifted from go-fabric-ui %s (%d finding(s)):\n  %s\n\n"+
			"Refresh the copy from the module's contract/openapi.yaml (Warning -> NodeWarning, responses/Problem -> responses/NodeProblem, the admin scope block under x-admin).",
			module.Info.Version, len(problems), strings.Join(problems, "\n  "))
	}
}

// goFabricUIModuleDir is the directory of the go-fabric-ui module version
// go.mod pins, in the module cache: the contract and the npm package's
// sources live there beside the Go code.
func goFabricUIModuleDir(t *testing.T) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "go", "list", "-m", "-f", "{{.Dir}}", "github.com/SukramJ/go-fabric-ui")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m github.com/SukramJ/go-fabric-ui: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("go list reported no directory for github.com/SukramJ/go-fabric-ui")
	}
	return dir
}

// loadGoFabricUIContract reads contract/openapi.yaml of the go-fabric-ui
// module version go.mod pins, from the module cache.
func loadGoFabricUIContract(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(filepath.Join(goFabricUIModuleDir(t), "contract", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load go-fabric-ui contract: %v", err)
	}
	return doc
}

func responseCodes(o *openapi3.Operation) string {
	if o.Responses == nil {
		return ""
	}
	codes := make([]string, 0, o.Responses.Len())
	for code := range o.Responses.Map() {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return strings.Join(codes, ",")
}

func declaresAdminScope(o *openapi3.Operation) bool {
	if o.Security == nil {
		return false
	}
	for _, req := range *o.Security {
		for _, scopes := range req {
			for _, s := range scopes {
				if s == "admin" {
					return true
				}
			}
		}
	}
	return false
}

func propertyNames(ref *openapi3.SchemaRef) string {
	if ref == nil || ref.Value == nil {
		return ""
	}
	names := make([]string, 0, len(ref.Value.Properties))
	for n := range ref.Value.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
