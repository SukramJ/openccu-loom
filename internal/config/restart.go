// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"reflect"
	"slices"
)

// RestartRule describes one config surface that cannot be hot-reloaded.
//
// It carries both halves of the restart contract so they cannot drift:
// Path is what [RestartRequiredDiff] reports when the surface changed, and
// Fields are the config leaves the SPA schema annotates with the
// restart-required badge. A rule whose Fields list is empty annotates Path
// itself.
//
// The two halves used to live in separate hand-maintained lists — the diff
// here and a static map in the REST config handler. They disagreed: the alarm
// block and the Basic/Bearer auth gates carried the badge but were never
// diffed, so a save answered restart_required:false and /restart-pending
// stayed silent while the change sat inert until the next boot.
type RestartRule struct {
	// Path is the identifier reported by [RestartRequiredDiff]. For a
	// block-scoped rule it names the block (e.g. "north.rest.auth.ccu").
	Path string
	// Fields are the config field paths the schema flags restart-required.
	// Empty means the single path {Path}.
	Fields []string
	// Differs reports whether the surface changed between the running
	// config and the freshly assembled one. Comparisons that have a
	// defaulting step compare the defaulted views, so "unset" changing to
	// its own default value is not a phantom restart.
	Differs func(boot, eff *Config) bool
}

// annotatedFields returns the field paths this rule contributes to the
// schema's restart-required badge.
func (r RestartRule) annotatedFields() []string {
	if len(r.Fields) == 0 {
		return []string{r.Path}
	}
	return r.Fields
}

// restartRules is the single source of truth for "changing this needs a
// restart". Both [RestartRequiredDiff] and [RestartRequiredFieldPaths] are
// derived from it; it tracks SPECIFICATION.md §7.1 Q12. The rules are grouped
// per subsystem so each group stays readable next to the wiring it describes.
func restartRules() []RestartRule {
	groups := [][]RestartRule{
		processRestartRules(),
		matterRestartRules(),
		restSurfaceRestartRules(),
		discoveryRestartRules(),
		northBridgeRestartRules(),
		authRestartRules(),
		alarmRestartRules(),
	}
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	out := make([]RestartRule, 0, n)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// processRestartRules covers the process-level surfaces: the data directory,
// the bind addresses and the callback listeners, plus the centrals whose
// lifecycle the orchestrator otherwise handles live.
func processRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path:    "data_dir",
			Differs: func(b, e *Config) bool { return b.DataDir != e.DataDir },
		},
		{
			Path:    "north.rest.listen",
			Differs: func(b, e *Config) bool { return b.North.REST.Listen != e.North.REST.Listen },
		},
		{
			// Bootstrap tier: the first-run probe and the onboarding
			// endpoints bind the value at boot, so flipping it in the YAML
			// of a running daemon must be reported rather than look applied.
			Path: "bootstrap.allow_first_run_setup",
			Differs: func(b, e *Config) bool {
				return b.Bootstrap.FirstRunSetupAllowed() != e.Bootstrap.FirstRunSetupAllowed()
			},
		},
		{
			Path:    "north.rest.public_url",
			Differs: func(b, e *Config) bool { return b.North.REST.PublicURL != e.North.REST.PublicURL },
		},
		{
			Path:    "callback.host",
			Differs: func(b, e *Config) bool { return b.Callback.Host != e.Callback.Host },
		},
		{
			Path:    "callback.port",
			Differs: func(b, e *Config) bool { return b.Callback.Port != e.Callback.Port },
		},
		{
			Path:    "callback.bin_port",
			Differs: func(b, e *Config) bool { return b.Callback.BinPort != e.Callback.BinPort },
		},
		{
			Path:    "callback.port_range",
			Differs: func(b, e *Config) bool { return b.Callback.PortRange != e.Callback.PortRange },
		},
		// The NAT override is read once per central while the callback URL
		// is assembled during south-bound bring-up; a later change never
		// reaches the CCU, which keeps pushing to the old address.
		{
			Path:    "callback.public_host",
			Differs: func(b, e *Config) bool { return b.Callback.PublicHost != e.Callback.PublicHost },
		},
		// The source-IP filter and the connection cap are decided once, at
		// listener construction: an allowlist is only built when the flag is
		// on at bind time, and the cap only wraps the listener when it is
		// positive then. Both listeners keep the wrapper they were born with,
		// so a later change is inert — and a hardening toggle the operator
		// believes is live is worse than one that says "restart to apply".
		{
			Path:    "callback.restrict_source_ips",
			Differs: func(b, e *Config) bool { return b.Callback.RestrictSourceIPs != e.Callback.RestrictSourceIPs },
		},
		{
			Path:    "callback.max_connections",
			Differs: func(b, e *Config) bool { return b.Callback.MaxConnections != e.Callback.MaxConnections },
		},
		// The scheduled-backup job is registered once at boot with the interval +
		// keep-count captured then, so a change takes effect only after a restart.
		{
			Path:    "backup.schedule",
			Differs: func(b, e *Config) bool { return b.Backup.Schedule != e.Backup.Schedule },
		},
		{
			Path:    "backup.keep_last",
			Differs: func(b, e *Config) bool { return b.Backup.KeepLast != e.Backup.KeepLast },
		},
		// Adding or removing a central is a live coordinator-lifecycle
		// operation (the orchestrator adopts/tears down without a restart),
		// so only an in-place modification of a central present in both
		// configs is restart-required. Centrals have their own admin CRUD
		// surface rather than a generic config section, so the schema badge
		// is informational only.
		{
			Path:    "centrals",
			Differs: func(b, e *Config) bool { return CentralsModifiedInPlace(b.Centrals, e.Centrals) },
		},
	}
}

