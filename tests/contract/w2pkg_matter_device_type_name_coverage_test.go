// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"go/ast"
	"go/constant"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/schema"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// [interfaces.MatterDeviceTypeName] is the only place a Matter device type
// acquires an operator-facing name: the REST layer stamps its result on every
// /api/v1/matter/exposable row as device_type_label, and the SPA groups,
// filters and text-searches the exposure list by that exact string. An ID
// with no case falls through to a hex fallback, so the device lands in a
// "0x0230" bucket and never matches a search for what it is.
//
// The table is hand-written, and its own doc comment claims to cover every
// device type the model advertises — a claim nothing measured until this
// guard. The two producers of a device type are enumerated here rather than
// asserted:
//
//   - endpoint sources: every MatterDeviceType() method in internal/model,
//     read out of the type checker, with each returned constant resolved to
//     its value. A method returning a non-constant expression is reported as
//     unresolvable rather than guessed at.
//   - measurement classes: [contract.MeasurementClassDeviceType] over
//     measurementClasses, the list that
//     TestMeasurementClassEnumerationIsComplete keeps complete.
//
// The names themselves are cross-checked against schema.DeviceTypeNames,
// codegen'd from the matter.js HEAD snapshot, so a label cannot be invented
// for an ID the Device Library does not define.

// w2PkgMatterDeviceTypeSource is one place a device-type ID enters the
// eligibility verdict, named for the failure message.
type w2PkgMatterDeviceTypeSource struct {
	id   uint16
	from string
}

// w2PkgAdvertisedDeviceTypes resolves every device-type ID the model layer
// can return from a MatterDeviceType method. It fails the test rather than
// skipping when a return expression is not a resolvable constant: an
// unmeasured branch would silently shrink the guard's reach.
func w2PkgAdvertisedDeviceTypes(t *testing.T) []w2PkgMatterDeviceTypeSource {
	t.Helper()

	pkgs := loadProductionPackages(t)
	var out []w2PkgMatterDeviceTypeSource
	methods := 0

	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !strings.Contains(p.PkgPath, "/internal/model/") {
			return
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name.Name != "MatterDeviceType" || fn.Body == nil {
					continue
				}
				methods++
				where := fmt.Sprintf("%s.%s", p.PkgPath[strings.LastIndex(p.PkgPath, "/")+1:], w2PkgReceiverName(fn))
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					ret, ok := n.(*ast.ReturnStmt)
					if !ok || len(ret.Results) != 1 {
						return true
					}
					tv, ok := p.TypesInfo.Types[ret.Results[0]]
					if !ok || tv.Value == nil {
						t.Errorf("%s: return expression is not a resolvable constant, so the device type it "+
							"advertises cannot be measured — extend this guard or make the value a constant",
							where)
						return true
					}
					v, exact := constant.Uint64Val(tv.Value)
					if !exact || v > 0xFFFF {
						t.Errorf("%s: returned constant %s does not fit a device-type ID", where, tv.Value)
						return true
					}
					out = append(out, w2PkgMatterDeviceTypeSource{id: uint16(v), from: where})
					return true
				})
			}
		}
	})

	if methods == 0 {
		t.Fatal("no MatterDeviceType method found under internal/model — the walk measures nothing")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func w2PkgReceiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "?"
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}

// TestW2PkgMatterDeviceTypeNameCoversEveryAdvertisedType holds the table's
// own claim: every device type the model can put into an eligibility verdict
// has an operator-facing name.
func TestW2PkgMatterDeviceTypeNameCoversEveryAdvertisedType(t *testing.T) {
	t.Parallel()

	sources := w2PkgAdvertisedDeviceTypes(t)
	for _, class := range measurementClasses {
		if id := contract.MeasurementClassDeviceType(class); id != 0 {
			sources = append(sources, w2PkgMatterDeviceTypeSource{
				id:   id,
				from: fmt.Sprintf("MatterMeasurementClassDeviceType(class %d)", class),
			})
		}
	}

	seen := map[uint16]bool{}
	for _, src := range sources {
		if src.id == 0 || seen[src.id] {
			continue
		}
		seen[src.id] = true

		name := interfaces.MatterDeviceTypeName(src.id)
		if name == fmt.Sprintf("0x%04X", src.id) {
			t.Errorf("MatterDeviceTypeName(0x%04X) falls through to the hex fallback, but %s advertises that "+
				"device type; the SPA then groups and searches the exposure row under the raw hex string. "+
				"matter.js HEAD names it %q (go-fabric schema/devicetypes.go)",
				src.id, src.from, schema.DeviceTypeNames[uint32(src.id)])
			continue
		}
		if _, ok := schema.DeviceTypeNames[uint32(src.id)]; !ok {
			t.Errorf("MatterDeviceTypeName names 0x%04X %q, but the matter.js HEAD schema snapshot defines no "+
				"such device type", src.id, name)
		}
	}
}
