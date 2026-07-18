// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

// Section is the canonical name of one DB-tier config section.
// Each value maps to a `config_sections.section` primary key and to
// one tab in the SPA Settings surface.
type Section string

const (
	// SectionMQTT carries [config.NorthMQTT].
	SectionMQTT Section = "north.mqtt"
	// SectionMatter carries [config.NorthMatter].
	SectionMatter Section = "north.matter"
	// SectionMCP carries [config.NorthMCP] — the Model Context Protocol
	// server toggle (enabled / allow_writes / path). See ADR 0025.
	SectionMCP Section = "north.mcp"
	// SectionDiscovery carries [config.NorthDiscovery].
	SectionDiscovery Section = "north.discovery"
	// SectionWebhook carries [config.NorthWebhook] — the outbound webhook
	// bridge toggle + endpoint. Restart-required: the bridge is wired once
	// at boot.
	SectionWebhook Section = "north.webhook"
	// SectionREST carries the CORS list + auth toggles +
	// rate-limit block (everything inside [config.NorthREST] except
	// the bind address, which lives in BootstrapConfig).
	SectionREST Section = "north.rest"
	// SectionOIDC carries [config.OIDCConfig].
	SectionOIDC Section = "north.rest.auth.oidc"
	// SectionCCUAuth carries [config.CCUAuthConfig] — the CCU-delegated
	// login provider (ADR 0043). Restart-required: the login chain is
	// wired once at boot.
	SectionCCUAuth Section = "north.rest.auth.ccu"
	// SectionHAIngress carries [config.HAIngressConfig] — the opt-in HA
	// Ingress auth passthrough (ADR 0044). Restart-required: the auth
	// middleware is wired once at boot.
	SectionHAIngress Section = "north.rest.auth.ha_ingress"
	// SectionUI carries [config.NorthUI] (enabled toggle only —
	// bind address is bootstrap).
	SectionUI Section = "north.ui"
	// SectionCallback carries [config.CallbackConfig].
	SectionCallback Section = "callback"
	// SectionCCUData carries [config.CCUDataConfig].
	SectionCCUData Section = "ccu_data"
	// SectionReliability carries [config.ReliabilityConfig].
	SectionReliability Section = "reliability"
	// SectionPersistence carries [config.PersistenceConfig].
	SectionPersistence Section = "persistence"
	// SectionAlarm carries [config.AlarmConfig] — the alarm engine's
	// global settings (docs/alarm-concept.md §14).
	SectionAlarm Section = "alarm"
	// SectionLocale carries the per-daemon default locale (the
	// single field at the top of the legacy YAML).
	SectionLocale Section = "locale"
	// SectionSecurity carries cross-cutting safety toggles, currently
	// just allow_plaintext_secrets (governs whether central rows may
	// store plaintext passwords vs env-var references).
	SectionSecurity Section = "security"
)

// AllSections lists every known section. Used by the schema
// endpoint to enumerate tabs and by the config-export CLI.
func AllSections() []Section {
	return []Section{
		SectionLocale,
		SectionMQTT,
		SectionMatter,
		SectionMCP,
		SectionDiscovery,
		SectionWebhook,
		SectionREST,
		SectionOIDC,
		SectionCCUAuth,
		SectionHAIngress,
		SectionUI,
		SectionCallback,
		SectionCCUData,
		SectionReliability,
		SectionPersistence,
		SectionAlarm,
		SectionSecurity,
	}
}

// SecurityConfig holds the DB-tier security toggles.
type SecurityConfig struct {
	// AllowPlaintextSecrets, when true, permits central rows to
	// store plaintext passwords (password_plain column) instead of
	// env-variable references. Default false — production daemons
	// rely on env-resolution exclusively; this knob exists for
	// test rigs and one-off dev installs.
	AllowPlaintextSecrets bool `json:"allow_plaintext_secrets" cfg:"expert"`
}

// LocaleConfig is the single-field locale section.
type LocaleConfig struct {
	// Locale is the default UI language tag (BCP-47), e.g. "de",
	// "en". Empty defaults to "en".
	Locale string `json:"locale" cfg:"basic"`
}
