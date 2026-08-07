// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/types"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// eventsWithoutSubscriber lists the event types that production
// publishes and nothing consumes, each with the reason it is allowed to
// stay that way.
//
// An entry here is a declaration, not an excuse: it says someone looked
// at this event and decided the silence is correct. Removing the last
// subscriber from an event that is NOT listed here fails the build, and
// adding a new event that nobody consumes fails it too.
//
// This map is a ratchet. It may shrink freely. Growing it needs a reason
// in the same commit that adds the entry — and "nothing consumes it yet"
// is not one, because that is precisely the state this guard exists to
// make visible rather than silent.
var eventsWithoutSubscriber = map[string]string{
	// Diagnostics-only signals. They exist so an operator dump can
	// reconstruct what the cache and refresh machinery did; no runtime
	// surface reacts to them, and none is planned.
	"CacheInvalidatedEvent":     "diagnostic trace of a cache drop; no runtime surface reacts",
	"DataRefreshTriggeredEvent": "diagnostic trace of a refresh start; the completion event carries the result",
	"DataRefreshCompletedEvent": "diagnostic trace of a refresh end; callers read the resulting model, not the event",
	"DriftCorrectedEvent":       "diagnostic trace of a clock-drift correction; corrective action already happened",
	"RPCParameterReceivedEvent": "per-parameter wire trace; the value change is carried by DataPointValueChangedEvent",

	// Connection-recovery telemetry. The recovery coordinator drives the
	// reconnect itself; this event announces what it did, and the health
	// tracker reads the client state directly instead of consuming it.
	// The per-stage/per-attempt progress events reach operators through
	// the diagnostics event-bus tap (subscribeCuratedEvents) and are
	// therefore no longer listed here.
	"ConnectionHealthChangedEvent": "recovery telemetry; the health tracker derives its own verdict from the client state",

	// The functional path is the data point's own change callback, which
	// the event bridge subscribes to and which drives MQTT and the SPA.
	// This event duplicates that signal on the bus for a consumer that
	// was never written.
	"WeekProfileChangedEvent": "the schedule change reaches MQTT/SPA through ProfileDataPoint.OnChange, not through the bus",
}

// TestEveryEventTypeHasASubscriber asserts that every event type the
// daemon defines is consumed by production code, or is declared in
// [eventsWithoutSubscriber] as deliberately unconsumed.
//
// It exists because publishing into silence is invisible. The bus has no
// wildcard subscription — events.Subscribe[T] is the only way in — so an
// event with no subscriber reaches nothing at all, and every test around
// it still passes: the producer's test asserts it published, and the
// would-be consumer's test builds its own bus and publishes onto it
// directly. Both halves work. Nothing joins them.
//
// That is the same seam that made SetHubModel dead in 0.52.12 and the
// alarm sink drop AlarmDuressEvent, and it has produced at least one
// code comment per instance asserting consumers that do not exist:
//
//   - device_pipeline.go publishes WeekProfileChangedEvent "so MQTT/WS
//     subscribers" can react. Neither subscribes.
//   - engine.go publishes AlarmDuressEvent "for the MQTT/webhook
//     consumers". Only the webhook subscribes.
//
// A comment naming a consumer is a hypothesis. This test is the check.
func TestEveryEventTypeHasASubscriber(t *testing.T) {
	t.Parallel()
	pkgs := loadProductionPackages(t)

	defined := definedEventTypes(t, pkgs)
	if len(defined) == 0 {
		t.Fatal("no event types found in pkg/hmevent; the walk is broken and this test would pass vacuously")
	}
	subscribed := subscribedEventTypes(t, pkgs)
	if len(subscribed) == 0 {
		t.Fatal("no events.Subscribe calls resolved; the walk is broken and this test would pass vacuously")
	}

	for _, name := range sortedKeys(defined) {
		_, hasSub := subscribed[name]
		reason, declared := eventsWithoutSubscriber[name]
		switch {
		case hasSub && declared:
			t.Errorf("%s is listed in eventsWithoutSubscriber (%q) but %s subscribes to it — "+
				"drop the entry so the list keeps meaning what it says",
				name, reason, strings.Join(sortedStrings(subscribed[name]), ", "))
		case !hasSub && !declared:
			t.Errorf("%s is published but nothing subscribes to it, and it carries no entry in "+
				"eventsWithoutSubscriber — every consumer of this event is silently dead. Wire a "+
				"consumer, or declare the silence with the reason it is correct.", name)
		}
	}

	for name := range eventsWithoutSubscriber {
		if _, ok := defined[name]; !ok {
			t.Errorf("eventsWithoutSubscriber names %q, which is not an event type any more — "+
				"a stale entry silently exempts nothing and hides the next real one", name)
		}
	}
}

// loadProductionPackages loads every non-test package of the module with
// full type information. The type checker is what makes the resolution
// trustworthy: a handler is matched by its actual parameter type, not by
// a method name that several receivers in one package may share.
func loadProductionPackages(t *testing.T) []*packages.Package {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Dir:   repoRoot(t),
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./internal/...", "./cmd/...", "./pkg/...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	var hard int
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		hard += len(p.Errors)
	})
	if hard > 0 {
		t.Fatalf("packages.Load reported %d type errors; resolution would be unreliable", hard)
	}
	return pkgs
}

