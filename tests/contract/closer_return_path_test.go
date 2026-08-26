// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// closer_return_path_test.go pins one half of a recurring defect class:
// a seam that hands back a closer -- func(), or a struct with a Stop/Close
// method -- promising that calling it undoes what the seam attached. Detach
// has repeatedly been less complete than attach (a fabric revoke without
// teardown, an availability cache with no invalidation, an eviction seam
// wired nowhere), and the mechanically decidable half of that promise is:
// does any test ever invoke the closer a seam returns?
//
// A closer no test calls is certainly unguarded -- calling it might already
// be a no-op and nothing would notice. This does not prove the closer's
// *effect* is correct, only that some test exercises the call at all; where a
// seam's test also asserts the effect (its own name says so), that assertion
// is the one that catches a no-op teardown.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// closerSeam is one production function or method that hands back a
// teardown closer for per-central (or per-process) lifecycle wiring.
type closerSeam struct {
	// name labels the seam in test output and keys closerSeamsUnderInvestigation.
	name string
	// declFile is where the closer-returning function is declared, repo-root
	// relative -- used only to make a failure message locate the seam.
	declFile string
	// attachIdent is the identifier a call site uses to produce the closer:
	// a function name, or a method's Sel name.
	attachIdent string
	// invokeIdent, when set, is the identifier ultimately invoked to run the
	// closer -- a variable name, a method Sel name, or (for the WebSocket
	// subscriber family) the lowercase field name the test drives. It is
	// checked independently of attachIdent: some closers surface through an
	// intermediate wrapper (Start()) rather than the return value the
	// composition root captures directly, so proving the seam's own
	// identifier is called and its stop identifier is invoked, anywhere in
	// the file, is the honest claim this check can make -- not that they are
	// provably the same closer, which needs full data-flow this scan does
	// not have.
	//
	// When empty, the closer must be provably the one attachIdent's call
	// returns: assigned to a variable that is later invoked (bare call,
	// deferred call, or handed to t.Cleanup), or the return chained straight
	// into a call (attachIdent(...)()).
	invokeIdent string
	// testFiles are searched, repo-root relative, for the invocation.
	testFiles []string
}

