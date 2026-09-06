// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"maps"
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
		scheduledJobRestartRules(),
		matterRestartRules(),
		matterEndpointRestartRules(),
		restSurfaceRestartRules(),
		restSecurityRestartRules(),
		discoveryRestartRules(),
		northBridgeRestartRules(),
		authRestartRules(),
		alarmRestartRules(),
		southboundRestartRules(),
		persistenceRestartRules(),
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
		// The logger stack — writer format, default level and the per-path
		// level overrides — is built once and installed as the process
		// default. Nothing re-reads this block afterwards: the runtime level
		// control is the diagnostics endpoint, which drives the level
		// registry directly and does not touch the config.
		{
			Path: "logging",
			Fields: []string{
				"logging.level",
				"logging.format",
				"logging.overrides",
			},
			Differs: func(b, e *Config) bool {
				return b.Logging.Level != e.Logging.Level ||
					b.Logging.Format != e.Logging.Format ||
					!maps.Equal(b.Logging.Overrides, e.Logging.Overrides)
			},
		},
		// The daemon-wide default locale is read while the label resolver,
		// the MQTT bridge, the Matter bridge and the alarm/security wiring
		// are constructed, and each keeps the value it was built with — a
		// live-adopted central included, because the orchestrator carries the
		// config snapshot taken at boot.
		{
			Path:    "locale",
			Differs: func(b, e *Config) bool { return b.Locale != e.Locale },
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

// scheduledJobRestartRules covers the periodic jobs the scheduler registers
// once at boot: each captures its cadence (and whether it runs at all) at
// registration time, so an edit lands with the next process, not the next tick.
func scheduledJobRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path: "addon_update.enabled",
			Differs: func(b, e *Config) bool {
				return b.AddonUpdate.PeriodicCheckEnabled() != e.AddonUpdate.PeriodicCheckEnabled()
			},
		},
		{
			Path:    "addon_update.check_interval",
			Differs: func(b, e *Config) bool { return b.AddonUpdate.CheckInterval != e.AddonUpdate.CheckInterval },
		},
		{
			// The archive store is constructed once during bring-up, so a
			// new directory only takes effect on the next boot. Without the
			// rule a save would answer restart_required:false while every
			// backup kept landing in the old place.
			Path:    "backup.dir",
			Differs: func(b, e *Config) bool { return b.Backup.Dir != e.Backup.Dir },
		},
		{
			Path:    "backup.schedule",
			Differs: func(b, e *Config) bool { return b.Backup.Schedule != e.Backup.Schedule },
		},
		{
			Path:    "backup.keep_last",
			Differs: func(b, e *Config) bool { return b.Backup.KeepLast != e.Backup.KeepLast },
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

// matterEndpointRestartRules covers the rest of the bridge's boot-time
// surface: the attestation material read from disk while the root clusters are
// built, the CASE identity handed to the session layer, and the label,
// secondary-channel policy, time-sync opt-in and IPv4 preference frozen into
// the endpoint assembly and the UDP listener.
func matterEndpointRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path: "north.matter.attestation",
			Fields: []string{
				"north.matter.attestation.cd_path",
				"north.matter.attestation.dac_path",
				"north.matter.attestation.dac_key_path",
				"north.matter.attestation.pai_path",
			},
			Differs: func(b, e *Config) bool {
				return b.North.Matter.Attestation != e.North.Matter.Attestation
			},
		},
		{
			Path: "north.matter.case",
			Fields: []string{
				"north.matter.case.fabric_id",
				"north.matter.case.node_id",
			},
			Differs: func(b, e *Config) bool {
				return b.North.Matter.CASE != e.North.Matter.CASE
			},
		},
		{
			// Defaulted view: the empty label resolves to the compiled-in
			// name, so unset → that same name is not a change.
			Path: "north.matter.node_label",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.WithDefaults().NodeLabel != e.North.Matter.WithDefaults().NodeLabel
			},
		},
		{
			Path: "north.matter.expose_secondary_channels",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.ExposeSecondaryChannels != e.North.Matter.ExposeSecondaryChannels
			},
		},
		{
			// Read once when the bridge is constructed and forwarded into
			// the endpoint assembler, which builds the endpoint set at that
			// moment; a controller's view of the fleet only changes when
			// the assembler runs again.
			Path: "north.matter.include_measurements",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.IncludeMeasurements != e.North.Matter.IncludeMeasurements
			},
		},
		{
			Path: "north.matter.enable_time_sync",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.TimeSyncEnabled() != e.North.Matter.TimeSyncEnabled()
			},
		},
		{
			Path:    "north.matter.prefer_ipv4",
			Differs: func(b, e *Config) bool { return b.North.Matter.PreferIPv4 != e.North.Matter.PreferIPv4 },
		},
		{
			Path: "north.matter.dev_rotate_unique_ids",
			Differs: func(b, e *Config) bool {
				return b.North.Matter.DevRotateUniqueIDs != e.North.Matter.DevRotateUniqueIDs
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
		// Whether the REST + WebSocket surface is mounted at all is decided
		// once, when the listener is assembled.
		{
			Path: "north.rest.enabled",
			Differs: func(b, e *Config) bool {
				return b.North.REST.IsEnabled() != e.North.REST.IsEnabled()
			},
		},
		// The OpenAPI document is read from this path while the validator is
		// built, so pointing at a different spec needs a restart even when
		// validation itself stays on.
		{
			Path:    "north.rest.openapi_spec_path",
			Differs: func(b, e *Config) bool { return b.North.REST.OpenAPISpecPath != e.North.REST.OpenAPISpecPath },
		},
		// The span exporter is constructed during shared-infrastructure
		// wiring; an endpoint added later collects nothing.
		{
			Path: "north.rest.tracing.otlp_endpoint",
			Differs: func(b, e *Config) bool {
				return b.North.REST.Tracing.OTLPEndpoint != e.North.REST.Tracing.OTLPEndpoint
			},
		},
		// The WebSocket replay ring is allocated with this capacity when the
		// hub is created.
		{
			Path: "north.rest.ws.replay_capacity",
			Differs: func(b, e *Config) bool {
				return b.North.REST.WS.ReplayCapacity != e.North.REST.WS.ReplayCapacity
			},
		},
	}
}

