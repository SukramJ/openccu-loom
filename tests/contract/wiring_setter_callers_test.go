// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// wiringSettersWithoutCaller lists the wiring seams that production never
// calls, each with the reason that is allowed.
//
// It is a ratchet: it may shrink freely, and every entry removed is a
// seam that either got wired or got deleted. Growing it needs a reason in
// the same commit — and "a caller is coming later" is not one, because a
// seam with no caller is exactly the state this guard exists to make
// loud. A seam kept for an external consumer belongs here with that said
// plainly; a seam kept for tests belongs deleted, because a setter only
// tests call is a setter production does not need.
var wiringSettersWithoutCaller = map[string]string{
	// Verified: an alternative production path carries the same duty.
	"github.com/SukramJ/openccu-loom/internal/central.Unit.SetHubLogoutFn":                  "logout on Stop already runs through the closer WireHub returns (addCloser); this hook is a second, dead path",
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile.Profile.SetPublishHook":     "the profile-change push flows through Profile.OnChange, which the event bridge subscribes to; this is an unused parallel API",
	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge.Bridge.AttachCaseHandler": "CASE dispatch is wired via AttachCaseHandlerProvider, which takes precedence for every exchange; this is the unused singleton fallback",
	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge.CaseAdapter.SetResponder": "identity rotation builds a fresh CaseAdapter plus Responder per AddNOC; nothing swaps a responder into an existing adapter",

	// Verified: the component around the seam is unmounted by a
	// documented choice, so the seam is dead along with it.
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core.DiagnosticLogs.AttachProvider": "the DiagnosticLogs cluster is deliberately not mounted on the root endpoint; the dead-code inventory already exempts the file",
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/light.ColorControlServer.SetWriter": "colour-temperature lights are served by internal/model/custom/light, not by this standalone reference server, which nothing constructs",

	// Verified: test-only seams. Listed rather than deleted because
	// removing them is a separate change with its own review.
	"github.com/SukramJ/openccu-loom/internal/diagnostics.Manager.SetClockForTesting":          "test seam; production takes the default clock",
	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge.PaseAdapter.SetRandomSource": "test seam; production reads crypto/rand",
}

// wiringSeamsUnderInvestigation is the same shape and a different claim.
//
// An entry here says nobody has established what the seam is for — not
// that the silence is correct. It is separated from the map above on
// purpose: merging the two would let "we looked and it is fine" and "we
// have not looked" wear the same face, which is how a ratchet quietly
// turns into a permit.
//
// This map exists to be emptied. Every entry is either wired, deleted, or
// promoted to the verified map with a reason.
var wiringSeamsUnderInvestigation = map[string]string{
	// The central's own service hooks. The methods they feed
	// (SaveFiles, ValidateConfigAndGetSystemInformation,
	// LoadAndRefreshDataPointDataForInterface, AcceptDeviceInbox) have no
	// production callers either, so each is a dead surface rather than a
	// broken wire — but whether they should be built or removed is a
	// product decision.
	"github.com/SukramJ/openccu-loom/internal/central.Unit.SetAcceptInboxFn":                "reached only through ServiceRegistry.Invoke, which production never calls",
	"github.com/SukramJ/openccu-loom/internal/central.Unit.SetLoadAndRefreshForInterfaceFn": "the method it feeds has no callers at all",
	"github.com/SukramJ/openccu-loom/internal/central.Unit.SetSaveFilesFn":                  "the method it feeds has no callers outside tests",
	"github.com/SukramJ/openccu-loom/internal/central.Unit.SetValidateConfigFn":             "the method it feeds has no callers outside tests",

	// Coordinator and facade seams. SetRecorder is wired on the hub and
	// recovery coordinators and not on the device one, which reads more
	// like an omission than a decision.
	"github.com/SukramJ/openccu-loom/internal/central.QueryFacade.SetHubStatePathProvider":                         "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central.QueryFacade.SetInstallModeProvider":                          "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/adapter.BackupAdapter.SetRestorer":                           "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.CacheCoordinator.SetParamsetInvalidator":        "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.CacheCoordinator.SetPersister":                  "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.DeviceCoordinator.SetDeviceNameOverrideChecker": "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.DeviceCoordinator.SetRecorder":                  "the sibling coordinators receive the recorder at central.go:213-214; this one does not",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.HubCoordinator.SetProgramStateWriter":           "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.HubCoordinator.SetSysvarGetter":                 "unclassified",
	"github.com/SukramJ/openccu-loom/internal/central/coordinators.HubCoordinator.SetSysvarValueWriter":            "unclassified",

	// A confirmed consequence, kept here because fixing it is feature
	// work: nothing ever attaches a generic event to a channel, and
	// nothing constructs the only attachable implementation either, so
	// GET /devices/{addr}/channels/{no}/event-groups can only ever answer
	// with an empty list.
	"github.com/SukramJ/openccu-loom/internal/model/device.Channel.AttachGenericEvent": "no producer exists, so the event-groups REST route always returns []",
}