// closerSeams is the reviewed population: every production seam under
// internal/ and pkg/ (outside internal/model/, whose per-listener On*/
// Subscribe closers are a different, ubiquitous pattern exercised by
// construction in every model test) that hands back a bare func() or a
// Stop-bearing struct for central or process lifecycle wiring. Compiled by
// grepping `) func() {` production declarations and keeping the ones a
// composition root actually calls.
var closerSeams = []closerSeam{
	{
		name:        "adapter.WireReadinessRecompute",
		declFile:    "internal/central/adapter/ccu_wiring.go",
		attachIdent: "WireReadinessRecompute",
		testFiles:   []string{"internal/central/adapter/ccu_wiring_readiness_recompute_test.go"},
	},
	{
		name:        "adapter.WireDeviceAvailability",
		declFile:    "internal/central/adapter/device_availability.go",
		attachIdent: "WireDeviceAvailability",
		testFiles:   []string{"internal/central/adapter/device_availability_test.go"},
	},
	{
		name:        "adapter.WireValueSourceLifecycle",
		declFile:    "internal/central/adapter/values_source_lifecycle.go",
		attachIdent: "WireValueSourceLifecycle",
		testFiles:   []string{"tests/integration/values_cache_e2e_test.go"},
	},
	{
		name:        "adapter.WireClimateLinkPeerRefresh",
		declFile:    "internal/central/adapter/climate_link_peer_refresh.go",
		attachIdent: "WireClimateLinkPeerRefresh",
		testFiles:   []string{"internal/central/adapter/climate_link_peer_refresh_test.go"},
	},
	{
		name:        "adapter.WireHealth",
		declFile:    "internal/central/adapter/health_wiring.go",
		attachIdent: "WireHealth",
		testFiles:   []string{"internal/central/adapter/health_wiring_test.go"},
	},
	{
		name:        "central.Unit.WireSessionRecorderPersistence",
		declFile:    "internal/central/central.go",
		attachIdent: "WireSessionRecorderPersistence",
		testFiles:   []string{"internal/central/central_service_hooks_test.go"},
	},
	{
		name:        "history.Recorder.Wire",
		declFile:    "internal/history/recorder.go",
		attachIdent: "Wire",
		testFiles:   []string{"internal/history/recorder_test.go"},
	},
	{
		name:        "history.Recorder.WireCentral",
		declFile:    "internal/history/recorder.go",
		attachIdent: "WireCentral",
		testFiles:   []string{"internal/history/recorder_test.go"},
	},
	{
		// The exported Recorder.StartRetention is only reachable through
		// this composition-root wrapper, which is the seam a test can drive
		// without a full daemon: the wrapper's own closer chains straight
		// into StartRetention's.
		name:        "history.Recorder.StartRetention",
		declFile:    "cmd/openccu-loom/history_wiring.go",
		attachIdent: "wireHistoryRetention",
		testFiles:   []string{"cmd/openccu-loom/history_wiring_test.go"},
	},
	{
		// Same shape: SubscribeCentral is the registry observer
		// SystemStatusBuffer.Subscribe registers, reachable in production
		// only via this composition-root wrapper.
		name:        "handlers.SystemStatusBuffer.Subscribe",
		declFile:    "cmd/openccu-loom/daemon_sysstatus.go",
		attachIdent: "wireSystemStatusSubscribers",
		testFiles:   []string{"cmd/openccu-loom/central_adopt_northbound_test.go"},
	},
	{
		name:        "webhook.Outbound.AttachCentral",
		declFile:    "internal/north/webhook/outbound.go",
		attachIdent: "AttachCentral",
		testFiles:   []string{"internal/north/webhook/outbound_attach_central_test.go"},
	},
	{
		name:        "adapter.ValuesCacheFlusher.Stop",
		declFile:    "internal/central/adapter/values_cache_flush.go",
		attachIdent: "WireValuesCacheFlusher",
		invokeIdent: "Stop",
		testFiles:   []string{"internal/central/adapter/values_cache_gc_test.go"},
	},
	{
		name:        "adapter.ValuesCacheEvictor.StartCentral",
		declFile:    "internal/central/adapter/values_cache_evict.go",
		attachIdent: "StartCentral",
		testFiles:   []string{"internal/central/adapter/values_cache_evict_test.go"},
	},
	{
		name:        "adapter.MasterValuesEvictor.StartCentral",
		declFile:    "internal/central/adapter/master_values_evict.go",
		attachIdent: "StartCentral",
		testFiles:   []string{"internal/central/adapter/master_values_evict_test.go"},
	},
	{
		name:        "adapter.ChannelFlagsEvictor.StartCentral",
		declFile:    "internal/central/adapter/channel_flags_evict.go",
		attachIdent: "StartCentral",
		testFiles:   []string{"internal/central/adapter/channel_flags_evict_test.go"},
	},
	{
		name:        "adapter.EventSourceFeed.StartCentral",
		declFile:    "internal/central/adapter/event_source_feed.go",
		attachIdent: "StartCentral",
		testFiles:   []string{"cmd/openccu-loom/central_adopt_boot_removal_test.go"},
	},
	{
		name:        "mqtt.StartHealthProbe",
		declFile:    "internal/north/mqtt/health_probe.go",
		attachIdent: "StartHealthProbe",
		testFiles:   []string{"internal/north/mqtt/health_probe_test.go"},
	},
	{
		name:        "sqlite.StartHealthProbe",
		declFile:    "internal/store/sqlite/health_probe.go",
		attachIdent: "StartHealthProbe",
		testFiles:   []string{"internal/store/sqlite/health_probe_test.go"},
	},
	{
		name:        "sqlite.StartWALCheckpointLoop",
		declFile:    "internal/store/sqlite/wal_checkpoint.go",
		attachIdent: "StartWALCheckpointLoop",
		testFiles:   []string{"internal/store/sqlite/wal_checkpoint_test.go"},
	},
	{
		name:        "hmlog.WatchSlow",
		declFile:    "pkg/hmlog/slowquery.go",
		attachIdent: "WatchSlow",
		testFiles:   []string{"pkg/hmlog/slowquery_test.go"},
	},
	{
		// The registry-observing WebSocket subscribers wire StartCentral as
		// an *observer* (reg.OnRegister(s.StartCentral)) rather than calling
		// it directly, so the identifier a test actually drives is the
		// public Start/Stop pair -- and subscriber_unsubscribe_ownership_test.go
		// asserts the stronger claim this scan cannot: that Stop removes the
		// exact subscriptions Start (and a runtime adopt) attached.
		name:        "ws.SystemStatusSubscriber.StartCentral",
		declFile:    "internal/north/rest/ws/system_status.go",
		attachIdent: "start",
		invokeIdent: "stop",
		testFiles:   []string{"internal/north/rest/ws/subscriber_unsubscribe_ownership_test.go"},
	},
	{
		name:        "ws.HubEventsSubscriber.StartCentral",
		declFile:    "internal/north/rest/ws/hub_events.go",
		attachIdent: "start",
		invokeIdent: "stop",
		testFiles:   []string{"internal/north/rest/ws/subscriber_unsubscribe_ownership_test.go"},
	},
	{
		name:        "ws.DeviceLifecycleSubscriber.StartCentral",
		declFile:    "internal/north/rest/ws/device_lifecycle.go",
		attachIdent: "start",
		invokeIdent: "stop",
		testFiles:   []string{"internal/north/rest/ws/subscriber_unsubscribe_ownership_test.go"},
	},
	{
		name:        "ws.DeviceTriggerSubscriber.StartCentral",
		declFile:    "internal/north/rest/ws/device_trigger.go",
		attachIdent: "start",
		invokeIdent: "stop",
		testFiles:   []string{"internal/north/rest/ws/subscriber_unsubscribe_ownership_test.go"},
	},
	{
		name:        "ws.OptimisticRollbackSubscriber.StartCentral",
		declFile:    "internal/north/rest/ws/optimistic_rollback.go",
		attachIdent: "start",
		invokeIdent: "stop",
		testFiles:   []string{"internal/north/rest/ws/subscriber_unsubscribe_ownership_test.go"},
	},
}