// restSecurityRestartRules covers the two REST-side security controls that are
// decided at router assembly. Both used to report "saved" for a change that
// never happened, which is worse than an inert tuning knob: the operator
// believes a control is protecting the surface while it is not.
func restSecurityRestartRules() []RestartRule {
	return []RestartRule{
		// The rate limiter is built from the whole block while the router is
		// assembled, and a disabled block installs no middleware at all — the
		// same shape as the CORS rule. An operator who ticks the limiter on
		// under a brute-force load otherwise gets a success response for a
		// control that never starts gating a single request.
		{
			Path: "north.rest.rate_limit",
			Fields: []string{
				"north.rest.rate_limit.enabled",
				"north.rest.rate_limit.requests_per_second",
				"north.rest.rate_limit.burst",
			},
			Differs: func(b, e *Config) bool {
				return b.North.REST.RateLimit != e.North.REST.RateLimit
			},
		},
		// The certificate RELOADER is built at boot only when both paths are
		// set, and the listener is switched to TLS in the same step. The
		// certificate BYTES are hot-reloaded through that reloader, but the
		// paths themselves — and therefore HTTPS itself — are decided once. A
		// silent no-op here leaves credentials travelling in the clear while
		// the config surface shows TLS configured.
		{
			Path:    "north.rest.tls_cert_file",
			Differs: func(b, e *Config) bool { return b.North.REST.TLSCertFile != e.North.REST.TLSCertFile },
		},
		{
			Path:    "north.rest.tls_key_file",
			Differs: func(b, e *Config) bool { return b.North.REST.TLSKeyFile != e.North.REST.TLSKeyFile },
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
		// The session store is constructed once at boot with the idle
		// timeout it will apply (buildSessionStore in the composition root),
		// so a new value reaches no running store.
		{
			Path: "north.rest.auth.session_idle_timeout",
			Differs: func(b, e *Config) bool {
				return b.North.REST.Auth.SessionIdleTimeout != e.North.REST.Auth.SessionIdleTimeout
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
		// The YAML-declared users and tokens are re-read into an in-memory
		// store on every boot and kept as the login chain's secondary source
		// (buildAuthStores / buildTokenMap in cmd/openccu-loom/daemon_north.go),
		// so an edit reaches nothing until the process restarts. The SQLite
		// stores in front of them ARE live, which is what made this look like a
		// one-time seed — but a credential that exists only in YAML is exactly
		// the case where the distinction matters, and it is the case an
		// operator hits when revoking one.
		{
			Path: "north.rest.auth.users",
			Differs: func(b, e *Config) bool {
				return !maps.Equal(b.North.REST.Auth.Users, e.North.REST.Auth.Users)
			},
		},
		{
			Path: "north.rest.auth.tokens",
			Differs: func(b, e *Config) bool {
				return !maps.Equal(b.North.REST.Auth.Tokens, e.North.REST.Auth.Tokens)
			},
		},
		// The OIDC client — issuer discovery, credentials, redirect URL and
		// the role claim — is constructed once while the router is mounted and
		// then lives for the process, so every field in the block is
		// restart-required. The failure mode is worse than a deferred setting:
		// an operator rotating a leaked client_secret is told the save took
		// effect while the daemon keeps presenting the compromised one.
		{
			Path: "north.rest.auth.oidc",
			Fields: []string{
				"north.rest.auth.oidc.enabled",
				"north.rest.auth.oidc.issuer",
				"north.rest.auth.oidc.client_id",
				"north.rest.auth.oidc.client_secret",
				"north.rest.auth.oidc.redirect_url",
				"north.rest.auth.oidc.role_claim",
			},
			Differs: func(b, e *Config) bool {
				return b.North.REST.Auth.OIDC != e.North.REST.Auth.OIDC
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

// southboundRestartRules covers the config the south-bound bring-up reads
// once: the CCU metadata archives are loaded into the shared catalogues
// during boot, and the reliability tunables are baked into each interface
// client's retry and throttle stack while that client is constructed. A
// central adopted at runtime is no exception — the orchestrator hands out
// the config snapshot it was built with.
func southboundRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path: "ccu_data",
			Fields: []string{
				"ccu_data.translations_path",
				"ccu_data.easymode_path",
			},
			Differs: func(b, e *Config) bool { return b.CCUData != e.CCUData },
		},
		{
			Path: "reliability",
			Fields: []string{
				"reliability.command_retry_initial_delay",
				"reliability.command_throttle_inter_command_delay",
			},
			Differs: func(b, e *Config) bool { return b.Reliability != e.Reliability },
		},
	}
}

// persistenceRestartRules covers the values cache and the measurement-history
// recorder. Both are wired once: the cache's enablement predicate, flush
// ticker and per-central exclusions are captured during south-bound wiring,
// and the recorder copies its whole settings block (retention tiers, flush
// cadence, include/exclude globs, the push exporter and the energy tariff the
// REST energy view renders) into the running instance at construction.
//
// The enablement gates compare their RESOLVED value: "unset" and the explicit
// default mean the same thing to the daemon, and reporting a restart for that
// difference would light the pending-restart banner for a save that changed
// nothing.
func persistenceRestartRules() []RestartRule {
	return []RestartRule{
		{
			Path: "persistence.values_cache",
			Fields: []string{
				"persistence.values_cache.enabled",
				"persistence.values_cache.flush_interval",
				"persistence.values_cache.disabled_centrals",
			},
			Differs: func(b, e *Config) bool {
				bv, ev := b.Persistence.ValuesCache, e.Persistence.ValuesCache
				return orDefault(bv.Enabled, true) != orDefault(ev.Enabled, true) ||
					bv.FlushInterval != ev.FlushInterval ||
					!slices.Equal(bv.DisabledCentrals, ev.DisabledCentrals)
			},
		},
		{
			Path: "persistence.history",
			Fields: []string{
				"persistence.history.enabled",
				"persistence.history.retention",
				"persistence.history.retention_hourly",
				"persistence.history.retention_daily",
				"persistence.history.flush_interval",
				"persistence.history.include",
				"persistence.history.exclude",
				"persistence.history.disabled_centrals",
				"persistence.history.energy_price_per_kwh",
				"persistence.history.energy_currency",
				"persistence.history.export.enabled",
				"persistence.history.export.kind",
				"persistence.history.export.endpoint",
				"persistence.history.export.org",
				"persistence.history.export.bucket",
				"persistence.history.export.token_env",
			},
			Differs: func(b, e *Config) bool {
				return historyDiffers(b.Persistence.History, e.Persistence.History)
			},
		},
	}
}

// historyDiffers compares two history blocks the way the recorder reads them:
// the two enablement gates resolved, everything else by value. Comparing the
// raw structs would flag "unset" against an explicit false as a change.
func historyDiffers(b, e HistoryConfig) bool {
	if b.HistoryFeatureEnabled() != e.HistoryFeatureEnabled() {
		return true
	}
	if b.Retention != e.Retention || b.RetentionHourly != e.RetentionHourly ||
		b.RetentionDaily != e.RetentionDaily || b.FlushInterval != e.FlushInterval {
		return true
	}
	if !slices.Equal(b.Include, e.Include) || !slices.Equal(b.Exclude, e.Exclude) ||
		!slices.Equal(b.DisabledCentrals, e.DisabledCentrals) {
		return true
	}
	if b.EnergyPricePerKWh != e.EnergyPricePerKWh || b.EnergyCurrency != e.EnergyCurrency {
		return true
	}
	if b.Export.ExportEnabled() != e.Export.ExportEnabled() {
		return true
	}
	bx, ex := b.Export, e.Export
	return bx.Kind != ex.Kind || bx.Endpoint != ex.Endpoint || bx.Org != ex.Org ||
		bx.Bucket != ex.Bucket || bx.TokenEnv != ex.TokenEnv
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