// matterRestartRules covers the Matter bridge, which is constructed once
// during bring-up together with its commissionable mDNS records.
func matterRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path:    "north.matter.enabled",
			Differs: func(b, e *Config) bool { return b.North.Matter.Enabled != e.North.Matter.Enabled },
		},
		{
			Path:    "north.matter.listen",
			Differs: func(b, e *Config) bool { return b.North.Matter.Listen != e.North.Matter.Listen },
		},
		// The bridge's Basic Information cluster and its commissionable mDNS
		// records are built once during bridge bring-up, so the identity and
		// pairing parameters only change on the next boot. A commissioner that
		// already cached the old discriminator otherwise keeps browsing for a
		// record nobody publishes any more.
		{
			Path: "north.matter.vendor_id",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.WithDefaults().VendorID != e.North.Matter.WithDefaults().VendorID
			},
		},
		{
			Path: "north.matter.product_id",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.WithDefaults().ProductID != e.North.Matter.WithDefaults().ProductID
			},
		},
		{
			Path: "north.matter.discriminator",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.WithDefaults().Discriminator != e.North.Matter.WithDefaults().Discriminator
			},
		},
		{
			// The PASE acceptor is constructed from the whole commissioning
			// block at bring-up (verifier derived from passcode + salt +
			// iterations), so every field in it is restart-required.
			Path: "north.matter.commissioning",
			Fields: []string{
				"north.matter.commissioning.passcode",
				"north.matter.commissioning.salt",
				"north.matter.commissioning.iterations",
				"north.matter.commissioning.concurrent_pairings",
				"north.matter.commissioning.ephemeral_window",
			},
			Differs: func(b, e *Config) bool {
				return !reflect.DeepEqual(b.North.Matter.WithDefaults().Commissioning, e.North.Matter.WithDefaults().Commissioning)
			},
		},
		// The advertiser is built once in the bridge bring-up; switching
		// between zeroconf and noop (or from the unset default) only takes
		// effect on the next boot. Compare the defaulted views so unset → the
		// explicit default value does not flag a phantom restart.
		{
			Path: "north.matter.mdns_advertise",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.WithDefaults().MDNSAdvertise != e.North.Matter.WithDefaults().MDNSAdvertise
			},
		},
	}
}