// closerSeamsUnderInvestigation ratchets a seam this pass found with no test
// invoking the returned closer. Every entry costs real coverage: nothing
// proves calling the closer does what its doc comment promises. Keep this
// map as small as it can honestly be made.
var closerSeamsUnderInvestigation = map[string]string{
	"adapter.EventSourceFeed.StartCentral": "production wiring is correct -- " +
		"cmd/openccu-loom/central_adopt.go stores the returned unwire and replays " +
		"it on removeCentral -- but the only test that reaches the seam " +
		"(cmd/openccu-loom/central_adopt_boot_removal_test.go) substitutes a stub " +
		"hook for it instead of calling the real EventSourceFeed.StartCentral; no " +
		"test proves a runtime-adopted central's device-trigger events reach " +
		"modevent.Source.Fire, or that removing that central detaches the " +
		"subscription. Reported as a finding, not fixed here.",
	"webhook.Outbound.AttachCentral": "outbound_attach_central_test.go calls " +
		"AttachCentral three times and checks only whether the returned detach " +
		"is nil or non-nil -- the closer itself is never invoked, so nothing " +
		"proves calling it drops the webhook subscription it attached. " +
		"Reported as a finding, not fixed here.",
	"sqlite.StartHealthProbe": "health_probe_test.go exercises the internal " +
		"probeOnce helper directly in every case; the exported StartHealthProbe " +
		"-- the ticker loop and its context lifecycle, the part an operator's " +
		"process actually runs -- is never called, so its closer is never " +
		"invoked either. Reported as a finding, not fixed here.",
}

// TestEveryTeardownCloserIsInvokedByATest asserts, for each reviewed seam,
// that at least one of its test files invokes the closer the seam hands
// back.
func TestEveryTeardownCloserIsInvokedByATest(t *testing.T) {
	root := repoRootForHelpers(t)
	for _, seam := range closerSeams {
		t.Run(seam.name, func(t *testing.T) {
			if reason, exempt := closerSeamsUnderInvestigation[seam.name]; exempt {
				t.Skipf("ratcheted (closerSeamsUnderInvestigation): %s", reason)
			}
			for _, tf := range seam.testFiles {
				if seamCoveredInFile(t, filepath.Join(root, tf), seam) {
					return
				}
			}
			t.Errorf(
				"closer return path: %s (declared %s) hands back a closer, but none of %v "+
					"invokes it -- calling it could already be a no-op and no test would notice",
				seam.name, seam.declFile, seam.testFiles,
			)
		})
	}
}

// seamCoveredInFile applies the seam's check to one parsed test file.
func seamCoveredInFile(t *testing.T, testFile string, seam closerSeam) bool {
	t.Helper()
	f := parseTestFile(t, testFile)
	if seam.invokeIdent == "" {
		return closerInvokedInFile(f, seam.attachIdent)
	}
	return callExists(f, seam.attachIdent) && methodOrFuncInvoked(f, seam.invokeIdent)
}

// parseTestFile parses testFile (repo-root-relative caller already joined),
// failing the test with a clear message when it does not exist or does not
// parse -- a seam whose test file was renamed must surface as a broken pin,
// not a silently-vacuous pass.
func parseTestFile(t *testing.T, testFile string) *ast.File {
	t.Helper()
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("closer_return_path: cannot stat %s: %v", testFile, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, testFile, nil, 0)
	if err != nil {
		t.Fatalf("closer_return_path: cannot parse %s: %v", testFile, err)
	}
	return f
}

