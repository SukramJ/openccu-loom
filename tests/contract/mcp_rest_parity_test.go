// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMCPCatalogueCoversEveryRESTDomain is the parity guard ADR 0025
// requires: "the tool catalogue must track the REST/WS surface".
//
// It exists because the guard that shipped with the adapter did not do
// this. `TestMCPWriteToolsGatedByAllowWrites` pins the write posture and
// `TestMCPToolNamingTaxonomy` pins the naming — neither compares the
// catalogue to anything. In the two months after the adapter landed,
// eighteen REST domains were built and not one MCP tool was added; the
// alarm system alone grew to 35 routes while MCP could read incidents
// and nothing else. No test failed, because none was looking.
//
// The direction matters. ADR 0025 describes the guard one-way — "a tool
// cannot reference a capability that has been removed" — which catches
// orphaned tools. That is the harmless direction: an orphaned tool
// fails loudly when called. The direction that actually drifted is a
// new capability with no tool, which fails silently and forever, so
// this test checks that one.
//
// Parity does not mean one tool per route. MCP is a curated projection:
// an assistant has no use for session cookies or Prometheus scraping.
// Domains that deliberately have no tool are declared in
// [restDomainsWithoutMCPTools] with the reason, so "we decided not to"
// and "nobody looked" cannot wear the same face.
func TestMCPCatalogueCoversEveryRESTDomain(t *testing.T) {
	t.Parallel()

	tools := mcpToolNames(t, fullyWiredMCPDeps())
	if len(tools) == 0 {
		t.Fatal("no tools advertised")
	}

	var missing []string
	for _, domain := range restDomains(t) {
		if reason, declared := restDomainsWithoutMCPTools[domain.name]; declared {
			if reason == "" {
				t.Errorf("domain %q is declared exempt with an empty reason", domain.name)
			}
			continue
		}
		if reason, declared := restDomainsAwaitingMCPTools[domain.name]; declared {
			if reason == "" {
				t.Errorf("domain %q is declared as backlog with an empty reason", domain.name)
			}
			continue
		}
		if !domainHasTool(domain, tools) {
			missing = append(missing, domain.name+" ("+itoa(domain.routes)+" REST routes)")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d REST domain(s) have no MCP tool and no declared exemption:\n  %s\n\n"+
			"Add a tool in internal/north/mcp/, or declare the domain in "+
			"restDomainsWithoutMCPTools with the reason it is not projected.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestMCPExemptionsAreStillReal keeps the ratchet honest: a declared
// exemption for a domain that no longer exists is stale bookkeeping
// that hides the next gap.
func TestMCPExemptionsAreStillReal(t *testing.T) {
	t.Parallel()

	live := map[string]bool{}
	for _, d := range restDomains(t) {
		live[d.name] = true
	}
	for domain := range restDomainsWithoutMCPTools {
		if !live[domain] {
			t.Errorf("domain %q is declared exempt but no longer exists in the router", domain)
		}
	}
	for domain := range restDomainsAwaitingMCPTools {
		if !live[domain] {
			t.Errorf("domain %q is declared as backlog but no longer exists in the router", domain)
		}
	}
	// A domain cannot be both decided-against and pending.
	for domain := range restDomainsAwaitingMCPTools {
		if _, exempt := restDomainsWithoutMCPTools[domain]; exempt {
			t.Errorf("domain %q is in both the exemption and the backlog map", domain)
		}
	}
}

// restDomainsWithoutMCPTools declares the REST domains that are
// deliberately not projected onto MCP, with the reason.
//
// Each entry is a decision, not a backlog item. A domain that merely
// has not been done yet does NOT belong here — it belongs in the
// failure list until a tool exists.
var restDomainsWithoutMCPTools = map[string]string{
	"auth":     "credential exchange; an assistant authenticates through its own token, never by driving the login flow",
	"me":       "the caller's own session identity; MCP callers are tokens, not sessions",
	"sessions": "browser session lifecycle, meaningless to a token-authenticated client",
	"config":   "daemon configuration editing is an operator action with a secret-masking round trip (see CLAUDE.md); an assistant that can rewrite config can lock the operator out of the daemon",
	"metrics":  "Prometheus scrape endpoint; a text exposition format is not a tool surface",
	"ui":       "surface-profile registry for the SPA's own navigation, not a fleet capability",
	"snapshot": "bulk state dump for backup tooling; the per-domain read tools cover the same ground in a shape an assistant can reason about",
	"diagrams": "SPA-side floor-plan editor state, not a fleet capability",
	"install-mode": "pairing window control actuates the radio; deliberately kept off the assistant surface " +
		"until the write posture for physical pairing is designed",
	"users": "account administration; an assistant that can create or delete accounts can lock the operator " +
		"out of the daemon, the same argument that keeps `config` off the surface",
	"setup":   "one-time first-run wizard; there is no fleet to reason about before it completes",
	"admin":   "daemon-level maintenance actions (reload, cache clear) whose blast radius is the daemon itself, not the fleet",
	"i18n":    "translation catalogue for the SPA; static content, not a capability",
	"webhook": "outbound notification configuration — config-shaped, and covered by the `config` argument",
}

// restDomainsAwaitingMCPTools is the declared backlog: domains that
// SHOULD have tools and do not yet.
//
// It is deliberately a second map rather than more entries in
// [restDomainsWithoutMCPTools]. CLAUDE.md keeps `wiringSettersWithoutCaller`
// and `wiringSeamsUnderInvestigation` apart for the same reason —
// merging them would let "we looked and decided against it" and "nobody
// has done it yet" wear the same face, and the second silently becomes
// the first over time.
//
// Entries here are expected to disappear. A new domain that lands in
// neither map fails the test, so the backlog can shrink but never grow
// unnoticed.
var restDomainsAwaitingMCPTools = map[string]string{
	"matter":     "10 routes: bridge status, fabrics, exposure allowlist, commissioning window",
	"groups":     "6 routes: heating-group roster and administration",
	"areas":      "5 routes: operator-defined room groupings",
	"backups":    "5 routes: create / list / download / restore",
	"interfaces": "3 routes: per-interface state and reconnect",
	"history":    "3 routes: recorded measurement series",
	"visibility": "3 routes: the hidden-parameter picker",
	"energy":     "1 route: energy aggregation",
	"hub":        "1 route: hub-level aggregate",
	"links":      "1 route: direct device-to-device links",
	"schedules":  "1 route: the fleet-wide schedule overview",
}

// restDomain is one path prefix of the REST router and how many routes
// it carries.
type restDomain struct {
	name   string
	routes int
}

// restRouteRe matches the router's registration calls, e.g.
// `pr.With(op).Post("/alarm/zones/{id}/arm", ...)`.
var restRouteRe = regexp.MustCompile(`\.(Get|Post|Put|Patch|Delete)\("/([a-z0-9-]+)`)

// restDomains extracts the domain prefixes the REST router mounts.
func restDomains(t *testing.T) []restDomain {
	t.Helper()
	path := filepath.Join(repoRoot(t), "internal", "north", "rest", "router.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	counts := map[string]int{}
	for _, m := range restRouteRe.FindAllStringSubmatch(string(src), -1) {
		counts[m[2]]++
	}
	out := make([]restDomain, 0, len(counts))
	for name, n := range counts {
		out = append(out, restDomain{name: name, routes: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// domainHasTool reports whether any advertised tool covers the domain.
//
// The match is on the tool's noun rather than the whole tool name,
// because a domain legitimately projects under a related word —
// `security` is served by fault tools, `history` by measurement tools.
// Every such word is declared in [mcpDomainAliases].
//
// The comparison is exact, never a substring. A substring match made
// the guard report the alarm domain — 35 REST routes, no control tool
// whatsoever — as covered, because the unrelated `list_alarm_messages`
// contains the word. A guard that answers "covered" for the largest
// gap it exists to find is worse than no guard.
func domainHasTool(d restDomain, tools []string) bool {
	needles := []string{singularise(d.name), d.name}
	if extra, ok := mcpDomainAliases[d.name]; ok {
		needles = append(needles, extra...)
	}
	for _, tool := range tools {
		_, noun, _ := strings.Cut(tool, "_")
		for _, needle := range needles {
			if noun == needle {
				return true
			}
		}
	}
	return false
}

// mcpDomainAliases maps a REST domain onto the tool nouns that serve
// it where the words differ.
var mcpDomainAliases = map[string][]string{
	"devices":          {"device", "channel", "paramset", "datapoint"},
	"security":         {"security_status", "fault", "hazard"},
	"history":          {"measurement"},
	"energy":           {"measurement", "power"},
	"system":           {"system_info", "health"},
	"info":             {"system_info"},
	"diagnostics":      {"health", "diagnostic"},
	"service-messages": {"service_messages"},
	"alarm-messages":   {"alarm_messages"},
	"incidents":        {"incident"},
	"rooms":            {"room"},
	"functions":        {"function"},
	"areas":            {"area"},
	"centrals":         {"central"},
	"visibility":       {"hidden_parameter", "unignore"},
	"alarm":            {"alarm_zone", "alarm_zones", "alarm_state", "triggered_motion"},
	"programs":         {"program"},
	"sysvars":          {"sysvar"},
	"inbox":            {"inbox"},
	"interfaces":       {"interface"},
	"backups":          {"backup"},
	"groups":           {"group"},
	"links":            {"link"},
	"schedules":        {"schedule", "week_profile"},
	"matter":           {"matter_status", "matter_fabric"},
	"users":            {"user"},
	"webhook":          {"webhook"},
	"admin":            {"admin"},
	"setup":            {"setup"},
	"i18n":             {"translation"},
	"hub":              {"hub"},
}

// singularise strips a trailing plural "s" for the noun match.
func singularise(s string) string {
	if strings.HasSuffix(s, "s") && len(s) > 1 {
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// fullyWiredMCPDeps builds the deps with every optional seam present and
// writes allowed, so the catalogue under test is the maximal one. A
// tool missing here is missing everywhere.
func fullyWiredMCPDeps() mcp.Deps {
	return mcp.Deps{
		Centrals:     emptyCentrals{},
		Devices:      emptyDevices{},
		Writer:       mcpNoopWriter{},
		Paramsets:    mcpNoopParamsets{},
		Health:       mcpNoopHealth{},
		Hubs:         mcpNoopHubs{},
		Audit:        mcpParityAuditRecorder{},
		Incidents:    fakeIncidentsReader{},
		Alarm:        mcpParityAlarm{},
		AlarmControl: mcpParityAlarm{},
		Security:     mcpParitySecurity{},
		AllowWrites:  true,
	}
}

// mcpParityAuditRecorder satisfies audit.Recorder so the audit-backed
// tool registers; the catalogue is all this test reads.
type mcpParityAuditRecorder struct{}

func (mcpParityAuditRecorder) Record(audit.Entry) {}

func (mcpParityAuditRecorder) List(int) []audit.Entry { return nil }

// mcpParityAlarm / mcpParitySecurity satisfy the alarm and security
// seams so their tools register. The catalogue is all this test reads,
// so the projections stay empty.
type mcpParityAlarm struct{}

func (mcpParityAlarm) Zones() []engine.ZoneSnapshot { return nil }

func (mcpParityAlarm) TriggeredMotionSensors(string) []engine.TriggeredMotionSensor { return nil }

func (mcpParityAlarm) Arm(context.Context, string, hmenum.AlarmMode) error { return nil }

func (mcpParityAlarm) Disarm(context.Context, string) error { return nil }

func (mcpParityAlarm) ResetMotion(context.Context, string) (reset, failed int) { return 0, 0 }

type mcpParitySecurity struct{}

func (mcpParitySecurity) Snapshot() security.Snapshot { return security.Snapshot{} }