// restSurfaceRestartRules covers the HTTP surfaces whose middleware chain and
// mounts are decided exactly once, while the router is assembled: a middleware
// that was not installed at assembly time cannot start running later, and one
// that was cannot be removed. The same holds for the server-rendered no-JS
// surface, which is either mounted onto the REST listener at boot or not at all.
func restSurfaceRestartRules() []RestartRule {
	return []RestartRule{
		// The double-submit guard and its Secure-cookie flag are both bound
		// when the middleware is installed. Compare the resolved tri-states so
		// unset → the explicit default is not a phantom restart.
		{
			Path: "north.rest.csrf_enabled",
			Differs: func(b, e *Config) bool {
				return b.North.REST.CSRFIsEnabled() != e.North.REST.CSRFIsEnabled()
			},
		},
		{
			Path:    "north.rest.csrf_secure",
			Differs: func(b, e *Config) bool { return b.North.REST.CSRFSecure != e.North.REST.CSRFSecure },
		},
		// The allowed-origin list is captured when the CORS middleware is
		// constructed, during router assembly. An empty list installs no
		// middleware at all, so neither adding the first origin nor
		// removing the last one can take effect in a running daemon.
		{
			Path: "north.rest.cors",
			Differs: func(b, e *Config) bool {
				return !slices.Equal(b.North.REST.CORS, e.North.REST.CORS)
			},
		},
		// The OpenAPI request validator is loaded and wrapped around the
		// router at boot only.
		{
			Path: "north.rest.openapi_validate",
			Differs: func(b, e *Config) bool {
				return b.North.REST.OpenAPIValidateEnabled() != e.North.REST.OpenAPIValidateEnabled()
			},
		},
		// The no-JS diagnostic router (/health, /about) is built during
		// north-bound assembly and mounted onto the REST listener, or skipped.
		{
			Path: "north.ui.enabled",
			Differs: func(b, e *Config) bool {
				return b.North.UI.IsEnabled() != e.North.UI.IsEnabled()
			},
		},
	}
}

// discoveryRestartRules covers the LAN-discovery surfaces (ADR 0021). The mDNS
// advertiser and the SSDP scan loop are each started once during bring-up with
// their parameters captured then, so a later edit leaves the daemon advertising
// (or scanning) exactly as before — the opposite of what an operator who just
// switched LAN visibility off expects.
func discoveryRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path: "north.discovery.mdns.enabled",
			Differs: func(b, e *Config) bool {
				return b.North.Discovery.MDNS.IsEnabled() != e.North.Discovery.MDNS.IsEnabled()
			},
		},
		// The instance name is also the leading component of the wire
		// interface_id (ADR 0024), which the CCU learns at init() time.
		{
			Path: "north.discovery.mdns.instance_name",
			Differs: func(b, e *Config) bool {
				return b.North.Discovery.MDNS.ResolveInstanceName() != e.North.Discovery.MDNS.ResolveInstanceName()
			},
		},
		{
			Path: "north.discovery.ssdp.enabled",
			Differs: func(b, e *Config) bool {
				return b.North.Discovery.SSDP.IsEnabled() != e.North.Discovery.SSDP.IsEnabled()
			},
		},
		{
			Path: "north.discovery.ssdp.interval",
			Differs: func(b, e *Config) bool {
				return b.North.Discovery.SSDP.ResolveInterval() != e.North.Discovery.SSDP.ResolveInterval()
			},
		},
	}
}