// TestEveryWiringSetterHasAProductionCaller asserts that every method
// which injects a collaborator is actually called by production code.
//
// This is the mechanical half of the rule that wiring is pinned through
// the composition root. The rule asks for a pin that asserts the effect;
// no test can check that a pin asserts the right thing. What a test can
// check is the defect signature, and the signature is specific: in
// 0.52.12 the hub notifiers were dead because SetHubModel had no
// production caller at all. The coordinator tests called it themselves,
// so they stayed green while every hub push event was silently lost.
//
// A seam with no caller is not merely untested. It is unreachable: the
// collaborator is never handed over, so the feature behind it cannot
// work, and nothing in a normal test run says so.
//
// Note the asymmetry with a plain unused-code check. The dead-code
// analyser sees these methods as reachable — they are exported, and
// something in a test or another package mentions them. Only the
// question "does *production* ever call this" separates a live seam from
// a decorative one.
func TestEveryWiringSetterHasAProductionCaller(t *testing.T) {
	t.Parallel()
	pkgs := loadProductionPackages(t)

	seams := findWiringSeams(pkgs)
	if len(seams) == 0 {
		t.Fatal("no wiring seams found; the walk is broken and this test would pass vacuously")
	}
	called, dispatched := findCalledFuncs(pkgs)
	if len(called) == 0 && len(dispatched) == 0 {
		t.Fatal("no calls resolved; the walk is broken and this test would pass vacuously")
	}

	var orphans []string
	for _, key := range sortedSeamKeys(seams) {
		if called[key] || wiredViaInterface(seams[key].recv, dispatched) {
			if reason, declared := declaredReason(key); declared {
				t.Errorf("%s is listed in wiringSettersWithoutCaller (%q) but production does call "+
					"it — drop the entry so the list keeps meaning what it says", key, reason)
			}
			continue
		}
		if _, declared := declaredReason(key); declared {
			continue
		}
		orphans = append(orphans, fmt.Sprintf("  %s\n      declared at %s", key, seams[key].pos))
	}
	if len(orphans) > 0 {
		t.Errorf("%d wiring seam(s) that production never calls — the collaborator is never handed "+
			"over, so the feature behind each of these cannot work and no test says so:\n%s\n"+
			"Wire it through the composition root, delete it, or declare it in "+
			"wiringSettersWithoutCaller with the reason the silence is correct.",
			len(orphans), strings.Join(orphans, "\n"))
	}

	for key := range allDeclared() {
		if _, ok := seams[key]; !ok {
			t.Errorf("wiringSettersWithoutCaller names %q, which is not a wiring seam any more — "+
				"a stale entry exempts nothing and hides the next real one", key)
		}
	}
}

// declaredReason looks a seam up in either declaration map.
func declaredReason(key string) (reason string, ok bool) {
	if r, found := wiringSettersWithoutCaller[key]; found {
		return r, true
	}
	r, found := wiringSeamsUnderInvestigation[key]
	return r, found
}

// allDeclared is the union, used to catch stale entries in either map.
func allDeclared() map[string]string {
	out := make(map[string]string, len(wiringSettersWithoutCaller)+len(wiringSeamsUnderInvestigation))
	for k, v := range wiringSettersWithoutCaller {
		out[k] = v
	}
	for k, v := range wiringSeamsUnderInvestigation {
		out[k] = v
	}
	return out
}

// findWiringSeams collects every exported Set*/Attach*/Register* method
// that injects a collaborator, keyed as pkg.Type.Method.
//
// The filter is deliberately about shape rather than about the receiver's
// name. A name-based rule ("types called *Service") would miss the seam
// on a type nobody thought to name that way, which is the only kind that
// goes unnoticed.
func findWiringSeams(pkgs []*packages.Package) map[string]wiringSeam {
	out := map[string]wiringSeam{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !ownPackage(p) {
			return
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name == nil || !fn.Name.IsExported() {
					continue
				}
				if !isWiringVerb(fn.Name.Name) {
					continue
				}
				obj, ok := p.TypesInfo.Defs[fn.Name].(*types.Func)
				if !ok || !injectsCollaborator(obj) {
					continue
				}
				recv := receiverTypeName(obj)
				if recv == "" {
					continue
				}
				key := p.PkgPath + "." + recv + "." + fn.Name.Name
				out[key] = wiringSeam{pos: shortPos(p.Fset, fn.Pos()), recv: obj.Type().(*types.Signature).Recv().Type()}
			}
		}
	})
	return out
}

// modulePath scopes both walks to this repository. NeedDeps pulls the
// whole dependency graph into the load, and a seam inside a third-party
// module is not this project's to wire.
const modulePath = "github.com/SukramJ/openccu-loom"

func ownPackage(p *packages.Package) bool {
	return p.TypesInfo != nil &&
		strings.HasPrefix(p.PkgPath, modulePath) &&
		!strings.Contains(p.PkgPath, "/tests/")
}

