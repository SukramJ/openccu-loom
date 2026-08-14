// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// registryWalkersWithoutAdoptSeam declares the registry walkers that
// deliberately have no per-central seam reachable from the runtime-adopt
// path, each with the reason the silence is correct.
//
// It is a ratchet in the same sense as wiringSettersWithoutCaller: it may
// shrink freely, and growing it needs a reason in the same commit. "A hook
// is coming later" is not one — a walker with no seam is precisely the
// state this guard exists to make loud.
var registryWalkersWithoutAdoptSeam = map[string]string{
	// Verified: the composition root re-runs the whole walk instead of
	// attaching one central. attachCentralHooks subscribes an adopted
	// central's bus to the same CentralSouthboundReadyEvent pipeline the
	// boot-time centrals feed (subscribeHubReadyTrigger), and the debounced
	// trigger calls Start again — which tears the plane down and rebuilds it
	// from the registry as it stands then, adopted central included. The
	// per-central half, wireOneCentral, stays internal on purpose: the hub
	// plane is serial-gated, so a central has to be re-walked once its serial
	// resolves anyway.
	"github.com/SukramJ/openccu-loom/internal/central/adapter.HubMQTTPublisher.Start": "the hub plane re-Starts on every central's southbound-ready event, which the adopt path subscribes for the adopted central too",
}

// TestEveryRegistryWalkerHasAnAdoptSeam asserts that every collaborator
// which subscribes to a central's event bus by walking the shared registry
// also exposes a per-central seam the composition root calls when a CCU is
// adopted at runtime.
//
// This is the guard for the second-largest defect class the daemon has
// produced. The shape never varies: a subsystem walks central.Registry.List()
// once during boot, subscribes to every unit it finds, and is never told
// about a CCU adopted afterwards. Thirteen instances were found by hand —
// measurement history, the outbound webhook, four WebSocket subscribers, the
// REST system-status buffer, the MQTT system-status plane, the values-cache
// flusher and more — and every one of them was invisible: the boot walk is
// correct, its own tests are green, and the adopted CCU simply never emits
// anything on that plane until the daemon is restarted.
//
// The guard checks the defect signature rather than the fix, because the fix
// has several legitimate shapes (an exported AttachCentral, an unexported
// helper shared with the boot walk, a composite hook). The signature is
// exact:
//
//  1. a range over (*central.Registry).List(), and
//  2. inside that loop, a call that carries something derived from the loop
//     variable into a subscription — events.Subscribe itself, or any function
//     that reaches one — and
//  3. no per-central entry point on the same receiver that the composition
//     root under cmd/ calls with a *central.Unit.
//
// Step 3 is what separates a live subsystem from a boot-only one. It is
// deliberately not "some test calls it": a seam only tests reach is a seam
// production does not have, which is the entire lesson of this class.
//
// Two things it deliberately does NOT flag. A method called ON the unit
// (u.Start(ctx)) is part of the unit's own lifecycle and runs identically for
// an adopted central, so the receiver position does not count as carrying the
// central out to a third party. And a walk that only reads the model —
// device lists, hub state, link peers — re-walks the registry on every call
// and therefore sees an adopted central without being told anything.
func TestEveryRegistryWalkerHasAnAdoptSeam(t *testing.T) {
	t.Parallel()
	pkgs := loadProductionPackages(t)

	g := buildCentralCallGraph(pkgs)
	if len(g.funcs) == 0 {
		t.Fatal("no functions resolved; the walk is broken and this test would pass vacuously")
	}
	walkers := g.subscribingRegistryWalkers()
	// The current tree has one walker per north-bound plane that fans a
	// central's bus out; a load that finds none has stopped resolving
	// (*central.Registry).List and would pass whatever the code does.
	if len(walkers) < 5 {
		t.Fatalf("found only %d registry walkers that subscribe; the analysis is broken "+
			"and this test would pass vacuously", len(walkers))
	}

	var orphans []string
	for _, w := range walkers {
		seam, ok := g.adoptSeamFor(w)
		if ok {
			if reason, declared := registryWalkersWithoutAdoptSeam[w.key]; declared {
				t.Errorf("%s is listed in registryWalkersWithoutAdoptSeam (%q) but %s IS called "+
					"from the composition root — drop the entry so the list keeps meaning what it says",
					w.key, reason, seam)
			}
			continue
		}
		if _, declared := registryWalkersWithoutAdoptSeam[w.key]; declared {
			continue
		}
		orphans = append(orphans, fmt.Sprintf("  %s\n      walks the registry at %s", w.key, w.pos))
	}
	if len(orphans) > 0 {
		t.Errorf("%d registry walker(s) that subscribe to every central found at boot and have no "+
			"per-central seam the composition root calls on adopt — a CCU added at runtime is "+
			"silent on this plane until the daemon restarts, and nothing reports it:\n%s\n"+
			"Replace the walk with central.Registry.OnRegister — it replays over the centrals "+
			"already registered and fires for every later one, so boot and adopt are one "+
			"registration — or, when the attach order relative to the south-bound bring-up is "+
			"load-bearing, add a per-central entry point taking a *central.Unit that the "+
			"composition root calls on adopt. Otherwise declare it in "+
			"registryWalkersWithoutAdoptSeam with the reason the silence is correct.",
			len(orphans), strings.Join(orphans, "\n"))
	}

	for key := range registryWalkersWithoutAdoptSeam {
		if !g.isWalker(key, walkers) {
			t.Errorf("registryWalkersWithoutAdoptSeam names %q, which no longer walks the registry — "+
				"a stale entry exempts nothing and hides the next real one", key)
		}
	}
}

