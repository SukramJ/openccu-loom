// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// orDefault returns *p, or def when p is nil. Shared by the many
// `cfg:"..."` tri-state `*T` fields in this file whose accessor mirrors
// "not set → default, explicitly set → honour it" (e.g.
// [NorthREST.IsEnabled], [CentralBehavior.LightLastBrightnessEnabled]).
func orDefault[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// InterfaceSpec describes one Homematic interface attached to a central.
// It supports two YAML forms and a mixed form:
//
//	# short form (backwards-compatible)
//	interfaces: [HmIP-RF, BidCos-RF]
//
//	# long form
//	interfaces:
//	  - name: HmIP-RF
//	    port: 12345
//	  - name: BidCos-RF
//	    rpc_type: xmlrpc
//
//	# mixed form
//	interfaces: [HmIP-RF, {name: BidCos-RF, port: 22000}]
//
// Zero values for Port, RemotePath, and RPCType mean "use the backend
// default". The Name field is required and must not be empty.
type InterfaceSpec struct {
	// Name is the Homematic interface identifier, e.g. "HmIP-RF",
	// "BidCos-RF", "VirtualDevices", "CUxD".
	Name string `yaml:"name" json:"name" cfg:"basic"`

	// Port overrides the default XML-RPC/BIN-RPC port for this interface.
	// 0 means "use the backend default derived from the interface type".
	// Valid range: 1-65535 when non-zero.
	Port int `yaml:"port,omitempty" json:"port,omitempty" cfg:"expert"`

	// RemotePath overrides the URL path for XML-RPC requests.
	// "" means "use the backend default" ("/RPC2" for HmIP-RF /
	// BidCos-RF / BidCos-Wired, "/groups" for VirtualDevices).
	// Operators with non-standard CCU routing can pin the value here.
	RemotePath string `yaml:"remote_path,omitempty" json:"remote_path,omitempty" cfg:"expert"`

	// RPCType selects the transport explicitly. Accepted values: "",
	// "xmlrpc", "binrpc". "" means "derive from interface name".
	RPCType string `yaml:"rpc_type,omitempty" json:"rpc_type,omitempty" cfg:"expert"`
}

// UnmarshalYAML implements yaml.Unmarshaler so that InterfaceSpec
// accepts both the short string form ("HmIP-RF") and the long map
// form ({name: HmIP-RF, port: 12345}).
func (s *InterfaceSpec) UnmarshalYAML(value *yaml.Node) error {
	// Short form: scalar string node.
	if value.Kind == yaml.ScalarNode {
		s.Name = value.Value
		return nil
	}
	// Long form: mapping node — decode via a type alias to avoid
	// infinite recursion.
	type plain InterfaceSpec
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	*s = InterfaceSpec(p)
	return nil
}

// Validate returns an error when the spec is not usable.
func (s InterfaceSpec) Validate(idx int) error {
	if s.Name == "" {
		return fmt.Errorf("config: interfaces[%d].name: required", idx)
	}
	if s.Name == "HmIP-Wired" {
		return fmt.Errorf(
			"config: interfaces[%d].name: %q is not a separate CCU interface — "+
				"HmIP-Wired devices are reached through the HmIP-RF interface; "+
				"remove this entry, the device is auto-classified as ProductGroup "+
				"HMIPW via its model name (prefix \"hmipw-\")",
			idx, s.Name,
		)
	}
	if s.Port != 0 && (s.Port < 1 || s.Port > 65535) {
		return fmt.Errorf("config: interfaces[%d].port: out of range 1-65535: %d", idx, s.Port)
	}
	switch s.RPCType {
	case "", "xmlrpc", "binrpc":
		// valid
	default:
		return fmt.Errorf("config: interfaces[%d].rpc_type: invalid value %q (use xmlrpc or binrpc)", idx, s.RPCType)
	}
	return nil
}

// Config is the root daemon configuration.
type Config struct {
	Locale      string            `yaml:"locale" json:"locale" cfg:"basic"`
	DataDir     string            `yaml:"data_dir" json:"data_dir" cfg:"basic"`
	CCUData     CCUDataConfig     `yaml:"ccu_data" json:"ccu_data" cfg:"expert"`
	Logging     LoggingConfig     `yaml:"logging" json:"logging" cfg:"basic"`
	Callback    CallbackConfig    `yaml:"callback" json:"callback" cfg:"expert"`
	North       NorthConfig       `yaml:"north" json:"north" cfg:"basic"`
	Centrals    []CentralConfig   `yaml:"centrals" json:"centrals" cfg:"basic"`
	Reliability ReliabilityConfig `yaml:"reliability,omitempty" json:"reliability,omitzero" cfg:"expert"`
	Persistence PersistenceConfig `yaml:"persistence,omitempty" json:"persistence,omitzero" cfg:"expert"`
	Backup      BackupConfig      `yaml:"backup,omitempty" json:"backup,omitzero" cfg:"expert"`
	Alarm       AlarmConfig       `yaml:"alarm,omitempty" json:"alarm,omitzero" cfg:"expert"`
	AddonUpdate AddonUpdateConfig `yaml:"addon_update,omitempty" json:"addon_update,omitzero" cfg:"expert"`
}

// AddonUpdateConfig configures the CCU add-on self-update checker
// (ADR 0057). Meaningful only on platforms where the capability probe
// passes (add-on build + firmware installer present); elsewhere the
// section has no effect.
type AddonUpdateConfig struct {
	// Enabled gates the background release checking — the boot-delayed
	// check and the recurring one alike (default true). The manual
	// check/install verbs stay available regardless. Tri-state so
	// "unset" and an explicit false are distinguishable, mirroring the
	// other Enabled toggles.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"expert"`
	// CheckInterval is the recurring release-check cadence, layered on
	// top of the unconditional boot check and the manual check/install
	// verbs (ADR 0057 §4). Zero means "use the compiled-in default"
	// (24h, applied in [Config.applyDefaults]); disabling the periodic
	// check is Enabled=false, not a zero interval.
	CheckInterval time.Duration `yaml:"check_interval,omitempty" json:"check_interval,omitzero" cfg:"expert"`
}

// PeriodicCheckEnabled reports the resolved background-check toggle
// (default true).
func (c AddonUpdateConfig) PeriodicCheckEnabled() bool {
	return orDefault(c.Enabled, true)
}

// BackupConfig configures automatic, scheduled CCU backups. Off by default
// (a backup touches the CCU and produces files, so it is opt-in). A change to
// either field is hot-reloaded — it only re-tunes a scheduler job interval.
type BackupConfig struct {
	// Schedule is how often each configured central is backed up
	// automatically. Zero disables scheduled backups (manual backups via the
	// REST/UI surface still work).
	Schedule time.Duration `yaml:"schedule,omitempty" json:"schedule,omitzero" cfg:"expert"`
	// KeepLast bounds the number of scheduled backups retained per central:
	// after a successful backup the oldest beyond this count are pruned. Zero
	// keeps all.
	KeepLast int `yaml:"keep_last,omitempty" json:"keep_last,omitzero" cfg:"expert"`
}

// AlarmConfig configures the alarm engine (docs/alarm-concept.md §14).
// Relational alarm data (areas, sensors, outputs) is first-class
// domain data managed via REST/UI, not config material — this section
// carries only the global engine settings.
type AlarmConfig struct {
	// Enabled starts the alarm service; nil defaults to true (the
	// engine is inert without configured areas, so "on" is safe).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	// DefaultSirenSeconds bounds one acoustic activation when an
	// output does not configure its own duration.
	DefaultSirenSeconds int `yaml:"default_siren_seconds" json:"default_siren_seconds" cfg:"basic"`
	// MaxAcousticPerIncidentSeconds is the cumulative acoustic budget
	// of one incident across all re-triggers and restarts.
	MaxAcousticPerIncidentSeconds int `yaml:"max_acoustic_per_incident_seconds" json:"max_acoustic_per_incident_seconds" cfg:"expert"`
	// StopVerifySeconds bounds how long an unverified siren stop is
	// retried before it becomes a health incident.
	StopVerifySeconds int `yaml:"stop_verify_seconds" json:"stop_verify_seconds" cfg:"expert"`
	// JournalRetentionDays prunes alarm-journal entries; 0 disables
	// retention.
	JournalRetentionDays int `yaml:"journal_retention_days" json:"journal_retention_days" cfg:"basic"`
	// RestartLoopBreaker caps restore-driven output re-fires per
	// incident before degradation to optical and notifications.
	RestartLoopBreaker int `yaml:"restart_loop_breaker" json:"restart_loop_breaker" cfg:"expert"`
	// DuressVisibility bounds where a duress-code use or a silent panic
	// trigger may appear: "hidden", "notify_only" (default) or "full".
	//
	// The threat model is not that Home Assistant is insecure — it is
	// that whoever stands next to you sees the same screen you do. A
	// hallway wall tablet, or a lock-screen banner while the attacker
	// watches, defeats the covert trigger the feature exists for. But an
	// installation that notifies only through Home Assistant and runs no
	// webhook would get no duress notification at all under a
	// hidden-only policy, which is a safety function failing silently.
	// The trade-off therefore belongs to the operator.
	DuressVisibility string `yaml:"duress_visibility" json:"duress_visibility" cfg:"expert"`
}

// AlarmEnabled reports the tri-state Enabled flag with its nil→true
// default.
func (c AlarmConfig) AlarmEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// PersistenceConfig groups the cross-cutting persistence-tuning knobs
// that the daemon's SQLite-backed caches expose to operators. Each
// block defaults to "feature on, sensible defaults"; zero-valued
// fields fall back to the hard-coded constants the wiring uses.
//
// Today only [PersistenceConfig.ValuesCache] is wired through. Future
// caches (e.g. linkprofile snapshots) get their own sub-block here.
type PersistenceConfig struct {
	ValuesCache ValuesCacheConfig `yaml:"values_cache,omitempty" json:"values_cache,omitzero" cfg:"expert"`
	History     HistoryConfig     `yaml:"history,omitempty" json:"history,omitzero" cfg:"expert"`
}

// HistoryConfig configures the opt-in measurement-history recorder
// introduced by ADR 0040. Unlike the VALUES cache, history is OPT-IN:
// Enabled defaults to false (nil), so the recorder, the dedicated
// history.db, and the retention job are only wired when the operator
// turns the feature on.
//
// This block is DB-tier config: it is seeded into the config_sections
// table and editable through the SPA, like persistence.values_cache and
// north.mqtt.
type HistoryConfig struct {
	// Enabled is the master switch. Defaults to false (opt-in). Use
	// *bool so the YAML decoder can distinguish "not set" from
	// "explicitly false".
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"expert"`

	// Retention bounds how long raw samples are kept. Zero falls back to
	// the daemon default (720h / 30 days). The retention job purges rows
	// older than now-Retention.
	Retention time.Duration `yaml:"retention,omitempty" json:"retention,omitempty" cfg:"expert"`

	// RetentionHourly bounds how long the hourly rollup tier
	// (measurements_hourly) is kept. Zero falls back to
	// [RetentionHourlyDefault] (13 months). The rollup job purges hourly
	// rows older than now-RetentionHourly, always after they have been
	// folded into the daily tier.
	RetentionHourly time.Duration `yaml:"retention_hourly,omitempty" json:"retention_hourly,omitempty" cfg:"expert"`

	// RetentionDaily bounds how long the daily rollup tier
	// (measurements_daily) is kept. Zero means keep forever — daily rows
	// are tiny (one row per data point per day), so there is no default
	// expiry.
	RetentionDaily time.Duration `yaml:"retention_daily,omitempty" json:"retention_daily,omitempty" cfg:"expert"`

	// FlushInterval overrides the recorder's batch-flush cadence. Zero
	// falls back to the daemon default (5s).
	FlushInterval time.Duration `yaml:"flush_interval,omitempty" json:"flush_interval,omitempty" cfg:"expert"`

	// Include lists parameter-name globs to record. Empty (default)
	// records every numeric VALUES parameter. A non-empty list records
	// only parameters matching at least one pattern (e.g. "TEMPERATURE",
	// "*POWER*", "ACTUAL_*").
	Include []string `yaml:"include,omitempty" json:"include,omitempty" cfg:"expert"`

	// Exclude lists parameter-name globs to drop. Exclude always wins
	// over Include. Empty (default) excludes nothing.
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty" cfg:"expert"`

	// DisabledCentrals lists central names whose data points must not be
	// recorded. Empty (default) records every enabled central.
	DisabledCentrals []string `yaml:"disabled_centrals,omitempty" json:"disabled_centrals,omitempty" cfg:"expert"`

	// EnergyPricePerKWh is the electricity tariff the energy view uses to
	// show costs next to kWh. Zero (the default) means no tariff is
	// configured and the view omits every cost figure rather than
	// rendering a misleading 0.00.
	EnergyPricePerKWh float64 `yaml:"energy_price_per_kwh,omitempty" json:"energy_price_per_kwh,omitempty" cfg:"basic"`

	// EnergyCurrency labels the amounts derived from EnergyPricePerKWh.
	// Free text so any symbol or ISO code works; empty falls back to the
	// euro sign. It is a label only - no conversion happens anywhere.
	EnergyCurrency string `yaml:"energy_currency,omitempty" json:"energy_currency,omitempty" cfg:"basic"`

	// Export configures the opt-in push exporter that forwards each
	// recorded sample to an external time-series store (ADR 0040). The
	// embedded history.db stays the default surface; this is additive.
	Export HistoryExportConfig `yaml:"export,omitempty" json:"export,omitzero" cfg:"expert"`
}

// HistoryExportConfig configures the opt-in measurement-history push
// exporter. Default off. The shipped backend speaks InfluxDB line
// protocol; the seam allows other backends later (ADR 0040).
//
// The access token is a secret and is read from the named environment
// variable (TokenEnv), never stored inline in config (ADR 0027).
type HistoryExportConfig struct {
	// Enabled turns the exporter on. Defaults to false.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"expert"`

	// Kind selects the backend. Empty or "influxdb" = InfluxDB line
	// protocol (the only backend today).
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty" cfg:"expert"`

	// Endpoint is the base URL of the target (e.g. http://influx:8086).
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty" cfg:"expert"`

	// Org and Bucket are the InfluxDB v2 write target.
	Org    string `yaml:"org,omitempty" json:"org,omitempty" cfg:"expert"`
	Bucket string `yaml:"bucket,omitempty" json:"bucket,omitempty" cfg:"expert"`

	// TokenEnv names the environment variable that holds the write
	// token. The daemon reads os.Getenv(TokenEnv) at wiring time.
	TokenEnv string `yaml:"token_env,omitempty" json:"token_env,omitempty" cfg:"expert"`
}

// ExportEnabled reports whether the push exporter should be wired.
func (c HistoryExportConfig) ExportEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

// HistoryEnabled reports whether the history recorder should be wired
// for the named central. True only when the feature is explicitly
// enabled AND the central is not in DisabledCentrals.
func (c HistoryConfig) HistoryEnabled(centralName string) bool {
	if c.Enabled == nil || !*c.Enabled {
		return false
	}
	return !slices.Contains(c.DisabledCentrals, centralName)
}

// HistoryFeatureEnabled reports whether the feature is on at all
// (independent of any single central). Used to decide whether to open
// history.db and wire the retention job.
func (c HistoryConfig) HistoryFeatureEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

// RetentionHourlyDefault is how long the hourly rollup tier is kept when
// the operator did not override it: 13 months, chosen so a full year of
// hourly resolution survives even a slightly late rollout of the daily
// tier's consumers (e.g. the energy view's month-over-month comparisons).
const RetentionHourlyDefault = 13 * 30 * 24 * time.Hour

// HistoryRetentionFloor is the smallest raw-sample retention the daemon
// accepts. It mirrors the recorder's hourly-rollup lag (one hour): a
// retention below it would let the retention purge delete raw rows before
// the hourly fold has folded them, permanently losing that data. An
// explicit value below this floor is clamped up to it at config load; zero
// still means "use the daemon default", which is far above the floor.
const HistoryRetentionFloor = time.Hour

// RetentionHourlyOrDefault returns RetentionHourly, falling back to
// [RetentionHourlyDefault] when unset.
func (c HistoryConfig) RetentionHourlyOrDefault() time.Duration {
	if c.RetentionHourly > 0 {
		return c.RetentionHourly
	}
	return RetentionHourlyDefault
}

// RetentionDailyOrDefault returns RetentionDaily. Unlike the raw and
// hourly tiers, zero is a genuine value here (keep daily rows forever),
// not a "use the default" sentinel — so this is a passthrough kept for
// symmetry with [HistoryConfig.RetentionHourlyOrDefault] at call sites.
func (c HistoryConfig) RetentionDailyOrDefault() time.Duration {
	return c.RetentionDaily
}

// ValuesCacheConfig overrides the defaults baked into the wire-DP
// VALUES persistence introduced by ADR 0019.
//
// All fields are optional. A zero value means "use the daemon
// default" — currently:
//   - Enabled:          true  (opt-out, not opt-in)
//   - FlushInterval:    60 s
//   - DisabledCentrals: empty (every central writes to the cache)
type ValuesCacheConfig struct {
	// Enabled is the master switch. Set to false to skip wiring the
	// store, the restore pass and the periodic flusher entirely.
	// Defaults to true (opt-out). Use *bool so the YAML decoder can
	// distinguish "not set" from "explicitly false".
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"expert"`

	// FlushInterval overrides the periodic-flush cadence. Zero falls
	// back to [adapter.DefaultValuesCacheFlushInterval] (60 s).
	FlushInterval time.Duration `yaml:"flush_interval,omitempty" json:"flush_interval,omitempty" cfg:"expert"`

	// DisabledCentrals lists central names whose data points must be
	// kept out of the cache entirely. Useful for test rigs in a
	// multi-CCU deployment where one central is non-production.
	// Empty list (default) caches every central.
	DisabledCentrals []string `yaml:"disabled_centrals,omitempty" json:"disabled_centrals,omitempty" cfg:"expert"`
}

// ValuesCacheEnabled reports whether the values cache should be wired
// for the central. Returns true when:
//   - the feature is enabled (default true), AND
//   - the central is NOT listed in DisabledCentrals.
func (c ValuesCacheConfig) ValuesCacheEnabled(centralName string) bool {
	if c.Enabled != nil && !*c.Enabled {
		return false
	}
	return !slices.Contains(c.DisabledCentrals, centralName)
}

// ReliabilityConfig overrides reliability-stack defaults. All fields
// default to the openccu-loom Go-idiomatic values when zero; set them
// Explicitly to pin behaviour.
//
// References for the
//   - command_retry_base_delay = 2.0s prod / 0.1s test
//   - command_throttle_interval = 0.0 (disabled by default)
type ReliabilityConfig struct {
	// CommandRetryInitialDelay overrides the first backoff delay in
	// [reliability.RetryConfig.Initial]. Zero (default) keeps the
	// OpenCCU-Loom 250 ms default. Set to 2s to match
	// Production behaviour, or 100 ms for the
	CommandRetryInitialDelay time.Duration `yaml:"command_retry_initial_delay,omitempty" json:"command_retry_initial_delay,omitempty" cfg:"expert"`

	// CommandThrottleInterCommandDelay overrides the minimum gap
	// between consecutive throttled commands. Zero (default) leaves the
	// openccu-loom throttle in "no inter-command pacing" mode. Set to
	// e.g. 50ms or 500ms to pin a specific gap on heavily-loaded
	// BidCos-RF interfaces.
	CommandThrottleInterCommandDelay time.Duration `yaml:"command_throttle_inter_command_delay,omitempty" json:"command_throttle_inter_command_delay,omitempty" cfg:"expert"`
}

// CCUDataConfig locates the archives produced
// extract scripts. Both paths are optional; graceful fallback is
// raw parameter / model names in the UI.
type CCUDataConfig struct {
	TranslationsPath string `yaml:"translations_path" json:"translations_path" cfg:"expert"`
	EasymodePath     string `yaml:"easymode_path" json:"easymode_path" cfg:"expert"`
}

// LoggingConfig controls structured logging.
//
// Overrides maps a dot-separated logger subsystem path
// (e.g. `openccu-loom.client.transport.xmlrpc`) to one of
// debug|info|warn|error. Static config-time overrides are useful for
// reproducing a known-noisy subsystem at boot; the diagnostics REST
// endpoint installs additional runtime overrides on top, with a TTL,
// without touching the YAML file.
type LoggingConfig struct {
	Level     string            `yaml:"level" json:"level" cfg:"basic"`   // debug|info|warn|error
	Format    string            `yaml:"format" json:"format" cfg:"basic"` // json|text
	Overrides map[string]string `yaml:"overrides,omitempty" json:"overrides,omitempty" cfg:"expert"`
}

// CallbackConfig governs the XML-RPC + BIN-RPC callback servers.
//
// PortRange, when set, takes precedence over Port for the XML-RPC
// listener. The precedence is what makes the range reachable at all:
// [Config.applyDefaults] fills Port with 8120, so a rule of "range
// applies only when Port is 0" could never fire once defaults had run —
// which is on every boot, and again on every DB-tier overlay.
type CallbackConfig struct {
	Host    string `yaml:"host" json:"host" cfg:"expert"`
	Port    int    `yaml:"port" json:"port" cfg:"expert"`         // XML-RPC; ignored when PortRange is set
	BinPort int    `yaml:"bin_port" json:"bin_port" cfg:"expert"` // BIN-RPC; 0 = dynamic
	// PortRange bounds the XML-RPC listener to "<lo>-<hi>" (e.g.
	// "30000-30099"); the server binds the first free port in it. Empty
	// leaves Port in charge.
	PortRange         string `yaml:"port_range" json:"port_range" cfg:"expert"`
	PublicHost        string `yaml:"public_host" json:"public_host" cfg:"expert"`                 // optional NAT override
	MaxConnections    int    `yaml:"max_connections" json:"max_connections" cfg:"expert"`         // per-listener concurrent-connection cap; 0 = default (64)
	RestrictSourceIPs bool   `yaml:"restrict_source_ips" json:"restrict_source_ips" cfg:"expert"` // only accept callbacks from configured CCU IPs (+loopback)
}

// NorthConfig bundles north-bound server settings.
type NorthConfig struct {
	REST      NorthREST      `yaml:"rest" json:"rest" cfg:"basic"`
	UI        NorthUI        `yaml:"ui" json:"ui" cfg:"basic"`
	MQTT      NorthMQTT      `yaml:"mqtt" json:"mqtt" cfg:"basic"`
	Matter    NorthMatter    `yaml:"matter" json:"matter" cfg:"basic"`
	MCP       NorthMCP       `yaml:"mcp" json:"mcp" cfg:"basic"`
	Discovery NorthDiscovery `yaml:"discovery" json:"discovery" cfg:"basic"`
	Webhook   NorthWebhook   `yaml:"webhook" json:"webhook" cfg:"basic"`
}

// NorthWebhook configures the outbound webhook bridge — a north-bound
// adapter that POSTs a signed JSON payload to an operator-configured URL on
// datapoint, system-status and incident events. Disabled by default.
//
// A single endpoint is supported. Multi-endpoint fan-out is a planned
// follow-up: it requires masking secret-class values inside list elements
// and a stable element identity across config edits, neither of which the
// current secret round-trip handles.
type NorthWebhook struct {
	// Enabled is the feature flag. Default false; while disabled the daemon
	// subscribes to no event bus and POSTs nothing. Changing it is
	// restart-required (the bridge is wired once at boot).
	Enabled bool `yaml:"enabled" json:"enabled" cfg:"basic"`

	// URL is the absolute http(s) endpoint the bridge POSTs each event to.
	// Empty disables delivery even when Enabled is true.
	URL string `yaml:"url" json:"url" cfg:"basic"`

	// Secret is the shared key for the HMAC-SHA256 body signature sent in
	// the X-OpenCCU-Signature header. Empty means the bridge sends no
	// signature header (receiver cannot verify authenticity).
	Secret string `yaml:"secret" json:"secret" cfg:"secret"`

	// Events is an allowlist of event-type tags to deliver (e.g.
	// "datapoint.value_changed"). Empty means all supported events.
	Events []string `yaml:"events" json:"events" cfg:"basic"`

	// Centrals is an allowlist of central names to deliver events for.
	// Empty means all centrals.
	Centrals []string `yaml:"centrals" json:"centrals" cfg:"basic"`

	// ParameterGlob, when set, restricts datapoint events to those whose
	// parameter name matches the glob (e.g. "*TEMPERATURE*"). Empty means
	// no parameter filter. Non-datapoint events are unaffected.
	ParameterGlob string `yaml:"parameter_glob" json:"parameter_glob" cfg:"expert"`

	// TimeoutMs is the per-delivery HTTP timeout in milliseconds. Zero
	// selects the 10s default; a negative value is rejected by
	// [Config.Validate] rather than silently falling back to it.
	TimeoutMs int `yaml:"timeout_ms" json:"timeout_ms" cfg:"expert"`

	// Inbound is the reverse direction: a REST surface external systems POST
	// to in order to set a datapoint value or trigger a program. Disabled by
	// default and independent of the outbound endpoint above.
	Inbound NorthWebhookInbound `yaml:"inbound" json:"inbound" cfg:"basic"`
}

// Timeout returns the configured per-delivery timeout, or the 10s default
// when TimeoutMs is unset (zero or negative).
func (w NorthWebhook) Timeout() time.Duration {
	if w.TimeoutMs <= 0 {
		return 10 * time.Second
	}
	return time.Duration(w.TimeoutMs) * time.Millisecond
}

// NorthWebhookInbound configures the inbound webhook REST surface — the
// endpoints external systems POST to in order to set a datapoint value
// (`POST /api/v1/webhook/value`) or trigger a program
// (`POST /api/v1/webhook/program`). These are real device writes / program
// runs, so they carry the same authorization weight as the equivalent REST
// calls. Disabled by default; the routes are mounted only when Enabled, so
// toggling it is restart-required (mirrors the outbound bridge).
type NorthWebhookInbound struct {
	// Enabled is the feature flag. Default false; while disabled the daemon
	// mounts no inbound webhook route (404).
	Enabled bool `yaml:"enabled" json:"enabled" cfg:"basic"`

	// Token is an optional inbound-specific bearer token accepted in addition
	// to the normal REST auth chain, so a header-only caller (e.g. a doorbell)
	// can POST without a session or user token. Empty means only the normal
	// auth chain applies. Sent as `Authorization: Bearer <token>`.
	Token string `yaml:"token" json:"token" cfg:"secret"`
}

// NorthMCP configures the Model Context Protocol server — a north-bound
// adapter that exposes the domain to LLM agents as tools over a
// Streamable-HTTP transport. Disabled by default; read-only even when
// enabled until AllowWrites is also set. See ADR 0025.
type NorthMCP struct {
	// Enabled is the feature flag. Default false; flip explicitly to
	// expose the MCP server. While disabled the daemon registers no MCP
	// route and advertises no mcp.* capability.
	Enabled bool `yaml:"enabled" json:"enabled" cfg:"basic"`

	// AllowWrites gates the write-capable tools (set_datapoint, …).
	// Default false: enabling the adapter alone yields a read-only
	// surface. An operator who wants agent-driven control opts in twice.
	AllowWrites bool `yaml:"allow_writes" json:"allow_writes" cfg:"basic"`

	// Path is the HTTP mount path for the Streamable-HTTP transport,
	// served on the existing REST listener. Defaults to "/mcp".
	Path string `yaml:"path" json:"path" cfg:"expert"`
}

// MountPath returns the configured MCP mount path, or the "/mcp"
// default when unset.
func (m NorthMCP) MountPath() string {
	if m.Path == "" {
		return "/mcp"
	}
	return m.Path
}

// NorthDiscovery groups LAN-discovery surfaces external clients use
// to locate the daemon without manual configuration. See ADR 0021.
type NorthDiscovery struct {
	MDNS NorthDiscoveryMDNS `yaml:"mdns" json:"mdns" cfg:"basic"`
	SSDP NorthDiscoverySSDP `yaml:"ssdp" json:"ssdp" cfg:"basic"`
}

// NorthDiscoverySSDP configures active SSDP / UPnP discovery of Homematic /
// OpenCCU central units on the LAN. When enabled the daemon periodically
// multicasts an M-SEARCH, follows each responder's `basic_dev.cgi`, and
// surfaces matching CCUs in the UI so the operator can adopt or ignore them.
//
// Default is enabled: it only reads the network (a multicast probe — no data
// about the daemon leaves the LAN) and simply finds nothing when multicast is
// unavailable (e.g. some container network setups).
type NorthDiscoverySSDP struct {
	// Enabled toggles the discovery scan. Defaults to true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	// Interval is how often the daemon re-runs the M-SEARCH scan. Zero / unset
	// falls back to 60s.
	Interval time.Duration `yaml:"interval" json:"interval" cfg:"basic"`
}

// IsEnabled reports whether SSDP discovery is on, applying the default-true
// policy when the pointer is nil.
func (s NorthDiscoverySSDP) IsEnabled() bool {
	return orDefault(s.Enabled, true)
}

// ResolveInterval returns the configured scan interval, defaulting to 60s.
func (s NorthDiscoverySSDP) ResolveInterval() time.Duration {
	if s.Interval <= 0 {
		return 60 * time.Second
	}
	return s.Interval
}

// NorthDiscoveryMDNS configures the daemon's self-advertisement on
// the local network via mDNS / Zeroconf. When enabled, the daemon
// publishes one `_openccu-loom._tcp.local.` record whose port mirrors
// `North.REST.Listen` so Home Assistant (and other zeroconf-aware
// clients) can auto-discover the instance.
//
// Default is enabled — opt-out is intended for security-sensitive
// deployments where LAN visibility of the daemon is unwanted.
type NorthDiscoveryMDNS struct {
	// Enabled toggles the advertisement. Defaults to true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	// InstanceName is the leftmost label of the SRV / TXT record
	// (`<instance>._openccu-loom._tcp.local.`). Empty falls back to
	// the OS hostname (with any `.local` suffix stripped).
	InstanceName string `yaml:"instance_name" json:"instance_name" cfg:"basic"`
}

// IsEnabled reports whether mDNS advertisement is on, applying the
// default-true policy when the pointer is nil.
func (m NorthDiscoveryMDNS) IsEnabled() bool {
	return orDefault(m.Enabled, true)
}

// ResolveInstanceName returns the configured InstanceName or the OS
// hostname (with any ".local" suffix stripped) when unset. Beyond mDNS
// advertising, this is the daemon's instance identity used as the
// leading component of the wire interface_id (ADR-0024).
func (m NorthDiscoveryMDNS) ResolveInstanceName() string {
	if m.InstanceName != "" {
		return m.InstanceName
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return strings.TrimSuffix(h, ".local")
	}
	return ""
}

// NorthMatter configures the Matter bridge runtime. Disabled by
// default — when enabled, the daemon stands up the UDP listener,
// assembles the endpoint topology, and advertises via mDNS. See
// ADR 0012 for the rollout plan.
type NorthMatter struct {
	// Enabled is the feature flag. Defaults to false; flip explicitly
	// when running the bridge against commissioners.
	Enabled bool `yaml:"enabled" json:"enabled" cfg:"basic"`

	// Listen is the UDP bind address. Empty defaults to ":5540" via
	// the udp package's MatterPort default. Use ":0" in tests / CI to
	// let the OS pick a port.
	Listen string `yaml:"listen" json:"listen" cfg:"expert"`

	// PreferIPv4 forces an IPv4-only socket. Default (false) opens an
	// IPv6 dual-stack socket which also accepts IPv4 traffic.
	PreferIPv4 bool `yaml:"prefer_ipv4" json:"prefer_ipv4" cfg:"expert"`

	// ExposeSecondaryChannels, when true, projects a device's secondary
	// channel-group members — the status transmitter channel and the
	// additional virtual-receiver actor channels — as their own Matter
	// endpoints in addition to the primary (group-master) channel.
	//
	// Default false: one physical HmIP device projects a SINGLE Matter
	// endpoint from its primary channel. A switch / dimmer / cover / lock /
	// siren / valve models its actor as a channel group (primary receiver +
	// a status transmitter + extra virtual-receiver link slots); exposing
	// every member would surface the same physical device as several
	// duplicate endpoints in Apple / Google Home. This mirrors HA-Discovery,
	// which already marks secondary channels enabled-by-default false.
	//
	// Matter-only: MQTT, HA-Discovery and the REST/WS surfaces always carry
	// every channel regardless of this flag.
	ExposeSecondaryChannels bool `yaml:"expose_secondary_channels" json:"expose_secondary_channels" cfg:"expert"`

	// VendorID is the bridge's IANA-assigned vendor identifier.
	// Defaults to 0xFFF1 (the test/development vendor block) when unset
	// — never ship that value in production.
	VendorID uint16 `yaml:"vendor_id" json:"vendor_id" cfg:"expert"`

	// ProductID is the vendor-assigned product identifier. Defaults
	// to 0x8000 when unset.
	ProductID uint16 `yaml:"product_id" json:"product_id" cfg:"expert"`

	// NodeLabel is the bridge's user-visible label. Defaults to
	// "openccu-loom" when unset.
	NodeLabel string `yaml:"node_label" json:"node_label" cfg:"basic"`

	// Discriminator is the 12-bit Matter commissioning discriminator.
	// Defaults to 0xF00 when unset.
	Discriminator uint16 `yaml:"discriminator" json:"discriminator" cfg:"expert"`

	// MDNSAdvertise selects the mDNS advertiser implementation:
	//   - "" (unset): defaults to "zeroconf" — commissioners can only
	//     discover the bridge when its records are actually on the
	//     network, so the enabled bridge advertises by default.
	//   - "zeroconf": multicast `_matter._tcp` + `_matterc._udp` records
	//     so commissioners (Apple Home, Google Home, chip-tool) find the
	//     bridge by service-type instead of needing an explicit IP/port.
	//     The commissionable record is published with `_L<long>` /
	//     `_S<short>` / `_V<vendor>` subtypes whenever a commissioning
	//     window is open and withdrawn on close.
	//   - "noop": explicit opt-out — in-memory only, no multicast
	//     traffic. Fine for unit tests and when discovery is handled
	//     out-of-band. Commissioning by QR scan CANNOT work in this
	//     mode; commissioners never see the bridge.
	MDNSAdvertise string `yaml:"mdns_advertise" json:"mdns_advertise" cfg:"expert"`

	// Commissioning configures the PASE acceptor. When Passcode is
	// non-zero the daemon stands up a Spake2+ verifier wired into
	// the bridge's PASE port; commissioner Pake1 traffic completes
	// against it and Pake3 success registers a fresh PASE session
	// with the operational manager. Passcode=0 leaves the PASE port
	// at noop (commissioners get debug-logged drops).
	Commissioning NorthMatterCommissioning `yaml:"commissioning" json:"commissioning" cfg:"basic"`

	// CASE configures the operational-session responder. When NodeID is
	// non-zero the daemon constructs a sigma responder and wires it into
	// the bridge's CASE port. The operational fabric identity established
	// during commissioning (NOC + ICAC + operational private key + IPK) is
	// persisted per fabric and rehydrated at boot, so CASE completes against
	// a production controller that validates the certificate chain and
	// survives daemon restarts — commissioned controllers reconnect without
	// re-pairing. The operational private key is stored unencrypted at rest;
	// protecting the data directory is the operator's responsibility.
	CASE NorthMatterCASE `yaml:"case" json:"case" cfg:"expert"`

	// Attestation configures the bridge's Device Attestation surface
	// (DAC + PAI + CD bytes + DAC private key). When all four paths
	// resolve, [OperationalCredentials] presents the production
	// material to commissioners; otherwise it falls back to an
	// ephemeral self-signed development DAC that only validates
	// under chip-tool's `--bypass-attestation-verifier true` flag.
	Attestation NorthMatterAttestation `yaml:"attestation" json:"attestation" cfg:"expert"`

	// DevRotateUniqueIDs mixes a per-boot 16-byte random salt into
	// every bridged endpoint's Matter UniqueID derivation. Default
	// false matches matter.js + chip's "stable UniqueID across
	// restarts" behaviour — production Apple Home / Google Home
	// installations rely on a stable identity per (bridge, endpoint)
	// to recognise accessories after a daemon restart. Setting this
	// to true rotates the identity at every boot, which is useful for
	// the chip-tool brief's T11 test (asserts UID changes across
	// restarts) and for the dev-mode "force-fresh-pair" workflow
	// after an Apple HMHome cache corruption. Toggling this in
	// production breaks accessory recognition; users must re-link
	// every device in the Home app after each daemon restart.
	DevRotateUniqueIDs bool `yaml:"dev_rotate_unique_ids" json:"dev_rotate_unique_ids" cfg:"expert"`

	// EnableTimeSync mounts the TimeSynchronization cluster (0x0038) on the
	// Root endpoint. Tri-state via *bool: nil / false → not mounted, which is
	// the safe default — matter.js lists TimeSynchronization as optional-only
	// on the RootNode and Apple Home's HAP service mapper may reject an
	// unexpected RootNode cluster at pairing. Set true only when a controller
	// genuinely needs the bridge to expose a time-sync surface (re-pair
	// afterwards). See docs/parity/by_design.md (BD-Matter-TimeSync-NotMounted).
	EnableTimeSync *bool `yaml:"enable_time_sync,omitempty" json:"enable_time_sync,omitempty" cfg:"expert"`
}

// TimeSyncEnabled reports whether the TimeSynchronization cluster (0x0038)
// should be mounted on the Root endpoint. Default (unset) is false.
func (m NorthMatter) TimeSyncEnabled() bool {
	return m.EnableTimeSync != nil && *m.EnableTimeSync
}

// WithDefaults returns a copy of m with every documented zero-value
// default applied. This is the single defaulting point for the Matter
// runtime: the bridge core, the commissioning-window opener, the mDNS
// advertisement, and the REST setup-payload endpoint must all consume
// the SAME defaulted view. Defaulting only a subset used to split the
// bridge identity: the bridge core saw discriminator 0xF00 while the
// QR code, manual code, and mDNS TXT record carried the raw 0 —
// commissioners then filtered for the wrong discriminator.
//
// The persisted config is never mutated — operators keep seeing (and
// saving) their own values; unset fields stay unset on disk.
func (m NorthMatter) WithDefaults() NorthMatter {
	out := m
	if out.VendorID == 0 {
		out.VendorID = 0xFFF1
	}
	if out.ProductID == 0 {
		out.ProductID = 0x8000
	}
	if out.NodeLabel == "" {
		out.NodeLabel = "openccu-loom"
	}
	if out.Discriminator == 0 {
		out.Discriminator = 0xF00
	}
	if out.MDNSAdvertise == "" {
		// An enabled bridge that never advertises is undiscoverable —
		// operators read "Matter enabled + passcode set" as pairable, so
		// the default must put records on the network. "noop" remains an
		// explicit opt-out for hermetic tests / out-of-band discovery.
		out.MDNSAdvertise = "zeroconf"
	}
	if out.Commissioning.Iterations == 0 {
		out.Commissioning.Iterations = 1000
	}
	return out
}

// NorthMatterAttestation carries vendor-supplied attestation
// material. All four files are PEM- or DER-encoded; the loader
// auto-detects format on read. The DAC private key MUST match the
// public key embedded in the DAC certificate (loader verifies this
// at startup; mismatch surfaces as a daemon log warning and
// triggers the dev-attestation fallback).
//
// Operators that ship a real device under their own VID + PID
// populate all four fields. Test deployments leave them empty and
// pair via `chip-tool --bypass-attestation-verifier true`.
type NorthMatterAttestation struct {
	// DACPath is the filesystem path to the Device Attestation
	// Certificate. PEM (".pem") or DER (".der") accepted; the
	// loader sniffs the magic bytes.
	DACPath string `yaml:"dac_path" json:"dac_path" cfg:"expert"`
	// DACKeyPath is the filesystem path to the DAC's P-256 private
	// key. PEM (PKCS#8) or DER accepted. Path itself is operator-
	// visible; the file's contents are treated as a secret.
	DACKeyPath string `yaml:"dac_key_path" json:"dac_key_path" cfg:"secret"`
	// PAIPath is the filesystem path to the Product Attestation
	// Intermediate cert.
	PAIPath string `yaml:"pai_path" json:"pai_path" cfg:"expert"`
	// CDPath is the filesystem path to the CSA-signed Certification
	// Declaration (CMS message).
	CDPath string `yaml:"cd_path" json:"cd_path" cfg:"expert"`
}

// NorthMatterCommissioning carries the parameters the PASE acceptor
// needs to verify a commissioner. The values are typically printed
// on the device's setup-code label or QR sheet; never check them
// into source control.
type NorthMatterCommissioning struct {
	// Passcode is the 27-bit Matter setup code (Spec §5.1.6.4),
	// between 00000001 and 99999998. Leave 0 to disable PASE.
	Passcode uint32 `yaml:"passcode" json:"passcode" cfg:"secret"`

	// Salt is the PBKDF2 salt persisted alongside the passcode
	// (16..32 bytes per Spec §3.10). Empty defaults to a fixed
	// development salt — never ship that default in production.
	Salt string `yaml:"salt" json:"salt" cfg:"secret"`

	// Iterations is the PBKDF2 iteration count (1000..100000 per
	// Spec §3.10). Defaults to 1000 when 0.
	Iterations int `yaml:"iterations" json:"iterations" cfg:"expert"`

	// ConcurrentPairings, when true, isolates the PaseAdapter per
	// exchange-id so multiple commissioners can pair in parallel
	// without trampling each other's transcript-bind capture. The
	// default (false) attaches a singleton adapter — fine for the
	// typical "one commissioner at a time" flow and slightly cheaper
	// memory-wise. Flip on for shared-bridge scenarios (e.g. multi-
	// admin Matter Hub) where two controllers may overlap.
	ConcurrentPairings bool `yaml:"concurrent_pairings" json:"concurrent_pairings" cfg:"expert"`

	// EphemeralWindow, when true, makes every REST-driven Open
	// Commissioning Window call generate a fresh discriminator +
	// passcode + Spake2+ verifier and swap them onto the bridge for
	// the window's lifetime. The configured Passcode then acts only
	// as the long-lived fallback acceptor used when no window is
	// open (e.g. the operator pre-prints a label code).
	//
	// Recommended for production: pairing codes auto-rotate and the
	// configured passcode never leaves the bridge process. Requires
	// ConcurrentPairings = false (the singleton adapter path is the
	// only one that supports a clean revert on close).
	EphemeralWindow bool `yaml:"ephemeral_window" json:"ephemeral_window" cfg:"basic"`
}

// UnmarshalJSON tolerates a JSON string for `passcode` in addition to a
// number. Config UIs render the Matter setup code as a secret text field
// (it is an 8-digit code where leading zeros are significant), so the
// REST section-PUT body arrives with the passcode quoted; decoding that
// straight into a uint32 otherwise fails with "cannot unmarshal string
// ... of type uint32". Every other field keeps strict decoding, including
// rejection of unknown keys, to match the section-validation contract.
//
// JSON only: YAML loading is unaffected (yaml.v3 does not call this), so
// config.yaml continues to use a plain numeric passcode.
func (c *NorthMatterCommissioning) UnmarshalJSON(data []byte) error {
	type alias NorthMatterCommissioning
	aux := &struct {
		Passcode flexUint32 `json:"passcode"`
		*alias
	}{alias: (*alias)(c)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(aux); err != nil {
		return err
	}
	c.Passcode = uint32(aux.Passcode)
	return nil
}

// flexUint32 decodes a uint32 from either a JSON number or a numeric
// JSON string. An empty string or null decodes to 0.
type flexUint32 uint32

func (f *flexUint32) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s = strings.TrimSpace(s); s == "" {
			*f = 0
			return nil
		}
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid numeric string %q: %w", s, err)
		}
		*f = flexUint32(n)
		return nil
	}
	var n uint32
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*f = flexUint32(n)
	return nil
}

// NorthMatterCASE configures the CASE (operational-session)
// responder. CASE picks up after a fabric is established (commissioner
// has installed a NOC). Until persistent fabric identity wiring lands,
// this block enables a STRUCTURAL wiring of `secure/sigma.Responder`
// with an ephemeral private key and a trust-everything peer verifier
// — useful for development against a controller that ignores cert
// validation, never for production.
//
// NodeID == 0 disables CASE entirely. When non-zero, the daemon
// constructs a CaseAdapter and wires it via [Bridge.AttachCaseHandler].
type NorthMatterCASE struct {
	// NodeID is the bridge's 64-bit operational node identifier
	// inside the fabric. Leave 0 to disable CASE.
	NodeID uint64 `yaml:"node_id" json:"node_id" cfg:"expert"`

	// FabricID is the 64-bit fabric identifier the bridge belongs to.
	// Required when NodeID is non-zero.
	FabricID uint64 `yaml:"fabric_id" json:"fabric_id" cfg:"expert"`
}

// TracingConfig controls span export to an external collector.
// Export is disabled by default (empty OTLPEndpoint).
type TracingConfig struct {
	// OTLPEndpoint, when non-empty, enables OTLP/HTTP trace export to
	// <endpoint>/v1/traces. Empty (default) disables export.
	OTLPEndpoint string `yaml:"otlp_endpoint" json:"otlp_endpoint" cfg:"expert"`
}

// NorthREST configures the REST+WS server.
type NorthREST struct {
	Listen string   `yaml:"listen" json:"listen" cfg:"basic"`
	CORS   []string `yaml:"cors" json:"cors" cfg:"basic"`
	// PublicURL is the externally-reachable base URL of this daemon's
	// REST + Config-UI surface, e.g. "https://loom.example.de". Set it
	// when the daemon sits behind a reverse proxy (Traefik, nginx, …)
	// that terminates TLS and routes a hostname to the REST listener:
	// the CCU add-on's "Open Config UI" button then links straight at
	// the proxy instead of the direct host:port heuristic, which a
	// browser on the public side cannot reach. Empty (the default) keeps
	// the heuristic — correct for a LAN install hitting the CCU directly.
	// No path suffix: the SPA mount (/app/) is appended by ConfigUIURL.
	PublicURL string `yaml:"public_url" json:"public_url" cfg:"basic"`
	// Enabled is the master switch for the REST + WebSocket
	// server. Defaults to true when absent — the daemon has no
	// operator surface without it. Use *bool so the YAML decoder
	// can distinguish "not set" from "explicitly false".
	Enabled *bool      `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	Auth    AuthConfig `yaml:"auth" json:"auth" cfg:"basic"`
	// OpenAPISpecPath is the on-disk path of the API spec. The
	// daemon loads it at boot and, when [OpenAPIValidate] is true,
	// installs the [middleware.OpenAPIValidator] in the REST
	// chain. Empty falls back to `assets/openapi.yaml` next to the
	// binary; missing files are tolerated (validator simply not
	// installed) so dev iterations on the spec do not block the
	// daemon.
	OpenAPISpecPath string `yaml:"openapi_spec_path" json:"openapi_spec_path" cfg:"expert"`
	// OpenAPIValidate toggles request validation against the spec.
	// Pointer-bool with three states:
	//   - nil   → unset in YAML → defaults to TRUE (validation on).
	//             Documented MVP endpoints are covered by the spec
	//             (enforced by `TestOpenAPIDeclaresMVPEndpoints`),
	//             so production should leave this unset.
	//   - true  → explicit on (same as nil).
	//   - false → explicit opt-out; useful during a spec migration
	//             on a fork where some endpoint is missing in the
	//             local spec copy.
	OpenAPIValidate *bool `yaml:"openapi_validate,omitempty" json:"openapi_validate,omitempty" cfg:"expert"`
	// WS holds knobs scoped to the WebSocket surface (replay buffer,
	// future heartbeat tuning).
	WS NorthRESTWS `yaml:"ws" json:"ws" cfg:"expert"`
	// RateLimit configures the per-identity REST rate limiter.
	// Defaults to disabled — operators flip after profiling.
	RateLimit NorthRESTRateLimit `yaml:"rate_limit" json:"rate_limit" cfg:"basic"`
	// CSRFEnabled mounts the double-submit cookie/header guard for
	// mutating REST endpoints. Enabled by default because the SPA is
	// served by the same daemon and every browser-facing deployment
	// needs this protection. Set to false only for pure API-token
	// deployments where no browser session cookies are issued.
	CSRFEnabled *bool `yaml:"csrf_enabled,omitempty" json:"csrf_enabled,omitempty" cfg:"basic"`
	// CSRFSecure sets the Secure flag on the CSRF cookie. Enable when
	// the daemon is behind an HTTPS/TLS terminator.
	CSRFSecure bool `yaml:"csrf_secure" json:"csrf_secure" cfg:"basic"`
	// Tracing configures span export to an external OTLP collector.
	// Disabled by default (empty OTLPEndpoint).
	Tracing TracingConfig `yaml:"tracing" json:"tracing" cfg:"expert"`
	// TLSCertFile / TLSKeyFile, when both set, switch the REST + SPA
	// listener to HTTPS. The same port serves the API and the SPA, so
	// enabling TLS here secures both. PEM-encoded; the certificate is
	// hot-reloaded on file change (and after an upload via
	// POST /admin/tls/certificate) without a daemon restart. Empty
	// (the default) keeps plain HTTP — correct behind a TLS-terminating
	// reverse proxy (see PublicURL / CSRFSecure).
	TLSCertFile string `yaml:"tls_cert_file" json:"tls_cert_file" cfg:"basic"`
	TLSKeyFile  string `yaml:"tls_key_file" json:"tls_key_file" cfg:"basic"`
}

// TLSEnabled reports whether both the certificate and key paths are set,
// i.e. the listener should serve HTTPS.
func (n NorthREST) TLSEnabled() bool {
	return n.TLSCertFile != "" && n.TLSKeyFile != ""
}

// NorthRESTWS tunes the WebSocket subsystem.
type NorthRESTWS struct {
	// ReplayCapacity is the in-memory ring-buffer ceiling for
	// subscribe-with-since replays (ADR-0022). Default 1024 events;
	// 0 disables replay (subscribe-with-since immediately yields
	// replay_lost). Operators with high-event-rate deployments can
	// raise this; the buffer is a fixed Go slice so RAM cost is
	// roughly capacity × ~200 B per event.
	ReplayCapacity int `yaml:"replay_capacity" json:"replay_capacity" cfg:"expert"`
}

// NorthRESTRateLimit configures the per-identity REST rate limiter.
// Defaults emit no enforcement (Enabled = false) — operators opt in
// after profiling their typical client traffic. When enabled the
// middleware emits HTTP 429 problem+json with a Retry-After header
// per RFC 9110 §10.2.3 for the openapi.yaml Problem-schema-documented
// rate_limited contract.
type NorthRESTRateLimit struct {
	// Enabled toggles the middleware. Defaults to false; flip
	// explicitly once steady-state traffic is profiled.
	Enabled bool `yaml:"enabled" json:"enabled" cfg:"basic"`
	// RequestsPerSecond is the steady-state token-refill rate per
	// identity. Defaults to 10 when zero.
	RequestsPerSecond float64 `yaml:"requests_per_second" json:"requests_per_second" cfg:"expert"`
	// Burst is the bucket size (max concurrent requests per
	// identity before the limiter starts gating). Defaults to 30
	// when zero.
	Burst int `yaml:"burst" json:"burst" cfg:"expert"`
}

// OpenAPIValidateEnabled returns the effective OpenAPI-validation
// setting after applying the nil-default convention. Daemons should
// read through this helper rather than touching the pointer directly.
func (n NorthREST) OpenAPIValidateEnabled() bool {
	return orDefault(n.OpenAPIValidate, true)
}

// ConfigUIURL returns the externally-reachable Config-UI (SPA) URL
// derived from PublicURL, or "" when no public URL is configured. The
// SPA is mounted at /app/ on the REST listener, so the mount path is
// appended to the operator-supplied origin (any trailing slash on
// PublicURL is collapsed first). The CCU add-on writes this value to a
// hint file that config.cgi links at.
func (n NorthREST) ConfigUIURL() string {
	if n.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(n.PublicURL, "/") + "/app/"
}

// IsEnabled reports whether the REST + WebSocket server should run.
// nil → true (the default), so an operator only has to set
// `enabled: false` to opt out.
func (n NorthREST) IsEnabled() bool {
	return orDefault(n.Enabled, true)
}

// NorthUI configures the HTMX Config UI.
type NorthUI struct {
	// Enabled is the master switch for the HTMX-bootstrap UI
	// (login, /setup wizard, /health, /about). Defaults to true
	// when absent — without it the SPA cannot offer pre-auth
	// flows and the daemon cannot drive a first-run setup. Use
	// *bool so the YAML decoder can distinguish "not set" from
	// "explicitly false".
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`

	// Embedded declares that Home Assistant owns this daemon's config
	// surface — the Homematic(IP) Local integration runs against THIS
	// daemon. It selects the `embedded` surface profile, which hides what
	// HA already owns and scopes the writes the Ingress passthrough
	// identity may perform.
	//
	// It is deliberately NOT derived from the Ingress signal. Being behind
	// HA Ingress answers "am I proxied by HA?", not "does HA own my config
	// surface?" — two propositions that come apart in both directions: the
	// add-on runs in deployments that never configure the integration, and
	// the remote proxy add-on forwards X-Ingress-Path while deliberately
	// serving the full UI. Only the operator knows the answer, so it is
	// declared. nil → false.
	Embedded *bool `yaml:"embedded,omitempty" json:"embedded,omitempty" cfg:"basic"`

	// Profiles carries per-profile surface overrides, keyed by profile
	// name (see [ProfileStandalone] / [ProfileEmbedded]) and then by
	// surface id. It is SPARSE by construction: only deviations from the
	// shipped default are stored, so a view added in a later release
	// arrives with the default its own code assigns instead of being
	// invisible because it was missing from a frozen snapshot.
	//
	// Edited through the dedicated /api/v1/ui/surfaces endpoint rather
	// than the generic section editor, which cannot render a nested map.
	Profiles map[string]map[string]SurfaceState `yaml:"profiles,omitempty" json:"profiles,omitempty" cfg:"basic"`
}

// SurfaceState is the stored visibility of a single UI surface. The
// string form keeps the YAML self-describing (`nav.alarm: hidden` reads
// as what it does, `nav.alarm: false` does not).
type SurfaceState string

const (
	// SurfaceVisible shows a surface that the shipped default hides.
	SurfaceVisible SurfaceState = "visible"
	// SurfaceHidden hides a surface that the shipped default shows.
	SurfaceHidden SurfaceState = "hidden"
)

// Surface profile names. Two, fixed: "who owns the config surface" has
// exactly two answers, and every further profile would double the
// review surface of the shipped default table.
const (
	// ProfileStandalone is live unless Embedded is set. It ships with
	// every surface visible.
	ProfileStandalone = "standalone"
	// ProfileEmbedded is live when Embedded is true.
	ProfileEmbedded = "embedded"
)

// IsEnabled reports whether the bootstrap UI should run. nil → true.
func (n NorthUI) IsEnabled() bool {
	return orDefault(n.Enabled, true)
}

// IsEmbedded reports whether Home Assistant owns this daemon's config
// surface. nil → false: a daemon that was never told so must serve
// everything.
func (n NorthUI) IsEmbedded() bool {
	return orDefault(n.Embedded, false)
}

// ActiveProfile names the surface profile the daemon currently serves.
func (n NorthUI) ActiveProfile() string {
	if n.IsEmbedded() {
		return ProfileEmbedded
	}
	return ProfileStandalone
}

// SurfaceOverrides returns the stored overrides of one profile. The
// returned map is never nil, so callers can range over it directly.
func (n NorthUI) SurfaceOverrides(profile string) map[string]SurfaceState {
	if n.Profiles == nil {
		return map[string]SurfaceState{}
	}
	out := make(map[string]SurfaceState, len(n.Profiles[profile]))
	for id, state := range n.Profiles[profile] {
		out[id] = state
	}
	return out
}

// NorthMQTT configures the MQTT bridge.
type NorthMQTT struct {
	Enabled          bool   `yaml:"enabled" json:"enabled" cfg:"basic"`
	BrokerURL        string `yaml:"broker_url" json:"broker_url" cfg:"basic"` // tcp://host:1883
	ClientID         string `yaml:"client_id" json:"client_id" cfg:"basic"`
	Username         string `yaml:"username" json:"username" cfg:"basic"`
	Password         string `yaml:"password" json:"password" cfg:"secret"`
	TopicBase        string `yaml:"topic_base" json:"topic_base" cfg:"basic"`
	RawEnabled       bool   `yaml:"raw_enabled" json:"raw_enabled" cfg:"basic"`
	DiscoveryEnabled bool   `yaml:"discovery_enabled" json:"discovery_enabled" cfg:"basic"`

	// ProtocolVersion selects the MQTT wire dialect: "5" (default when
	// empty) or "3.1.1" for brokers without MQTT 5.0 support. There is
	// no silent downgrade — a v5 connect against a v3-only broker
	// surfaces a named error, and this knob is the fix.
	ProtocolVersion string `yaml:"protocol_version,omitempty" json:"protocol_version,omitempty" cfg:"expert"`

	// PayloadFormat selects the wire format the bridge publishes to
	// state topics. Empty / "bare" (default) keeps primitive scalar
	// payloads — backwards-compatible with non-HA consumers.
	// "json" wraps state in `{"value":..,"available":..}`.
	// `value_template` filters and gets per-DP availability for
	// free. Switching this BREAKS bare-value subscribers; flip it
	// only after every consumer has been updated.
	PayloadFormat string `yaml:"payload_format" json:"payload_format" cfg:"expert"`

	// SubDevicesEnabled splits multi-channel-group devices into one
	// HA device per channel group. Each sub-device's MQTT discovery
	// stamps `device.identifiers = "<topic_base>_<addr>-<group_no>"`
	// and `device.via_device = "<topic_base>_<addr>"`, so HA renders
	// the parent + N children hierarchy.
	SubDevicesEnabled bool `yaml:"sub_devices_enabled" json:"sub_devices_enabled" cfg:"basic"`

	// RetainCleanupWindowMs is the broker snapshot window (in milliseconds)
	// used by RunRetainCleanupOnce and RunDiscoveryOrphanCleanupOnce.
	// The daemon subscribes to the legacy/discovery topic prefix and waits
	// this long for the broker to deliver all retained messages before
	// processing the eviction list.
	//
	// Valid range: 500–30000 ms. Zero falls back to the default (2000 ms).
	// Raising the value helps on high-latency brokers or large retained-message
	// stores; lowering it shortens boot time at the risk of missing some
	// retained messages.  [default: 2000]
	RetainCleanupWindowMs int `yaml:"retain_cleanup_window_ms" json:"retain_cleanup_window_ms,omitempty" cfg:"expert"`
}

// EffectiveRetainCleanupWindow returns the broker snapshot window for retain
// cleanup as a [time.Duration], applying the default (2 s) when the field is
// zero and clamping the value to the [500 ms, 30 s] range so mis-configured
// values never produce absurdly short or indefinitely long boot stalls.
func (m NorthMQTT) EffectiveRetainCleanupWindow() time.Duration {
	const (
		defaultMs = 2000
		minMs     = 500
		maxMs     = 30000
	)
	ms := m.RetainCleanupWindowMs
	if ms <= 0 {
		ms = defaultMs
	}
	if ms < minMs {
		ms = minMs
	}
	if ms > maxMs {
		ms = maxMs
	}
	return time.Duration(ms) * time.Millisecond
}

// AuthConfig collects auth-related switches.
//
// BasicEnabled / BearerEnabled are tri-state policy gates for the two
// header-based schemes: nil defaults to enabled, an explicit false
// rejects the scheme even when credentials are configured. A scheme is
// therefore active iff its gate is on AND matching credentials exist.
// Session-cookie resolution has no gate — it is the SPA's core login
// mechanism and always installed.
type AuthConfig struct {
	BasicEnabled  *bool             `yaml:"basic_enabled,omitempty" json:"basic_enabled,omitempty" cfg:"basic"`
	BearerEnabled *bool             `yaml:"bearer_enabled,omitempty" json:"bearer_enabled,omitempty" cfg:"basic"`
	Users         map[string]string `yaml:"users" json:"users" cfg:"secret"`   // username → bcrypt hash (MVP: plaintext)
	Tokens        map[string]string `yaml:"tokens" json:"tokens" cfg:"secret"` // token → role
	OIDC          OIDCConfig        `yaml:"oidc" json:"oidc" cfg:"basic"`
	CCU           CCUAuthConfig     `yaml:"ccu" json:"ccu" cfg:"basic"`
	HAIngress     HAIngressConfig   `yaml:"ha_ingress" json:"ha_ingress" cfg:"basic"`
}

// BasicAuthEnabled resolves the tri-state Basic gate: nil defaults to
// enabled so existing configs keep working unchanged.
func (a AuthConfig) BasicAuthEnabled() bool {
	return orDefault(a.BasicEnabled, true)
}

// BearerAuthEnabled resolves the tri-state Bearer gate: nil defaults
// to enabled so existing configs keep working unchanged.
func (a AuthConfig) BearerAuthEnabled() bool {
	return orDefault(a.BearerEnabled, true)
}

// HAIngressConfig opts into trusting Home Assistant Ingress: when the daemon
// runs as the supervised HA add-on behind Ingress, a request proxied by the
// Supervisor is treated as an authenticated admin without a local login (see
// ADR 0044). It is a deliberate auth bypass, so it is OFF by default and
// guarded by several independent conditions resolved by the composition root:
//   - the build must be supervised (the add-on build stamp / OPENCCU_LOOM_SUPERVISOR),
//   - the request's real TCP peer (RemoteAddr, never X-Forwarded-For) must fall
//     inside TrustedProxyCIDR (the Supervisor's Docker subnet), and
//   - the request must carry the Supervisor's X-Ingress-Path header.
//
// It maps to a single admin identity only — HA does not pass the user's name
// or role to the add-on, so the safety contract is that the add-on's
// config.yaml keeps `panel_admin: true` (only HA admins reach Ingress). A
// genuine Bearer token or session always wins over the passthrough, so a
// scoped token is never silently elevated.
type HAIngressConfig struct {
	// Enabled is tri-state: nil defaults to the supervised stamp — ON in the
	// HA add-on (where Ingress is admin-only via panel_admin: true), OFF in a
	// plain build / Docker image. An explicit true/false overrides. Resolved by
	// the composition root (it depends on the supervised/build stamp, which
	// config must not import). Even when ON the passthrough only ever fires for
	// a genuine Supervisor-proxied request (see the gates above).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	// TrustedProxyCIDR is the network the Ingress request must originate from
	// (the request's real RemoteAddr). Empty uses the HA Supervisor default
	// (172.30.32.0/23). Only the loopback / this CIDR are ever trusted.
	TrustedProxyCIDR string `yaml:"trusted_proxy_cidr" json:"trusted_proxy_cidr" cfg:"expert"`
	// Role is the Loom role granted to a trusted Ingress request: "admin"
	// (default), "operator" or "viewer".
	Role string `yaml:"role" json:"role" cfg:"expert"`
}

// CCUAuthConfig delegates login to a CCU's own user database (see
// ADR 0043). When enabled, the login chain validates credentials against
// the named central via Session.login and maps the CCU UserLevel to a
// Loom role. Carries no secret: the credentials come from the login
// form, not the config.
type CCUAuthConfig struct {
	// Enabled is tri-state: nil (unset) defaults to the build's add-on
	// flag — true in the CCU add-on, false otherwise — so the add-on
	// ships with CCU login on and a plain build keeps it off. An
	// explicit true/false overrides. Resolved by the composition root
	// (it depends on the build stamp, which config must not import).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	// Primary is tri-state: nil defaults to true (when CCU auth is on,
	// the CCU is the primary source; local users are the break-glass
	// fallback). false flips to local-first / CCU-last. Break-glass-safe
	// either way — the CCU store maps every failure (wrong credentials
	// or outage) to "unauthenticated", so a local admin always falls
	// through to the local store. See ADR 0043.
	Primary *bool `yaml:"primary,omitempty" json:"primary,omitempty" cfg:"basic"`
	// Central names the CentralConfig whose user database authenticates
	// logins. Empty selects the first configured central.
	Central string `yaml:"central" json:"central" cfg:"basic"`
	// MinUserLevel rejects users below this CCU UserLevel (0 = UPL_NONE
	// is always denied). Default 1 (guest) admits any real user.
	MinUserLevel int `yaml:"min_user_level" json:"min_user_level" cfg:"expert"`
	// RoleMapping overrides the default UserLevel → Loom-role mapping.
	// Keys are the CCU UserLevel as a string ("8","2","1"); values are
	// "admin" / "operator" / "viewer". Empty uses the ADR 0043 defaults.
	RoleMapping map[string]string `yaml:"role_mapping" json:"role_mapping" cfg:"expert"`
}

// OIDCConfig describes one OpenID Connect provider. Empty Issuer
// disables the flow.
type OIDCConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled" cfg:"basic"`
	Issuer       string `yaml:"issuer" json:"issuer" cfg:"basic"`
	ClientID     string `yaml:"client_id" json:"client_id" cfg:"basic"`
	ClientSecret string `yaml:"client_secret" json:"client_secret" cfg:"secret"`
	RedirectURL  string `yaml:"redirect_url" json:"redirect_url" cfg:"basic"`
	RoleClaim    string `yaml:"role_claim" json:"role_claim" cfg:"expert"`
}

// CentralConfig describes one configured CCU.
//
// Port is the legacy single-port override applied to every configured
// interface when Ports does not name it explicitly. Ports takes
// precedence and lets operators pin a different endpoint per
// interface (keys match [hmenum.Interface] string values, e.g.
// "HmIP-RF", "BidCos-RF", "VirtualDevices").
//
// TLSInsecureSkipVerify disables certificate verification on the
// XML-RPC client. Set only against self-signed CCUs in trusted
// networks — it short-circuits hostname + chain validation.
type CentralConfig struct {
	Name  string         `yaml:"name" json:"name" cfg:"basic"`
	Host  string         `yaml:"host" json:"host" cfg:"basic"`
	Port  int            `yaml:"port" json:"port" cfg:"expert"`
	Ports map[string]int `yaml:"ports" json:"ports" cfg:"expert"`
	// JSONRPCPort overrides the CCU's HTTP port for JSON-RPC and
	// related top-level web endpoints (e.g. /api/homematic.cgi,
	// /config/cp_security.cgi). Zero (the default) falls back to 80
	// (plain) / 443 (TLS) — the standard CCU configuration. Useful
	// when the CCU sits behind a non-standard reverse proxy or when
	// running against an in-process CCU simulator that binds
	// JSON-RPC to an OS-assigned port.
	JSONRPCPort           int             `yaml:"json_rpc_port" json:"json_rpc_port" cfg:"expert"`
	Username              string          `yaml:"username" json:"username" cfg:"basic"`
	Password              string          `yaml:"password" json:"password" cfg:"secret"`
	Interfaces            []InterfaceSpec `yaml:"interfaces" json:"interfaces" cfg:"basic"`
	TLS                   bool            `yaml:"tls" json:"tls" cfg:"basic"`
	TLSInsecureSkipVerify bool            `yaml:"tls_insecure_skip_verify" json:"tls_insecure_skip_verify" cfg:"expert"`

	// PrimaryInterface pins the CCU's primary interface for the
	// per-central health-aggregation rule (see internal/health). Empty
	// (the default) keeps the `HmIP-RF` substring heuristic that
	// works for the overwhelming majority of installations. Set this
	// when the operator runs BidCos-RF as the primary surface and
	// HmIP-RF is either absent or marginal.
	PrimaryInterface string `yaml:"primary_interface" json:"primary_interface" cfg:"expert"`

	// Visibility holds per-central visibility overrides — currently
	// just the un_ignore list that promotes otherwise-hidden parameters
	// to first-class data points. The list seeded here is a bootstrap
	// default; runtime changes via the REST surface persist in SQLite
	// and take precedence after the next daemon start.
	Visibility VisibilityConfig `yaml:"visibility" json:"visibility" cfg:"expert"`

	// CheckConnectionInterval overrides the background check_connection
	// job cadence for this central. Zero uses the compiled-in default
	// (30 s). Negative disables the job. Useful in test environments
	// where the default 30 s cadence makes degraded-state detection
	// unacceptably slow.
	CheckConnectionInterval time.Duration `yaml:"check_connection_interval" json:"check_connection_interval,omitempty" cfg:"expert"`

	// Behavior holds per-central custom-data-point rendering toggles
	// (light last-brightness, cover group-channel state).
	Behavior CentralBehavior `yaml:"behavior" json:"behavior" cfg:"expert"`
}

// CentralBehavior groups per-central custom-data-point rendering
// toggles. Each is a *bool so the YAML/DB decoder can distinguish
// "not set" (→ default) from an explicit value; both default to true.
type CentralBehavior struct {
	// LightLastBrightness controls a plain light turn-on. When true
	// (default) the light restores the last non-zero brightness the
	// CCU reported; when false it turns on at full (100%). Reference
	// stack key: enable_light_last_brightness.
	LightLastBrightness *bool `yaml:"light_last_brightness,omitempty" json:"light_last_brightness,omitempty" cfg:"expert"`

	// UseGroupChannelForCoverState controls cover position reporting.
	// When true (default) a cover that exposes a group-channel LEVEL
	// reports its position from the group channel; when false it
	// reports from its own channel. Reference stack key:
	// use_group_channel_for_cover_state.
	UseGroupChannelForCoverState *bool `yaml:"use_group_channel_for_cover_state,omitempty" json:"use_group_channel_for_cover_state,omitempty" cfg:"expert"`

	// EnableSysvarScan / EnableProgramScan gate the per-central hub
	// scan: when false the daemon never fetches system variables /
	// programs from the CCU, so no hub entities of that kind spawn.
	// Both default to true. Reference stack keys: enable_sysvar_scan,
	// enable_program_scan.
	EnableSysvarScan  *bool `yaml:"enable_sysvar_scan,omitempty" json:"enable_sysvar_scan,omitempty" cfg:"expert"`
	EnableProgramScan *bool `yaml:"enable_program_scan,omitempty" json:"enable_program_scan,omitempty" cfg:"expert"`

	// IncludeInternalSysvars (default true) / IncludeInternalPrograms
	// (default false) control whether CCU-internal system variables /
	// programs surface as hub entities. Reference stack keys:
	// include_internal_sysvars, include_internal_programs.
	IncludeInternalSysvars  *bool `yaml:"include_internal_sysvars,omitempty" json:"include_internal_sysvars,omitempty" cfg:"expert"`
	IncludeInternalPrograms *bool `yaml:"include_internal_programs,omitempty" json:"include_internal_programs,omitempty" cfg:"expert"`

	// SysvarMarkers / ProgramMarkers restrict the hub scan to entities
	// whose CCU description carries one of the listed marker tokens
	// (prefix match). The tokens are the closed [hmenum.DescriptionMarker]
	// set (HAHM, HX, INTERNAL, MQTT). Empty (default) includes
	// everything that passes the internal filter. Reference stack keys:
	// sysvar_markers, program_markers.
	SysvarMarkers  []hmenum.DescriptionMarker `yaml:"sysvar_markers,omitempty" json:"sysvar_markers,omitempty" cfg:"expert"`
	ProgramMarkers []hmenum.DescriptionMarker `yaml:"program_markers,omitempty" json:"program_markers,omitempty" cfg:"expert"`

	// SysvarScanInterval overrides the periodic sysvar-refresh cadence
	// for this central. Zero uses the compiled-in default. Values below
	// [MinHubScanInterval] are rejected by [Config.Validate]. Reference
	// stack key: sysvar_scan_interval.
	SysvarScanInterval time.Duration `yaml:"sysvar_scan_interval,omitempty" json:"sysvar_scan_interval,omitempty" cfg:"expert"`

	// EnableDeviceFirmwareCheck (default true) gates the per-device
	// firmware-update entity surface. Reference stack key:
	// enable_device_firmware_check (which defaults false there; see
	// docs/parity/by_design.md for the divergence rationale).
	EnableDeviceFirmwareCheck *bool `yaml:"enable_device_firmware_check,omitempty" json:"enable_device_firmware_check,omitempty" cfg:"expert"`

	// DelayNewDeviceCreation (default false) defers creation of a
	// newly-paired device until its description is complete, avoiding
	// half-formed entities during pairing. Reference stack key:
	// delay_new_device_creation.
	DelayNewDeviceCreation *bool `yaml:"delay_new_device_creation,omitempty" json:"delay_new_device_creation,omitempty" cfg:"expert"`
}

// LightLastBrightnessEnabled reports the resolved toggle, defaulting
// to true when unset.
func (b CentralBehavior) LightLastBrightnessEnabled() bool {
	return orDefault(b.LightLastBrightness, true)
}

// UseGroupChannelForCoverStateEnabled reports the resolved toggle,
// defaulting to true when unset.
func (b CentralBehavior) UseGroupChannelForCoverStateEnabled() bool {
	return orDefault(b.UseGroupChannelForCoverState, true)
}

// EnableSysvarScanEnabled reports the resolved toggle (default true).
func (b CentralBehavior) EnableSysvarScanEnabled() bool {
	return orDefault(b.EnableSysvarScan, true)
}

// EnableProgramScanEnabled reports the resolved toggle (default true).
func (b CentralBehavior) EnableProgramScanEnabled() bool {
	return orDefault(b.EnableProgramScan, true)
}

// IncludeInternalSysvarsEnabled reports the resolved toggle (default true).
func (b CentralBehavior) IncludeInternalSysvarsEnabled() bool {
	return orDefault(b.IncludeInternalSysvars, true)
}

// IncludeInternalProgramsEnabled reports the resolved toggle (default false).
func (b CentralBehavior) IncludeInternalProgramsEnabled() bool {
	return orDefault(b.IncludeInternalPrograms, false)
}

// EnableDeviceFirmwareCheckEnabled reports the resolved toggle. Default
// true: openccu-loom surfaces per-device firmware-update entities out
// of the box (a deliberate divergence from the reference stack's
// false default — see docs/parity/by_design.md).
func (b CentralBehavior) EnableDeviceFirmwareCheckEnabled() bool {
	return orDefault(b.EnableDeviceFirmwareCheck, true)
}

// DelayNewDeviceCreationEnabled reports the resolved toggle (default false).
func (b CentralBehavior) DelayNewDeviceCreationEnabled() bool {
	return orDefault(b.DelayNewDeviceCreation, false)
}

// VisibilityConfig configures per-central visibility overrides. Empty
// fields mean "no bootstrap override — use whatever is persisted in
// SQLite, or the built-in defaults when SQLite is empty".
type VisibilityConfig struct {
	// UnIgnore lists `MODEL:CHANNEL:PARAMETER` patterns (with `*`
	// wildcards for MODEL / CHANNEL) that promote parameters out of
	// the default-hidden set into the visible data-point surface.
	// Bare parameter names (no colons) are treated as
	// `*:*:PARAMETER`.
	UnIgnore []string `yaml:"un_ignore" json:"un_ignore" cfg:"expert"`
}

// ErrNoConfig is returned when [Load] is called with an empty path.
var ErrNoConfig = errors.New("config: no path provided")

// Load parses path as YAML into a Config. Missing fields fall back
// to zero values; defaults are applied by the constructor.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, ErrNoConfig
	}
	buf, err := os.ReadFile(path) //nolint:gosec // path comes from operator-supplied CLI arg; see #20
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(buf)
}

