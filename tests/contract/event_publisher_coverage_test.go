// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// eventsWithoutPublisher lists the event types production consumes but
// never emits, each with the reason it is allowed to stay that way.
//
// Same ratchet discipline as [eventsWithoutSubscriber]: an entry is a
// declaration that someone looked and decided the silence is correct.
// It may shrink freely; growing it needs a reason in the same commit,
// and "no producer yet" is not one — that is the state this guard
// exists to make visible.
var eventsWithoutPublisher = map[string]string{}

// TestEveryEventTypeHasAPublisher is the mirror image of
// [TestEveryEventTypeHasASubscriber]: it asserts that every event type
// the daemon defines is actually emitted by production code.
//
// A consumer with no producer is invisible in exactly the way a producer
// with no consumer is. The subscriber is registered, its handler is
// covered by a unit test that publishes the event onto a bus of its own,
// and the whole feature is dead in the running daemon. That is how the
// health tracker's activity pillar shipped: WireHealth subscribed to
// DataPointValueReceivedEvent and called RecordEventReceived, nothing on
// the wire path ever published it, so a fully healthy interface pushing
// callbacks for hours reported a zero "last event received" and could
// never score above 0.70. The subscriber-side guard is blind to that
// direction by construction.
func TestEveryEventTypeHasAPublisher(t *testing.T) {
	t.Parallel()
	pkgs := loadProductionPackages(t)

	defined := definedEventTypes(t, pkgs)
	if len(defined) == 0 {
		t.Fatal("no event types found in pkg/hmevent; the walk is broken and this test would pass vacuously")
	}
	published := publishedEventTypes(t, pkgs)
	if len(published) == 0 {
		t.Fatal("no events.Publish calls resolved; the walk is broken and this test would pass vacuously")
	}

	for _, name := range sortedKeys(defined) {
		_, hasPub := published[name]
		reason, declared := eventsWithoutPublisher[name]
		switch {
		case hasPub && declared:
			t.Errorf("%s is listed in eventsWithoutPublisher (%q) but %s publishes it — "+
				"drop the entry so the list keeps meaning what it says",
				name, reason, strings.Join(sortedStrings(published[name]), ", "))
		case !hasPub && !declared:
			t.Errorf("%s is consumed but nothing publishes it, and it carries no entry in "+
				"eventsWithoutPublisher — every subscriber of this event is silently dead. Wire a "+
				"producer, or declare the silence with the reason it is correct.", name)
		}
	}

	for name := range eventsWithoutPublisher {
		if _, ok := defined[name]; !ok {
			t.Errorf("eventsWithoutPublisher names %q, which is not an event type any more — "+
				"a stale entry silently exempts nothing and hides the next real one", name)
		}
	}
}

// publishedEventTypes maps an event type name onto the packages that
// publish it, resolving the value argument of every events.Publish call
// through the type checker. Test files are excluded from the load, so an
// event only its own tests emit counts as unpublished — which is the
// point.
func publishedEventTypes(t *testing.T, pkgs []*packages.Package) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.TypesInfo == nil || strings.Contains(p.PkgPath, "/tests/") {
			return
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isEventsPublish(call.Fun) || len(call.Args) < 2 {
					return true
				}
				name := publishedEventType(p, call)
				if name == "" {
					return true
				}
				if out[name] == nil {
					out[name] = map[string]bool{}
				}
				out[name][p.PkgPath] = true
				return true
			})
		}
	})
	return out
}

// isEventsPublish reports whether fun denotes events.Publish, with or
// without an explicit type argument.
func isEventsPublish(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.IndexExpr:
		return isEventsPublish(f.X)
	case *ast.IndexListExpr:
		return isEventsPublish(f.X)
	case *ast.SelectorExpr:
		id, ok := f.X.(*ast.Ident)
		return ok && id.Name == "events" && f.Sel.Name == "Publish"
	}
	return false
}

// publishedEventType resolves the concrete hmevent type a Publish call
// emits, from the static type of its value argument — a composite
// literal, a local variable or a parameter alike.
func publishedEventType(p *packages.Package, call *ast.CallExpr) string {
	tv, ok := p.TypesInfo.Types[call.Args[1]]
	if !ok || tv.Type == nil {
		return ""
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj.Pkg() == nil || !strings.HasSuffix(obj.Pkg().Path(), "/pkg/hmevent") {
		return ""
	}
	return obj.Name()
}