// centralCallGraph is the resolved view the analysis works on: every
// function this module declares, who it calls, and whether it can reach a
// subscription.
type centralCallGraph struct {
	funcs map[string]*analysedFunc
	// callers maps a callee key to the packages that call it. The package
	// is all the seam check needs: the question is whether the composition
	// root calls it, not from which line.
	callers map[string]map[string]bool
	// subscribes is the transitive closure of "reaches events.Subscribe".
	subscribes map[string]bool
}

type analysedFunc struct {
	key   string
	pos   string
	obj   *types.Func
	decl  *ast.FuncDecl
	pkg   *packages.Package
	recv  string // receiver type name, "" for plain functions
	calls map[string]bool
}

// registryWalk is one range over (*central.Registry).List() that carries the
// loop variable into a subscription.
type registryWalk struct {
	key  string // the enclosing function
	pos  string
	fn   *analysedFunc
	args []string // keys of the callees the loop variable is handed to
}

func buildCentralCallGraph(pkgs []*packages.Package) *centralCallGraph {
	g := &centralCallGraph{
		funcs:      map[string]*analysedFunc{},
		callers:    map[string]map[string]bool{},
		subscribes: map[string]bool{},
	}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !ownPackage(p) {
			return
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Name == nil {
					continue
				}
				obj, ok := p.TypesInfo.Defs[fn.Name].(*types.Func)
				if !ok {
					continue
				}
				key := funcKey(obj)
				if key == "" {
					key = obj.Pkg().Path() + "." + obj.Name()
				}
				af := &analysedFunc{
					key:   key,
					pos:   shortPos(p.Fset, fn.Pos()),
					obj:   obj,
					decl:  fn,
					pkg:   p,
					recv:  receiverTypeName(obj),
					calls: map[string]bool{},
				}
				g.funcs[key] = af
			}
		}
	})

	// Edges. A nested function literal's calls are attributed to the
	// enclosing declaration: a closure the walk hands to a helper is the
	// same wiring act as an inline call, and treating them separately would
	// let the same subscription hide behind one more indirection.
	for _, af := range g.funcs {
		ast.Inspect(af.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := calleeFunc(af.pkg, call)
			if callee == nil {
				return true
			}
			key := calleeKey(callee)
			if key == "" {
				return true
			}
			af.calls[key] = true
			if g.callers[key] == nil {
				g.callers[key] = map[string]bool{}
			}
			g.callers[key][af.pkg.PkgPath] = true
			return true
		})
	}

	// "Reaches a subscription", as a fixpoint over the edges above.
	for key, af := range g.funcs {
		for callee := range af.calls {
			if isEventSubscribeKey(callee) {
				g.subscribes[key] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for key, af := range g.funcs {
			if g.subscribes[key] {
				continue
			}
			for callee := range af.calls {
				if g.subscribes[callee] {
					g.subscribes[key] = true
					changed = true
					break
				}
			}
		}
	}
	return g
}

// isEventSubscribeKey reports whether key names the generic bus subscription
// every consumer in this daemon goes through.
func isEventSubscribeKey(key string) bool {
	return key == modulePath+"/internal/central/events.Subscribe"
}

// calleeFunc resolves the function a call expression targets, or nil when it
// is not a function this module declares (a dynamic call through a func
// value, a stdlib call, a conversion).
func calleeFunc(p *packages.Package, call *ast.CallExpr) *types.Func {
	fun := ast.Unparen(call.Fun)
	if idx, ok := fun.(*ast.IndexExpr); ok { // generic instantiation: events.Subscribe[T]
		fun = ast.Unparen(idx.X)
	}
	var ident *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		ident = f
	case *ast.SelectorExpr:
		ident = f.Sel
	default:
		return nil
	}
	fn, _ := p.TypesInfo.Uses[ident].(*types.Func)
	if fn == nil || fn.Pkg() == nil || !strings.HasPrefix(fn.Pkg().Path(), modulePath) {
		return nil
	}
	return fn
}