// definedEventTypes collects every named struct type in pkg/hmevent
// whose name ends in Event and that implements hmevent.Event.
func definedEventTypes(t *testing.T, pkgs []*packages.Package) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !strings.HasSuffix(p.PkgPath, "/pkg/hmevent") {
			return
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || !obj.Exported() || !strings.HasSuffix(name, "Event") {
				continue
			}
			if _, isStruct := obj.Type().Underlying().(*types.Struct); !isStruct {
				continue
			}
			out[name] = true
		}
	})
	return out
}

// subscribedEventTypes maps an event type name onto the packages that
// subscribe to it, resolving the handler argument of every
// events.Subscribe call through the type checker.
func subscribedEventTypes(t *testing.T, pkgs []*packages.Package) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.TypesInfo == nil || strings.Contains(p.PkgPath, "/tests/") {
			return
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isEventsSubscribe(call.Fun) || len(call.Args) < 2 {
					return true
				}
				name := handlerEventType(p, call)
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

// isEventsSubscribe reports whether fun denotes events.Subscribe, with
// or without an explicit type argument.
func isEventsSubscribe(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.IndexExpr:
		return isEventsSubscribe(f.X)
	case *ast.IndexListExpr:
		return isEventsSubscribe(f.X)
	case *ast.SelectorExpr:
		id, ok := f.X.(*ast.Ident)
		return ok && id.Name == "events" && f.Sel.Name == "Subscribe"
	}
	return false
}

// handlerEventType resolves the concrete hmevent type a Subscribe call
// binds, by reading the type of its handler argument. Subscribe's
// signature is func(*Bus, func(T), ...HandlerOption), so T is the sole
// parameter of the handler's type — whatever syntactic form the argument
// takes: a literal, a method value, or a plain function name.
func handlerEventType(p *packages.Package, call *ast.CallExpr) string {
	tv, ok := p.TypesInfo.Types[call.Args[1]]
	if !ok || tv.Type == nil {
		return ""
	}
	sig, ok := tv.Type.Underlying().(*types.Signature)
	if !ok || sig.Params().Len() != 1 {
		return ""
	}
	named, ok := sig.Params().At(0).Type().(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj.Pkg() == nil || !strings.HasSuffix(obj.Pkg().Path(), "/pkg/hmevent") {
		return ""
	}
	return obj.Name()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrings(m map[string]bool) []string {
	return sortedKeys(m)
}

// consumerClaimPattern matches doc prose that asserts consumers exist:
// plural consumer nouns and "subscribes / listens to this" forms. The
// negated singular forms the declarations use ("no subscriber",
// "nothing consumes this bus event") deliberately do not match.
var consumerClaimPattern = regexp.MustCompile(
	`(?i)\b(subscribers|consumers|listeners)\b|\bconsumed by\b|\bsubscribes? to this\b|\blistens? to this\b`,
)

// TestDeclaredSilentEventDocsClaimNoConsumers cross-checks the two
// truths this package keeps about an event: eventsWithoutSubscriber
// declares that nothing consumes it, while the catalogue's doc comment
// tells a reader what it is for. The two have contradicted each other
// in practice — catalogue comments asserted "MQTT subscribers listen to
// this", "audit loggers consume this event" and "North-bound
// subscribers can use this" for events this very package declared
// consumerless. A comment naming a consumer is a hypothesis, and for a
// declared-silent event it is a refuted one.
//
// The doc of a declared-silent event (its struct and its EventType
// constant) must not claim consumers. When the event gains a real
// subscriber, the ratchet entry falls away and the claim becomes legal
// — and is then checked by nothing weaker than the subscriber itself.
func TestDeclaredSilentEventDocsClaimNoConsumers(t *testing.T) {
	t.Parallel()
	pkgs := loadProductionPackages(t)
	docs := hmeventDeclDocs(pkgs)
	if len(docs) == 0 {
		t.Fatal("no doc comments collected from pkg/hmevent; the walk is broken and this test would pass vacuously")
	}
	for name, reason := range eventsWithoutSubscriber {
		for _, ident := range []string{name, "EventType" + strings.TrimSuffix(name, "Event")} {
			doc, ok := docs[ident]
			if !ok {
				continue
			}
			if m := consumerClaimPattern.FindString(doc); m != "" {
				t.Errorf("%s is declared consumerless (%q) but the doc of %s claims consumers (matched %q) — "+
					"wire the consumer and drop the ratchet entry, or state the silence in the doc",
					name, reason, ident, m)
			}
		}
	}
}

// hmeventDeclDocs collects the doc comment of every type and const
// declaration in pkg/hmevent, keyed by declared identifier.
func hmeventDeclDocs(pkgs []*packages.Package) map[string]string {
	out := map[string]string{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !strings.HasSuffix(p.PkgPath, "/pkg/hmevent") {
			return
		}
		for _, f := range p.Syntax {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gd.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						doc := s.Doc
						if doc == nil {
							doc = gd.Doc
						}
						if doc != nil {
							out[s.Name.Name] = doc.Text()
						}
					case *ast.ValueSpec:
						if s.Doc == nil {
							continue
						}
						for _, n := range s.Names {
							out[n.Name] = s.Doc.Text()
						}
					}
				}
			}
		}
	})
	return out
}
