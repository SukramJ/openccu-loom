// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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
	Reliability ReliabilityConfig `yaml:"reliability,omitempty" json:"reliability,omitempty" cfg:"expert"`
	Persistence PersistenceConfig `yaml:"persistence,omitempty" json:"persistence,omitempty" cfg:"expert"`
}

// PersistenceConfig groups the cross-cutting persistence-tuning knobs
// that the daemon's SQLite-backed caches expose to operators. Each
// block defaults to "feature on, sensible defaults"; zero-valued
// fields fall back to the hard-coded constants the wiring uses.
//
// Today only [PersistenceConfig.ValuesCache] is wired through. Future
// caches (e.g. linkprofile snapshots) get their own sub-block here.
type PersistenceConfig struct {
	ValuesCache ValuesCacheConfig `yaml:"values_cache,omitempty" json:"values_cache,omitempty" cfg:"expert"`
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
	for _, name := range c.DisabledCentrals {
		if name == centralName {
			return false
		}
	}
	return true
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
type CallbackConfig struct {
	Host       string `yaml:"host" json:"host" cfg:"expert"`
	Port       int    `yaml:"port" json:"port" cfg:"expert"`               // XML-RPC; 0 = dynamic
	BinPort    int    `yaml:"bin_port" json:"bin_port" cfg:"expert"`       // BIN-RPC; 0 = dynamic
	PortRange  string `yaml:"port_range" json:"port_range" cfg:"expert"`   // e.g. "30000-30099"
	PublicHost string `yaml:"public_host" json:"public_host" cfg:"expert"` // optional NAT override
}

// NorthConfig bundles north-bound server settings.
type NorthConfig struct {
	REST      NorthREST      `yaml:"rest" json:"rest" cfg:"basic"`
	UI        NorthUI        `yaml:"ui" json:"ui" cfg:"basic"`
	MQTT      NorthMQTT      `yaml:"mqtt" json:"mqtt" cfg:"basic"`
	Matter    NorthMatter    `yaml:"matter" json:"matter" cfg:"basic"`
	Discovery NorthDiscovery `yaml:"discovery" json:"discovery" cfg:"basic"`
}

// NorthDiscovery groups LAN-discovery surfaces external clients use
// to locate the daemon without manual configuration. See ADR 0021.
type NorthDiscovery struct {
	MDNS NorthDiscoveryMDNS `yaml:"mdns" json:"mdns" cfg:"basic"`
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
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
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
	//   - "" or "noop": in-memory only, no multicast traffic. Fine for
	//     unit tests and when discovery is handled out-of-band.
	//   - "zeroconf": multicast `_matter._tcp` + `_matterc._udp` records
	//     so chip-tool's `pairing` finds the bridge by service-type
	//     instead of needing an explicit IP/port. The commissionable
	//     record is published with `_L<long>` / `_S<short>` /
	//     `_V<vendor>` subtypes whenever a commissioning window is
	//     open and withdrawn on close.
	MDNSAdvertise string `yaml:"mdns_advertise" json:"mdns_advertise" cfg:"expert"`

	// Commissioning configures the PASE acceptor. When Passcode is
	// non-zero the daemon stands up a Spake2+ verifier wired into
	// the bridge's PASE port; commissioner Pake1 traffic completes
	// against it and Pake3 success registers a fresh PASE session
	// with the operational manager. Passcode=0 leaves the PASE port
	// at noop (commissioners get debug-logged drops).
	Commissioning NorthMatterCommissioning `yaml:"commissioning" json:"commissioning" cfg:"basic"`

	// CASE configures the operational-session responder. When NodeID
	// is non-zero the daemon constructs a sigma responder with an
	// ephemeral identity (development-only) and wires it into the
	// bridge's CASE port. Persistent fabric identity (NOC + ICAC +
	// stable private key) is a post-0.1.0 follow-up; until then the CASE
	// path is structurally wired but cannot complete with a
	// production controller that validates the bridge's certificate
	// chain.
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

// NorthREST configures the REST+WS server.
type NorthREST struct {
	Listen string   `yaml:"listen" json:"listen" cfg:"basic"`
	CORS   []string `yaml:"cors" json:"cors" cfg:"basic"`
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
	if n.OpenAPIValidate == nil {
		return true
	}
	return *n.OpenAPIValidate
}

// IsEnabled reports whether the REST + WebSocket server should run.
// nil → true (the default), so an operator only has to set
// `enabled: false` to opt out.
func (n NorthREST) IsEnabled() bool {
	if n.Enabled == nil {
		return true
	}
	return *n.Enabled
}

// NorthUI configures the HTMX Config UI.
type NorthUI struct {
	// Enabled is the master switch for the HTMX-bootstrap UI
	// (login, /setup wizard, /health, /about). Defaults to true
	// when absent — without it the SPA cannot offer pre-auth
	// flows and the daemon cannot drive a first-run setup. Use
	// *bool so the YAML decoder can distinguish "not set" from
	// "explicitly false".
	Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty" cfg:"basic"`
	Listen  string `yaml:"listen" json:"listen" cfg:"basic"`
}

// IsEnabled reports whether the bootstrap UI should run. nil → true.
func (n NorthUI) IsEnabled() bool {
	if n.Enabled == nil {
		return true
	}
	return *n.Enabled
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
}

// AuthConfig collects auth-related switches.
type AuthConfig struct {
	BasicEnabled   bool              `yaml:"basic_enabled" json:"basic_enabled" cfg:"basic"`
	BearerEnabled  bool              `yaml:"bearer_enabled" json:"bearer_enabled" cfg:"basic"`
	SessionEnabled bool              `yaml:"session_enabled" json:"session_enabled" cfg:"basic"`
	Users          map[string]string `yaml:"users" json:"users" cfg:"secret"`   // username → bcrypt hash (MVP: plaintext)
	Tokens         map[string]string `yaml:"tokens" json:"tokens" cfg:"secret"` // token → role
	OIDC           OIDCConfig        `yaml:"oidc" json:"oidc" cfg:"basic"`
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
	buf, err := os.ReadFile(path) //nolint:gosec // path comes from operator-supplied CLI arg
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
	if c.North.REST.Listen == "" {
		c.North.REST.Listen = ":8080"
	}
	if c.North.UI.Listen == "" {
		c.North.UI.Listen = ":8081"
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
	if n.CSRFEnabled == nil {
		return true
	}
	return *n.CSRFEnabled
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
		if cc.Port < 0 || cc.Port > 65535 {
			return fmt.Errorf("config: centrals[%d].port: out of range 0-65535: %d", i, cc.Port)
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
	return validateMQTT(&c.North.MQTT)
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
		return fmt.Errorf("config: mqtt.broker_url: required when mqtt.enabled is true")
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
		return fmt.Errorf("config: mqtt.broker_url: host must not be empty")
	}
	switch m.PayloadFormat {
	case "", "bare", "json":
		// valid
	default:
		return fmt.Errorf("config: mqtt.payload_format: invalid value %q (use bare or json)", m.PayloadFormat)
	}
	if m.TopicBase == "" {
		return fmt.Errorf("config: mqtt.topic_base: required")
	}
	return nil
}