func calleeKey(fn *types.Func) string {
	if key := funcKey(fn); key != "" {
		return key
	}
	if fn.Pkg() == nil {
		return ""
	}
	return fn.Pkg().Path() + "." + fn.Name()
}

// subscribingRegistryWalkers finds every function that ranges over
// (*central.Registry).List() and carries the loop variable into a
// subscription.
func (g *centralCallGraph) subscribingRegistryWalkers() []registryWalk {
	var out []registryWalk
	// One function can hold several walks — the Matter runtime wiring holds
	// two. They are the same seam question, so the first one answers for the
	// function and a second entry would only duplicate the report.
	seen := map[string]bool{}
	for _, af := range g.funcs {
		ast.Inspect(af.decl.Body, func(n ast.Node) bool {
			rng, ok := n.(*ast.RangeStmt)
			if !ok || !g.rangesOverRegistryList(af.pkg, rng) {
				return true
			}
			unit, ok := af.pkg.TypesInfo.Defs[identOf(rng.Value)].(*types.Var)
			if !ok || unit == nil {
				return true
			}
			args, subscribes := g.loopCarriesUnitIntoSubscription(af, rng, unit)
			if !subscribes || seen[af.key] {
				return true
			}
			seen[af.key] = true
			out = append(out, registryWalk{
				key:  af.key,
				pos:  shortPos(af.pkg.Fset, rng.Pos()),
				fn:   af,
				args: args,
			})
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

func identOf(e ast.Expr) *ast.Ident {
	id, _ := e.(*ast.Ident)
	return id
}

// rangesOverRegistryList reports whether the range expression is a call to
// (*central.Registry).List.
func (g *centralCallGraph) rangesOverRegistryList(p *packages.Package, rng *ast.RangeStmt) bool {
	call, ok := ast.Unparen(rng.X).(*ast.CallExpr)
	if !ok {
		return false
	}
	fn := calleeFunc(p, call)
	if fn == nil || fn.Name() != "List" {
		return false
	}
	return receiverTypeName(fn) == "Registry" &&
		fn.Pkg() != nil && fn.Pkg().Path() == modulePath+"/internal/central"
}

// loopCarriesUnitIntoSubscription reports whether the loop body hands
// something derived from the unit to a call that reaches a subscription, and
// returns the callees it was handed to.
//
// Only argument positions count. A method called on the unit itself is the
// unit's own lifecycle — Start, Stop, WireDevicesCreatedGate — and the adopt
// path runs those for every central it builds, so it cannot be the defect
// this guard looks for.
func (g *centralCallGraph) loopCarriesUnitIntoSubscription(
	af *analysedFunc, rng *ast.RangeStmt, unit *types.Var,
) (args []string, subscribes bool) {
	seen := map[string]bool{}
	ast.Inspect(rng.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		carries := false
		for _, arg := range call.Args {
			if mentions(af.pkg, arg, unit) {
				carries = true
				break
			}
		}
		if !carries {
			return true
		}
		callee := calleeFunc(af.pkg, call)
		if callee == nil {
			return true
		}
		key := calleeKey(callee)
		if !isEventSubscribeKey(key) && !g.subscribes[key] {
			return true
		}
		subscribes = true
		if !seen[key] {
			seen[key] = true
			args = append(args, key)
		}
		return true
	})
	sort.Strings(args)
	return args, subscribes
}

// mentions reports whether the expression reads the loop variable.
func mentions(p *packages.Package, e ast.Expr, unit *types.Var) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if p.TypesInfo.Uses[id] == unit {
			found = true
		}
		return !found
	})
	return found
}