// northBridgeRestartRules covers the north-bound bridges that are mounted or
// wired exactly once at boot: the MCP route and the outbound/inbound webhook.
func northBridgeRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path:    "north.mcp.enabled",
			Differs: func(b, e *Config) bool { return b.North.MCP.Enabled != e.North.MCP.Enabled },
		},
		{
			Path:    "north.mcp.allow_writes",
			Differs: func(b, e *Config) bool { return b.North.MCP.AllowWrites != e.North.MCP.AllowWrites },
		},
		{
			Path:    "north.mcp.path",
			Differs: func(b, e *Config) bool { return b.North.MCP.Path != e.North.MCP.Path },
		},
		// The outbound webhook bridge is wired once at boot, so every webhook
		// field is restart-required (mirrors the MCP block above).
		{
			Path:    "north.webhook.enabled",
			Differs: func(b, e *Config) bool { return b.North.Webhook.Enabled != e.North.Webhook.Enabled },
		},
		{
			Path:    "north.webhook.url",
			Differs: func(b, e *Config) bool { return b.North.Webhook.URL != e.North.Webhook.URL },
		},
		{
			Path:    "north.webhook.secret",
			Differs: func(b, e *Config) bool { return b.North.Webhook.Secret != e.North.Webhook.Secret },
		},
		{
			Path: "north.webhook.events",
			Differs: func(b, e *Config) bool {
				return !reflect.DeepEqual(b.North.Webhook.Events, e.North.Webhook.Events)
			},
		},
		{
			Path: "north.webhook.centrals",
			Differs: func(b, e *Config) bool {
				return !reflect.DeepEqual(b.North.Webhook.Centrals, e.North.Webhook.Centrals)
			},
		},
		{
			Path:    "north.webhook.parameter_glob",
			Differs: func(b, e *Config) bool { return b.North.Webhook.ParameterGlob != e.North.Webhook.ParameterGlob },
		},
		{
			Path:    "north.webhook.timeout_ms",
			Differs: func(b, e *Config) bool { return b.North.Webhook.TimeoutMs != e.North.Webhook.TimeoutMs },
		},
		{
			Path:    "north.webhook.inbound.enabled",
			Differs: func(b, e *Config) bool { return b.North.Webhook.Inbound.Enabled != e.North.Webhook.Inbound.Enabled },
		},
		{
			Path:    "north.webhook.inbound.token",
			Differs: func(b, e *Config) bool { return b.North.Webhook.Inbound.Token != e.North.Webhook.Inbound.Token },
		},
	}
}

// authRestartRules covers the login chain, which is assembled once at boot:
// which credential stores are wired in, and which providers take part.
func authRestartRules() []RestartRule {
	return []RestartRule{
		// The login chain (incl. the CCU auth provider) is wired once at
		// boot, so any change to the CCU-auth block is restart-required.
		{
			Path: "north.rest.auth.ccu",
			Fields: []string{
				"north.rest.auth.ccu.enabled",
				"north.rest.auth.ccu.primary",
				"north.rest.auth.ccu.central",
				"north.rest.auth.ccu.min_user_level",
				"north.rest.auth.ccu.role_mapping",
			},
			Differs: func(b, e *Config) bool {
				return !reflect.DeepEqual(b.North.REST.Auth.CCU, e.North.REST.Auth.CCU)
			},
		},
		// The HA Ingress auth-passthrough middleware is also wired once at boot.
		{
			Path: "north.rest.auth.ha_ingress",
			Fields: []string{
				"north.rest.auth.ha_ingress.enabled",
				"north.rest.auth.ha_ingress.trusted_proxy_cidr",
				"north.rest.auth.ha_ingress.role",
			},
			Differs: func(b, e *Config) bool {
				return !reflect.DeepEqual(b.North.REST.Auth.HAIngress, e.North.REST.Auth.HAIngress)
			},
		},
		// The Basic/Bearer scheme gates decide at boot which credential stores
		// are wired into the auth middleware, so toggling either takes effect
		// only after a restart. Compare the resolved tri-state so unset →
		// explicit true is not a phantom restart.
		{
			Path: "north.rest.auth.basic_enabled",
			Differs: func(b, e *Config) bool {
				return b.North.REST.Auth.BasicAuthEnabled() != e.North.REST.Auth.BasicAuthEnabled()
			},
		},
		{
			Path: "north.rest.auth.bearer_enabled",
			Differs: func(b, e *Config) bool {
				return b.North.REST.Auth.BearerAuthEnabled() != e.North.REST.Auth.BearerAuthEnabled()
			},
		},
	}
}