// closerInvokedInFile reports whether f contains a call to ident whose
// returned closure is itself invoked: assigned to a variable (including one
// slot of a multi-value assignment such as `buf, teardown := wire(...)`,
// where any assigned name being invoked counts -- this scan does not know
// return types) that is later called directly, deferred, or handed to
// t.Cleanup; or called inline as ident(...)().
func closerInvokedInFile(f *ast.File, ident string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if assignInvokesReturnOf(f, stmt, ident) {
				found = true
				return false
			}
		case *ast.DeferStmt:
			// defer ident(...)() -- the deferred call's Fun is itself the
			// matching call, invoked immediately at defer time.
			if inner, ok := stmt.Call.Fun.(*ast.CallExpr); ok && callFunMatches(inner, ident) {
				found = true
				return false
			}
		case *ast.ExprStmt:
			// ident(...)() with no intermediate variable.
			if outer, ok := stmt.X.(*ast.CallExpr); ok {
				if inner, ok := outer.Fun.(*ast.CallExpr); ok && callFunMatches(inner, ident) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// assignInvokesReturnOf reports whether stmt assigns the result of a call
// matching ident and at least one assigned (non-blank) name is later
// invoked. A single-call, multi-name assignment (`a, b := wire(...)`) checks
// every name, since the scan has no type information to know which return
// slot is the closer.
func assignInvokesReturnOf(f *ast.File, stmt *ast.AssignStmt, ident string) bool {
	if len(stmt.Rhs) == 1 {
		call, ok := stmt.Rhs[0].(*ast.CallExpr)
		if !ok || !callFunMatches(call, ident) {
			return false
		}
		for _, lhs := range stmt.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			if varInvoked(f, id.Name) {
				return true
			}
		}
		return false
	}
	for i, rhs := range stmt.Rhs {
		call, ok := rhs.(*ast.CallExpr)
		if !ok || !callFunMatches(call, ident) || i >= len(stmt.Lhs) {
			continue
		}
		id, ok := stmt.Lhs[i].(*ast.Ident)
		if !ok || id.Name == "_" {
			continue
		}
		if varInvoked(f, id.Name) {
			return true
		}
	}
	return false
}

// callExists reports whether f contains any call to ident, as a function or
// as a method selector. Presence only -- test code runs when the test runs,
// so there is no dead-code question the way there is for a production pin.
func callExists(f *ast.File, ident string) bool {
	exists := false
	ast.Inspect(f, func(n ast.Node) bool {
		if exists {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && callFunMatches(call, ident) {
			exists = true
			return false
		}
		return true
	})
	return exists
}

// callFunMatches reports whether call invokes a function or method named
// ident, matched purely by name so an import alias or a differently-typed
// receiver does not defeat the search.
func callFunMatches(call *ast.CallExpr, ident string) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name == ident
	case *ast.SelectorExpr:
		return f.Sel.Name == ident
	}
	return false
}

// varInvoked reports whether varName -- a variable a seam's call assigned
// its result to -- is later invoked in f: a bare call (varName(...)), a
// method call on it as receiver (varName.Any(...), covering a struct whose
// Stop/Close is the closer), or varName handed bare to a *.Cleanup(...)
// call, the idiom the test runner invokes on cleanup.
func varInvoked(f *ast.File, varName string) bool {
	invoked := false
	ast.Inspect(f, func(n ast.Node) bool {
		if invoked {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == varName {
				invoked = true
				return false
			}
		case *ast.SelectorExpr:
			if id, ok := fn.X.(*ast.Ident); ok && id.Name == varName {
				invoked = true
				return false
			}
			if fn.Sel.Name == "Cleanup" {
				for _, arg := range call.Args {
					if id, ok := arg.(*ast.Ident); ok && id.Name == varName {
						invoked = true
						return false
					}
				}
			}
		}
		return true
	})
	return invoked
}

// methodOrFuncInvoked reports whether name -- a function name or a method's
// Sel name -- is called anywhere in f, on any receiver, or handed as a
// receiver.name value (bare or selector) to a *.Cleanup(...) call.
func methodOrFuncInvoked(f *ast.File, name string) bool {
	invoked := false
	ast.Inspect(f, func(n ast.Node) bool {
		if invoked {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == name {
				invoked = true
				return false
			}
		case *ast.SelectorExpr:
			if fn.Sel.Name == name {
				invoked = true
				return false
			}
			if fn.Sel.Name == "Cleanup" {
				for _, arg := range call.Args {
					if sel, ok := arg.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
						invoked = true
						return false
					}
					if id, ok := arg.(*ast.Ident); ok && id.Name == name {
						invoked = true
						return false
					}
				}
			}
		}
		return true
	})
	return invoked
}