// adoptSeamFor looks for the per-central entry point the composition root
// calls for a runtime-adopted central: a function that takes a *central.Unit,
// reaches a subscription, belongs to the same receiver type (or the same
// package for a plain function), and is called from a package under cmd/.
func (g *centralCallGraph) adoptSeamFor(w registryWalk) (string, bool) {
	for _, key := range append(append([]string{}, w.args...), g.siblingSeams(w)...) {
		seam, ok := g.funcs[key]
		if !ok || !g.subscribes[key] || !g.isPerCentralSeam(seam) {
			continue
		}
		for caller := range g.callers[key] {
			if strings.HasPrefix(caller, modulePath+"/cmd/") {
				return key, true
			}
		}
	}
	return "", false
}

// siblingSeams lists the candidates that are not reached from the walk body
// itself: a per-central attach that duplicates the walk rather than being
// delegated to by it. The outbound webhook is the live example — its boot
// walk subscribes through a shared helper while AttachCentral is what the
// adopt path calls.
func (g *centralCallGraph) siblingSeams(w registryWalk) []string {
	var out []string
	for key, af := range g.funcs {
		if af.pkg.PkgPath != w.fn.pkg.PkgPath || key == w.key {
			continue
		}
		if w.fn.recv != "" && af.recv != w.fn.recv {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// isPerCentralSeam reports whether the function attaches ONE central rather
// than all of them.
//
// Both shapes in the tree count. Most seams take the unit itself; the alarm
// and Security & Safety services take the central's name instead and resolve
// it through Registry.Get, because their unwire half is keyed by name too. A
// rule that demanded the pointer would report both of them as unwired while
// the composition root calls them on every adopt.
func (g *centralCallGraph) isPerCentralSeam(seam *analysedFunc) bool {
	if takesCentralUnit(seam.obj) {
		return true
	}
	return seam.calls[modulePath+"/internal/central.Registry.Get"]
}

// takesCentralUnit reports whether the function accepts a *central.Unit —
// the shape of a per-central seam.
func takesCentralUnit(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return false
	}
	for i := range sig.Params().Len() {
		ptr, ok := sig.Params().At(i).Type().(*types.Pointer)
		if !ok {
			continue
		}
		named, ok := ptr.Elem().(*types.Named)
		if !ok {
			continue
		}
		if named.Obj().Name() == "Unit" && named.Obj().Pkg() != nil &&
			named.Obj().Pkg().Path() == modulePath+"/internal/central" {
			return true
		}
	}
	return false
}

func (g *centralCallGraph) isWalker(key string, walkers []registryWalk) bool {
	for _, w := range walkers {
		if w.key == key {
			return true
		}
	}
	return false
}