// alarmRestartRules covers the alarm engine and its security wiring, both
// built once at boot, so every field in the section is restart-required.
func alarmRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path: "alarm.enabled",
			Differs: func(b, e *Config) bool {
				return b.Alarm.AlarmEnabled() != e.Alarm.AlarmEnabled()
			},
		},
		{
			Path:    "alarm.default_siren_seconds",
			Differs: func(b, e *Config) bool { return b.Alarm.DefaultSirenSeconds != e.Alarm.DefaultSirenSeconds },
		},
		{
			Path: "alarm.max_acoustic_per_incident_seconds",
			Differs: func(b, e *Config) bool {
				return b.Alarm.MaxAcousticPerIncidentSeconds != e.Alarm.MaxAcousticPerIncidentSeconds
			},
		},
		{
			Path:    "alarm.stop_verify_seconds",
			Differs: func(b, e *Config) bool { return b.Alarm.StopVerifySeconds != e.Alarm.StopVerifySeconds },
		},
		{
			Path:    "alarm.journal_retention_days",
			Differs: func(b, e *Config) bool { return b.Alarm.JournalRetentionDays != e.Alarm.JournalRetentionDays },
		},
		{
			Path:    "alarm.restart_loop_breaker",
			Differs: func(b, e *Config) bool { return b.Alarm.RestartLoopBreaker != e.Alarm.RestartLoopBreaker },
		},
		{
			Path:    "alarm.duress_visibility",
			Differs: func(b, e *Config) bool { return b.Alarm.DuressVisibility != e.Alarm.DuressVisibility },
		},
	}
}

// RestartRules returns the restart contract's rule table. Callers that only
// need the annotated field paths should use [RestartRequiredFieldPaths].
func RestartRules() []RestartRule { return restartRules() }

// RestartRequiredFieldPaths returns every config field path that carries the
// restart-required annotation, derived from the same rules
// [RestartRequiredDiff] evaluates. The REST schema endpoint renders the badge
// from this set, so a field can never be badged without being diffed.
func RestartRequiredFieldPaths() map[string]struct{} {
	rules := restartRules()
	out := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		for _, f := range r.annotatedFields() {
			out[f] = struct{}{}
		}
	}
	return out
}

// RestartRequiredDiff reports the restart-required config paths whose
// value differs between two configs — typically the running (boot)
// config and the freshly assembled persisted config. A non-empty result
// means a saved change is staged but cannot take effect until the daemon
// restarts; an empty result means nothing is pending (so reverting a
// change clears it).
func RestartRequiredDiff(boot, eff *Config) []string {
	if boot == nil || eff == nil {
		return nil
	}
	rules := restartRules()
	out := make([]string, 0, 4)
	for _, r := range rules {
		if r.Differs(boot, eff) {
			out = append(out, r.Path)
		}
	}
	return out
}

// CentralsModifiedInPlace reports whether some central name is present
// in both boot and eff with a differing config. A central present in
// only one of the two slices is a pure add or remove — both are live
// operations now, so neither counts as a modification here.
func CentralsModifiedInPlace(boot, eff []CentralConfig) bool {
	byName := make(map[string]*CentralConfig, len(boot))
	for i := range boot {
		byName[boot[i].Name] = &boot[i]
	}
	for i := range eff {
		next := &eff[i]
		prev, ok := byName[next.Name]
		if !ok {
			continue
		}
		if !reflect.DeepEqual(prev, next) {
			return true
		}
	}
	return false
}
