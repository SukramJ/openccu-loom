// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// bridgeServicesWithoutHealthComponent records every north-bound bridge
// service the daemon starts that reports nothing to /health, with the
// reason.
//
// The reason has to answer a specific question: how does an operator find
// out this subsystem is dead? Not "it logs a warning" — a Warn line at boot
// is not a signal anyone is watching three weeks later, and the whole point
// of /health is that something polls it.
var bridgeServicesWithoutHealthComponent = map[string]string{
	"rest": "its verdict cannot travel. rest.Service reports unhealthy only while the REST surface is not serving, which is also the state in which /health cannot be fetched — see the note on bridge.Registry.Health, which has no caller for the same reason",

	"webhook": "still open. webhook.Outbound carries dropped/failed counters and no health component, so an endpoint that stops accepting deliveries is visible only in the log. Recorded in notes/plans/round-7-audit-strategy.md rather than closed here, because deciding what counts as unhealthy for a fire-and-forget sender is a product question",
}

// TestEveryBridgeServiceReportsHealth pins that a subsystem the daemon
// registers as a north-bound bridge either records a /health component or
// is recorded here as deliberately silent.
//
// A bridge service is the shape most able to fail invisibly. It is started
// once at boot, it owns a goroutine, and nothing downstream calls it — so
// when its constructor returns nil on a missing dependency, every surface
// it feeds answers as if the installation simply did not use that feature.
// The Security & Safety domain did exactly that: wireSecurityService
// returned nil on two paths, both log-only, twelve lines from an alarm
// service that had recorded on the tracker since it was written.
func TestEveryBridgeServiceReportsHealth(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	registered := bridgeServiceNames(t, root)
	if len(registered) < 5 {
		t.Fatalf("found only %d registered bridge services — the walk is wrong, and a guard "+
			"that sees too few passes by measuring nothing", len(registered))
	}
	recorded := healthComponentNames(t, root)

	// A service is covered by an exact component name or by a dotted
	// prefix of it: the tracker's naming convention scopes components that
	// way (matter.bridge, startup.<central>, ping_pong/<interface>), so
	// demanding an exact match reported the Matter bridge — which has its
	// own health probe — as uninstrumented.
	covered := func(name string) bool {
		if recorded[name] {
			return true
		}
		for c := range recorded {
			if strings.HasPrefix(c, name+".") {
				return true
			}
		}
		return false
	}

	var missing, stale []string
	for _, name := range registered {
		if covered(name) {
			continue
		}
		if _, known := bridgeServicesWithoutHealthComponent[name]; known {
			continue
		}
		missing = append(missing, name)
	}
	regSet := map[string]bool{}
	for _, n := range registered {
		regSet[n] = true
	}
	for name := range bridgeServicesWithoutHealthComponent {
		switch {
		case !regSet[name]:
			stale = append(stale, name+" (no longer a registered bridge service)")
		case covered(name):
			stale = append(stale, name+" (now records a component — the entry excuses nothing)")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("%d bridge service(s) start with no /health component:\n  %s\n\n"+
			"Nothing polls a log line. Record on the health tracker at the wiring site — "+
			"cmd/openccu-loom/alarm_wiring.go is the shape — or add an entry to "+
			"bridgeServicesWithoutHealthComponent saying how an operator is meant to "+
			"notice this subsystem is dead.",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("%d bridgeServicesWithoutHealthComponent entr(ies) no longer excuse anything:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

var (
	bridgeRegisterRE = regexp.MustCompile(`northBridges\.Register\w*\(\s*(\w+)`)
	healthRecordRE   = regexp.MustCompile(`Record(?:Quality)?\(\s*"([a-z0-9_.]+)"`)
	// A component named through a constant, and the constant's value.
	healthRecordConstRE = regexp.MustCompile(`Record(?:Quality)?\(\s*([A-Za-z_][\w.]*)\s*,`)
	healthConstRE       = regexp.MustCompile(`(?m)^\s*(?:const\s+)?([A-Za-z_]\w*(?:HealthComponent|ComponentName|HealthComponentName))\s*(?:=|\s+\w+\s*=)\s*"([a-z0-9_.]+)"`)
	// wireXService(...) → the service name the daemon knows it by.
	bridgeNameRE = regexp.MustCompile(`^(?:new|wire)?(\w+?)(?:Service|Svc|Outbound|Bridge)?$`)
)

// bridgeServiceNames returns the lowercase names of the services handed to
// northBridges.Register, derived from the identifier at the call site.
func bridgeServiceNames(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	seen := map[string]bool{}
	walkDaemonFiles(t, root, func(src string) {
		for _, m := range bridgeRegisterRE.FindAllStringSubmatch(src, -1) {
			ident := m[1]
			name := strings.ToLower(bridgeNameRE.ReplaceAllString(ident, "$1"))
			name = strings.TrimPrefix(name, "new")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	})
	sort.Strings(out)
	return out
}

// healthComponentNames returns every component name production code
// records on a health tracker.
//
// Constants are resolved, not skipped. Both subsystems that already had a
// component named it through one — mqtt.HealthComponentName and
// securityHealthComponent — so a literal-only match reported the two
// best-instrumented services as uninstrumented. Same class of blind spot
// as the DTO walk's: the write is there, in a shape the reader did not
// parse.
func healthComponentNames(t *testing.T, root string) map[string]bool {
	t.Helper()

	consts := map[string]string{}
	out := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		walkGoTree(t, filepath.Join(root, dir), func(src string) {
			for _, m := range healthConstRE.FindAllStringSubmatch(src, -1) {
				consts[m[1]] = m[2]
			}
		})
	}
	for _, dir := range []string{"cmd", "internal"} {
		walkGoTree(t, filepath.Join(root, dir), func(src string) {
			for _, m := range healthRecordRE.FindAllStringSubmatch(src, -1) {
				out[m[1]] = true
			}
			for _, m := range healthRecordConstRE.FindAllStringSubmatch(src, -1) {
				ident := m[1]
				if i := strings.LastIndex(ident, "."); i >= 0 {
					ident = ident[i+1:]
				}
				if v, ok := consts[ident]; ok {
					out[v] = true
				}
			}
		})
	}
	return out
}

func walkDaemonFiles(t *testing.T, root string, fn func(string)) {
	t.Helper()
	walkGoTree(t, filepath.Join(root, "cmd", "openccu-loom"), fn)
}

func walkGoTree(t *testing.T, dir string, fn func(string)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path) //nolint:gosec // walking a fixed repo subtree
		if rerr != nil {
			// An unreadable file cannot hold a Register or Record call the
			// build would accept, so skipping it cannot hide a service.
			// Failing here would turn a transient read error into a
			// health-coverage finding.
			return nil //nolint:nilerr // an unreadable file carries no reachable call site
		}
		fn(string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}