func isWiringVerb(name string) bool {
	for _, verb := range []string{"Set", "Attach", "Register"} {
		if strings.HasPrefix(name, verb) && len(name) > len(verb) &&
			name[len(verb)] >= 'A' && name[len(verb)] <= 'Z' {
			return true
		}
	}
	return false
}

// injectsCollaborator reports whether fn has the shape of a wiring seam:
// it takes exactly one collaborator and answers nothing but itself.
//
// A collaborator is an interface, a function value or a pointer to a
// named type — the three ways this codebase hands one component to
// another. A plain data setter (SetName(string), SetPort(int)) is not a
// seam: forgetting to call it leaves a default, not a dead feature.
//
// The fluent form counts. An earlier version of this filter demanded no
// results at all and therefore skipped every `Set*` that returns its
// receiver for chaining — including HubCoordinator.SetHubModel, the seam
// whose missing caller in 0.52.12 is the reason this guard exists. A
// filter that excludes the founding example is not strict, it is blind.
func injectsCollaborator(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Params().Len() != 1 || !returnsNothingOrSelf(sig) {
		return false
	}
	switch t := sig.Params().At(0).Type().(type) {
	case *types.Pointer:
		_, named := t.Elem().(*types.Named)
		return named
	case *types.Signature:
		return true
	case *types.Named:
		_, isIface := t.Underlying().(*types.Interface)
		return isIface
	case *types.Interface:
		return true
	}
	return false
}

// returnsNothingOrSelf accepts the plain setter and the fluent one, and
// nothing else: a method that answers a value the caller is meant to use
// is doing work, not wiring.
func returnsNothingOrSelf(sig *types.Signature) bool {
	switch sig.Results().Len() {
	case 0:
		return true
	case 1:
		return sig.Recv() != nil && types.Identical(sig.Results().At(0).Type(), sig.Recv().Type())
	}
	return false
}

func receiverTypeName(fn *types.Func) string {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return ""
	}
	t := sig.Recv().Type()
	if ptr, isPtr := t.(*types.Pointer); isPtr {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	return named.Obj().Name()
}

// findCalledFuncs collects what production calls: concrete methods by
// their own key, plus the interface types production dispatches through.
//
// The second half is not a nicety. This codebase wires collaborators
// through interfaces as a matter of course — the Matter bridge hands
// itself to every cluster with `recv.SetMatterEventEmitter(b)` over an
// [interfaces.MatterEventReceiver] — and a walk that only resolved the
// call to the interface method would report all fourteen concrete
// implementations as unwired. That is not a stricter guard, it is a
// wrong one: fourteen false alarms teach a reader to skim the list, and
// the real seam hides in the noise.
//
// Only non-test packages are loaded, so a call found here is a production
// call by construction. A method only its own tests call would otherwise
// look wired.
func findCalledFuncs(pkgs []*packages.Package) (concrete map[string]bool, ifaces []*types.Interface) {
	concrete = map[string]bool{}
	seen := map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !ownPackage(p) {
			return
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				fn, ok := p.TypesInfo.Uses[sel.Sel].(*types.Func)
				if !ok {
					return true
				}
				if !isWiringVerb(fn.Name()) {
					return true
				}
				if iface := receiverInterface(fn); iface != nil {
					if id := fn.Pkg().Path() + "." + receiverTypeName(fn); !seen[id] {
						seen[id] = true
						ifaces = append(ifaces, iface)
					}
					return true
				}
				if key := funcKey(fn); key != "" {
					concrete[key] = true
				}
				return true
			})
		}
	})
	return concrete, ifaces
}

// wiredViaInterface reports whether production hands this seam's
// receiver over through one of the interfaces it dispatches on.
func wiredViaInterface(recv types.Type, ifaces []*types.Interface) bool {
	if recv == nil {
		return false
	}
	for _, iface := range ifaces {
		if types.Implements(recv, iface) {
			return true
		}
	}
	return false
}

// receiverInterface returns the interface a method is declared on, or nil
// when the receiver is a concrete type.
func receiverInterface(fn *types.Func) *types.Interface {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return nil
	}
	named, ok := sig.Recv().Type().(*types.Named)
	if !ok {
		return nil
	}
	iface, _ := named.Underlying().(*types.Interface)
	return iface
}

// funcKey renders a method as pkg.Type.Method, matching the key
// [findWiringSeams] produces.
func funcKey(fn *types.Func) string {
	if fn.Pkg() == nil {
		return ""
	}
	recv := receiverTypeName(fn)
	if recv == "" {
		return ""
	}
	return fn.Pkg().Path() + "." + recv + "." + fn.Name()
}

func shortPos(fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	if i := strings.Index(p.Filename, "openccu-loom/"); i >= 0 {
		p.Filename = p.Filename[i+len("openccu-loom/"):]
	}
	return fmt.Sprintf("%s:%d", p.Filename, p.Line)
}

// wiringSeam is one seam plus the receiver type, which is what lets the
// interface-dispatch check ask whether production wires it indirectly.
type wiringSeam struct {
	pos  string
	recv types.Type
}

func sortedSeamKeys(m map[string]wiringSeam) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