// Parse ingests raw YAML bytes.
func Parse(buf []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(buf, cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default returns a Config populated with MVP-safe defaults.
func Default() *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	return cfg
}

// Clone returns an independent deep copy of c via a JSON round-trip, so a
// caller can layer changes onto a snapshot without mutating the config the
// running daemon holds. Callers that re-assemble the effective config (the
// reload path overlays the DB-tier sections onto the YAML base) need this
// because the overlay mutates its target in place. A nil receiver or a
// marshalling failure yields [Default] rather than nil, keeping the result
// safe to dereference.
func Clone(c *Config) *Config {
	if c == nil {
		return Default()
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Default()
	}
	out := &Config{}
	if err := json.Unmarshal(raw, out); err != nil {
		return Default()
	}
	return out
}

func (c *Config) applyDefaults() {
	if c.Locale == "" {
		c.Locale = "en"
	}
	if c.DataDir == "" {
		c.DataDir = "./var"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Callback.Host == "" {
		c.Callback.Host = "0.0.0.0"
	}
	if c.Callback.Port == 0 {
		c.Callback.Port = 8120
	}
	if c.Callback.BinPort == 0 {
		c.Callback.BinPort = 8129
	}
	if c.Callback.MaxConnections == 0 {
		c.Callback.MaxConnections = 64
	}
	if c.North.REST.Listen == "" {
		c.North.REST.Listen = ":8119"
	}
	if c.North.MQTT.TopicBase == "" {
		c.North.MQTT.TopicBase = "openccu-loom"
	}
	if c.North.REST.WS.ReplayCapacity == 0 {
		c.North.REST.WS.ReplayCapacity = 1024
	}
	if c.North.REST.CSRFEnabled == nil {
		t := true
		c.North.REST.CSRFEnabled = &t
	}
	if c.Alarm.Enabled == nil {
		t := true
		c.Alarm.Enabled = &t
	}
	if c.Alarm.DefaultSirenSeconds == 0 {
		c.Alarm.DefaultSirenSeconds = 180
	}
	if c.Alarm.MaxAcousticPerIncidentSeconds == 0 {
		c.Alarm.MaxAcousticPerIncidentSeconds = 900
	}
	if c.Alarm.StopVerifySeconds == 0 {
		c.Alarm.StopVerifySeconds = 120
	}
	if c.Alarm.JournalRetentionDays == 0 {
		c.Alarm.JournalRetentionDays = 90
	}
	if c.Alarm.RestartLoopBreaker == 0 {
		c.Alarm.RestartLoopBreaker = 3
	}
	if c.Alarm.DuressVisibility == "" {
		// notify_only reaches a phone without leaving the report on a
		// screen an attacker could read back.
		c.Alarm.DuressVisibility = string(hmenum.DuressVisibilityNotifyOnly)
	}
	if c.AddonUpdate.CheckInterval == 0 {
		c.AddonUpdate.CheckInterval = 24 * time.Hour
	}
}

// ApplyDefaults fills any still-unset field with its compiled-in default.
// It is idempotent — every field is guarded by a zero-value check — so it
// is safe to call again after a config-store overlay has merged a partial
// section on top of an already-defaulted config. The config-store path
// relies on this: a stored section that omits a field (e.g. north.mqtt
// without topic_base) would otherwise leave that field at the overlay's
// zero value and fail Validate.
func (c *Config) ApplyDefaults() { c.applyDefaults() }

// CSRFIsEnabled returns the effective CSRF-enabled flag. When the config
// field has not been set explicitly the default is true (safe default for
// browser-facing deployments). Callers that need a plain bool use this
// helper instead of dereferencing the pointer directly.
func (n *NorthREST) CSRFIsEnabled() bool {
	return orDefault(n.CSRFEnabled, true)
}

// MinHubScanInterval is the floor for an operator-set hub-scan cadence.
//
// Each cycle costs the CCU a JSON-RPC call plus a ReGa script run, and the
// ReGa interpreter is single-threaded: a cadence short enough for cycles to
// overlap starves the CCU's own automations rather than delivering fresher
// data. Three seconds matches the tightest interval a comparable gateway
// polls at, and leaves the compiled-in 30 s default an order of magnitude
// of room.
//
// Zero is not a violation — it selects the compiled-in default.
const MinHubScanInterval = 3 * time.Second

// validateCentralBehavior checks the per-central behaviour block. It is a
// function rather than inline code so [Config.Validate] stays within the
// linter's cyclomatic budget as the block grows.
func validateCentralBehavior(idx int, b *CentralBehavior) error {
	if b.SysvarScanInterval != 0 && b.SysvarScanInterval < MinHubScanInterval {
		return fmt.Errorf("config: centrals[%d].behavior.sysvar_scan_interval: %s is below the %s minimum (0 selects the default)",
			idx, b.SysvarScanInterval, MinHubScanInterval)
	}
	return nil
}

// Validate returns an error when required invariants are violated.
func (c *Config) Validate() error {
	if c.Logging.Level != "debug" && c.Logging.Level != "info" &&
		c.Logging.Level != "warn" && c.Logging.Level != "error" {
		return fmt.Errorf("config: invalid logging.level %q", c.Logging.Level)
	}
	if c.Logging.Format != "json" && c.Logging.Format != "text" && c.Logging.Format != "text-color" {
		return fmt.Errorf("config: invalid logging.format %q", c.Logging.Format)
	}
	if c.Callback.Port < 0 || c.Callback.Port > 65535 {
		return fmt.Errorf("config: callback.port out of range: %d", c.Callback.Port)
	}
	if c.Callback.BinPort < 0 || c.Callback.BinPort > 65535 {
		return fmt.Errorf("config: callback.bin_port out of range: %d", c.Callback.BinPort)
	}
	if c.Callback.MaxConnections < 0 {
		return fmt.Errorf("config: callback.max_connections must be >= 0: %d", c.Callback.MaxConnections)
	}
	if err := validateOperatorSurfaces(c); err != nil {
		return err
	}
	names := make(map[string]struct{}, len(c.Centrals))
	for i := range c.Centrals {
		cc := &c.Centrals[i]
		if cc.Name == "" {
			return fmt.Errorf("config: centrals[%d].name: required", i)
		}
		if _, dup := names[cc.Name]; dup {
			return fmt.Errorf("config: duplicate central name %q", cc.Name)
		}
		names[cc.Name] = struct{}{}
		if cc.Host == "" {
			return fmt.Errorf("config: centrals[%d].host: required", i)
		}
		if err := validateCentralHost(i, cc.Host); err != nil {
			return err
		}
		if cc.Port < 0 || cc.Port > 65535 {
			return fmt.Errorf("config: centrals[%d].port: out of range 0-65535: %d", i, cc.Port)
		}
		if err := validateCentralBehavior(i, &cc.Behavior); err != nil {
			return err
		}
		if len(cc.Interfaces) == 0 {
			return fmt.Errorf("config: centrals[%d].interfaces: required (at least one interface must be listed)", i)
		}
		for j, spec := range cc.Interfaces {
			if err := spec.Validate(j); err != nil {
				return err
			}
		}
		for iface, port := range cc.Ports {
			if port < 1 || port > 65535 {
				return fmt.Errorf("config: centrals[%d].ports[%q]: out of range 1-65535: %d", i, iface, port)
			}
		}
	}
	// Clamp an explicit history.retention below the hourly-rollup lag up to
	// the floor: keeping it lower would let the purge delete raw rows before
	// the hourly fold folds them (permanent loss). Zero is left untouched —
	// it selects the daemon default, which is well above the floor.
	if h := &c.Persistence.History; h.Retention > 0 && h.Retention < HistoryRetentionFloor {
		h.Retention = HistoryRetentionFloor
	}
	return validateMQTT(&c.North.MQTT)
}

// centralHostLabel matches one DNS label. Underscores are tolerated —
// nonstandard, but common on home LANs — and hyphens must stay inside
// the label.
var centralHostLabel = regexp.MustCompile(`^[a-zA-Z0-9_]([a-zA-Z0-9_-]*[a-zA-Z0-9_])?$`)

// validateCentralHost enforces that centrals[].host is a bare hostname
// or IP literal. The value is interpolated into every south-bound URL
// (XML-RPC / JSON-RPC endpoints, the CCU readiness probe), so a scheme,
// path, query, fragment, credentials, or an embedded port must be
// rejected at this trust boundary rather than silently reshaping those
// URLs. The TCP port has its own config field.
func validateCentralHost(idx int, host string) error {
	// IP literal, bare or bracketed (IPv6 URL form).
	candidate := host
	if strings.HasPrefix(candidate, "[") && strings.HasSuffix(candidate, "]") {
		candidate = candidate[1 : len(candidate)-1]
	}
	if net.ParseIP(candidate) != nil {
		return nil
	}
	// Hostname / FQDN (trailing dot allowed).
	trimmed := strings.TrimSuffix(host, ".")
	if trimmed != "" && len(host) <= 253 {
		ok := true
		for _, label := range strings.Split(trimmed, ".") {
			if !centralHostLabel.MatchString(label) {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf(
		"config: centrals[%d].host %q: must be a bare hostname or IP address (no scheme, path, port, or credentials)",
		idx, host,
	)
}

// validatePublicURL checks north.rest.public_url when set. Empty is
// valid (feature off). When present it must be an absolute http/https
// URL with a host — the value is handed to a browser as the "Open
// Config UI" target, so a relative or schemeless string would not be
// reachable from the public side.
// validateOperatorSurfaces groups the checks on operator-facing surface
// settings. They are collected here rather than inlined because Validate
// sits at the cyclomatic-complexity ceiling the linter enforces, and a
// grouped call keeps room for the next one.
func validateOperatorSurfaces(c *Config) error {
	if err := validatePublicURL(c.North.REST.PublicURL); err != nil {
		return err
	}
	if err := validateDuressVisibility(c.Alarm.DuressVisibility); err != nil {
		return err
	}
	if err := validateSurfaceProfiles(&c.North.UI); err != nil {
		return err
	}
	return validateFieldRanges(c)
}

// validateSurfaceProfiles rejects an unknown profile name or an
// unrecognised state.
//
// Surface *ids* are deliberately not validated here: the registry lives
// in the north-bound UI layer, and more importantly a downgrade must not
// fail to boot because a profile stored by a newer release names a view
// this binary does not have. Unknown ids are ignored at resolve time
// instead, which keeps the config forward-compatible.
func validateSurfaceProfiles(ui *NorthUI) error {
	for profile, overrides := range ui.Profiles {
		if profile != ProfileStandalone && profile != ProfileEmbedded {
			return fmt.Errorf("config: north.ui.profiles: unknown profile %q (want %q or %q)",
				profile, ProfileStandalone, ProfileEmbedded)
		}
		for id, state := range overrides {
			if state != SurfaceVisible && state != SurfaceHidden {
				return fmt.Errorf("config: north.ui.profiles.%s.%s: must be %q or %q: %q",
					profile, id, SurfaceVisible, SurfaceHidden, state)
			}
		}
	}
	return nil
}

// validateDuressVisibility rejects an unrecognised covert-trigger level.
//
// It fails loudly rather than falling back: a typo that quietly widened
// duress visibility would be discovered only by the person it endangers.
func validateDuressVisibility(raw string) error {
	if raw == "" || hmenum.DuressVisibility(raw).Valid() {
		return nil
	}
	return fmt.Errorf("config: alarm.duress_visibility must be hidden, notify_only or full: %q", raw)
}

func validatePublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: north.rest.public_url: invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("config: north.rest.public_url: scheme must be http or https, got %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("config: north.rest.public_url: missing host in %q", raw)
	}
	return nil
}

// validateMQTT enforces MQTT-block invariants when MQTT is enabled.
//
// Required when enabled:
//   - mqtt.broker_url — must be non-empty and parse as a valid URL whose
//     scheme is one of tcp, mqtt, tls, ssl, mqtts (empty scheme is also
//     accepted for bare host:port shorthand).
//
// Optional but range-checked:
//   - mqtt.payload_format — must be "", "bare" or "json".
//   - mqtt.topic_base — must be non-empty (filled by applyDefaults so
//     this is only reachable if someone sets it to an explicit empty
//     string in YAML).
//
// Username/password are intentionally not required: anonymous brokers
// are common in development environments. When both are supplied the
// values are passed through as-is; the broker rejects bad credentials.
func validateMQTT(m *NorthMQTT) error {
	if !m.Enabled {
		return nil
	}
	if m.BrokerURL == "" {
		return errors.New("config: mqtt.broker_url: required when mqtt.enabled is true")
	}
	u, err := url.Parse(m.BrokerURL)
	if err != nil {
		return fmt.Errorf("config: mqtt.broker_url: invalid URL: %w", err)
	}
	switch u.Scheme {
	case "tcp", "mqtt", "tls", "ssl", "mqtts", "":
		// all accepted
	default:
		return fmt.Errorf("config: mqtt.broker_url: unsupported scheme %q (use tcp, mqtt, tls, ssl or mqtts)", u.Scheme)
	}
	if u.Hostname() == "" {
		return errors.New("config: mqtt.broker_url: host must not be empty")
	}
	switch m.PayloadFormat {
	case "", "bare", "json":
		// valid
	default:
		return fmt.Errorf("config: mqtt.payload_format: invalid value %q (use bare or json)", m.PayloadFormat)
	}
	if m.TopicBase == "" {
		return errors.New("config: mqtt.topic_base: required")
	}
	return nil
}
