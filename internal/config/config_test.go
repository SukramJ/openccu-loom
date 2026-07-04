// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// minimalCentralYAML is a single-line YAML fragment for a valid central.
const minimalCentralYAML = `
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF]
`

func TestDefaults(t *testing.T) {
	c := Default()
	if c.Locale != "en" || c.Logging.Level != "info" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Callback.Port != 8120 || c.Callback.BinPort != 8129 {
		t.Fatalf("callback: %+v", c.Callback)
	}
}

func TestParseValidYAML(t *testing.T) {
	buf := []byte(`
locale: de
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF]
`)
	c, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Locale != "de" || len(c.Centrals) != 1 || c.Centrals[0].Name != "ccu-01" {
		t.Fatalf("parsed: %+v", c)
	}
}

func TestParseDuplicateCentralRejected(t *testing.T) {
	buf := []byte(`
centrals:
  - name: a
    host: h1
    interfaces: [HmIP-RF]
  - name: a
    host: h2
    interfaces: [HmIP-RF]
`)
	if _, err := Parse(buf); err == nil {
		t.Fatal("duplicate central name must fail")
	}
}

func TestParseInvalidLogLevel(t *testing.T) {
	buf := []byte(`logging: {level: lol, format: json}`)
	if _, err := Parse(buf); err == nil || !strings.Contains(err.Error(), "logging.level") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseInvalidLogFormat(t *testing.T) {
	t.Parallel()
	buf := []byte(`logging: {level: info, format: rainbow}`)
	if _, err := Parse(buf); err == nil || !strings.Contains(err.Error(), "logging.format") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseLogFormatTextColorAccepted(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `
logging:
  level: info
  format: text-color
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("text-color must validate: %v", err)
	}
	if cfg.Logging.Format != "text-color" {
		t.Fatalf("format = %q, want text-color", cfg.Logging.Format)
	}
}

func TestLoadEmptyPath(t *testing.T) {
	if _, err := Load(""); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// CCU / central validation
// ---------------------------------------------------------------------------

func TestCentralInterfacesRequired(t *testing.T) {
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: interfaces required")
	}
	if !strings.Contains(err.Error(), "interfaces") {
		t.Fatalf("error should mention 'interfaces', got: %v", err)
	}
}

func TestCentralHostRequired(t *testing.T) {
	buf := []byte(`
centrals:
  - name: ccu-01
    interfaces: [HmIP-RF]
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: host required")
	}
	if !strings.Contains(err.Error(), "host") {
		t.Fatalf("error should mention 'host', got: %v", err)
	}
}

func TestCentralNameRequired(t *testing.T) {
	buf := []byte(`
centrals:
  - host: 192.168.1.10
    interfaces: [HmIP-RF]
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: name required")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Fatalf("error should mention 'name', got: %v", err)
	}
}

func TestCentralPortOutOfRange(t *testing.T) {
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF]
    port: 99999
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: port out of range")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("error should mention 'port', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MQTT validation
// ---------------------------------------------------------------------------

func TestMQTTDisabledNoValidation(t *testing.T) {
	// When mqtt.enabled is false the broker_url is not required.
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: false
`)
	if _, err := Parse(buf); err != nil {
		t.Fatalf("disabled MQTT should not require broker_url: %v", err)
	}
}

func TestMQTTEnabledBrokerURLRequired(t *testing.T) {
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: broker_url required")
	}
	if !strings.Contains(err.Error(), "broker_url") {
		t.Fatalf("error should mention 'broker_url', got: %v", err)
	}
}

func TestMQTTEnabledValidTCPURL(t *testing.T) {
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tcp://192.168.1.5:1883
`)
	if _, err := Parse(buf); err != nil {
		t.Fatalf("valid tcp broker_url should be accepted: %v", err)
	}
}

func TestMQTTEnabledValidTLSURL(t *testing.T) {
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tls://mqtt.example.com:8883
`)
	if _, err := Parse(buf); err != nil {
		t.Fatalf("valid tls broker_url should be accepted: %v", err)
	}
}

func TestMQTTUnsupportedSchemeRejected(t *testing.T) {
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: ws://192.168.1.5:9001
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: unsupported scheme")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error should mention 'scheme', got: %v", err)
	}
}

func TestMQTTInvalidPayloadFormat(t *testing.T) {
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tcp://192.168.1.5:1883
    payload_format: xml
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: invalid payload_format")
	}
	if !strings.Contains(err.Error(), "payload_format") {
		t.Fatalf("error should mention 'payload_format', got: %v", err)
	}
}

func TestMQTTValidPayloadFormats(t *testing.T) {
	for _, pf := range []string{"", "bare", "json"} {
		buf := minimalCentralYAML + "\nnorth:\n  mqtt:\n    enabled: true\n    broker_url: tcp://192.168.1.5:1883\n    payload_format: " + pf + "\n"
		if _, err := Parse([]byte(buf)); err != nil {
			t.Fatalf("payload_format %q should be valid: %v", pf, err)
		}
	}
}

func TestMQTTAnonymousNoBrokerCredentials(t *testing.T) {
	// Anonymous brokers: username + password are not required.
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tcp://192.168.1.5:1883
`)
	if _, err := Parse(buf); err != nil {
		t.Fatalf("anonymous MQTT (no username/password) should be accepted: %v", err)
	}
}

func TestMQTTTopicBaseDefaultApplied(t *testing.T) {
	// When topic_base is absent (or set to "") applyDefaults fills "openccu-loom";
	// validation must accept that and not return an error.
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tcp://192.168.1.5:1883
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("expected no error with default topic_base: %v", err)
	}
	if cfg.North.MQTT.TopicBase != "openccu-loom" {
		t.Fatalf("expected default topic_base 'openccu-loom', got: %q", cfg.North.MQTT.TopicBase)
	}
}

func TestMQTTTopicBaseCustomAccepted(t *testing.T) {
	// A non-empty operator-supplied topic_base must be preserved and pass validation.
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tcp://192.168.1.5:1883
    topic_base: home/hm
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("custom topic_base should be accepted: %v", err)
	}
	if cfg.North.MQTT.TopicBase != "home/hm" {
		t.Fatalf("expected topic_base 'home/hm', got: %q", cfg.North.MQTT.TopicBase)
	}
}

func TestMQTTBrokerURLMissingHost(t *testing.T) {
	// Scheme present but no host (e.g. "tcp:///").
	buf := []byte(minimalCentralYAML + `
north:
  mqtt:
    enabled: true
    broker_url: tcp:///path-only
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error: broker_url host required")
	}
}

// ---------------------------------------------------------------------------
// InterfaceSpec — YAML unmarshalling + validation
// ---------------------------------------------------------------------------

// TestInterfaceSpecShortForm verifies that a plain YAML string is decoded
// as an InterfaceSpec with only Name set.
func TestInterfaceSpecShortForm(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF, BidCos-RF]
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	specs := cfg.Centrals[0].Interfaces
	if len(specs) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(specs))
	}
	if specs[0].Name != "HmIP-RF" {
		t.Errorf("interfaces[0].Name: want HmIP-RF, got %q", specs[0].Name)
	}
	if specs[0].Port != 0 || specs[0].RPCType != "" || specs[0].RemotePath != "" {
		t.Errorf("interfaces[0] overrides should be zero, got %+v", specs[0])
	}
	if specs[1].Name != "BidCos-RF" {
		t.Errorf("interfaces[1].Name: want BidCos-RF, got %q", specs[1].Name)
	}
}

// TestInterfaceSpecLongForm verifies that a YAML mapping is decoded
// with all fields populated.
func TestInterfaceSpecLongForm(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces:
      - name: HmIP-RF
        port: 12345
        remote_path: /rpc
        rpc_type: xmlrpc
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	spec := cfg.Centrals[0].Interfaces[0]
	if spec.Name != "HmIP-RF" {
		t.Errorf("Name: want HmIP-RF, got %q", spec.Name)
	}
	if spec.Port != 12345 {
		t.Errorf("Port: want 12345, got %d", spec.Port)
	}
	if spec.RemotePath != "/rpc" {
		t.Errorf("RemotePath: want /rpc, got %q", spec.RemotePath)
	}
	if spec.RPCType != "xmlrpc" {
		t.Errorf("RPCType: want xmlrpc, got %q", spec.RPCType)
	}
}

// TestInterfaceSpecMixedForm verifies that a list mixing strings and
// mappings is accepted without error.
func TestInterfaceSpecMixedForm(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces:
      - HmIP-RF
      - name: BidCos-RF
        port: 22000
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	specs := cfg.Centrals[0].Interfaces
	if len(specs) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(specs))
	}
	if specs[0].Name != "HmIP-RF" || specs[0].Port != 0 {
		t.Errorf("short-form entry: %+v", specs[0])
	}
	if specs[1].Name != "BidCos-RF" || specs[1].Port != 22000 {
		t.Errorf("long-form entry: %+v", specs[1])
	}
}

// TestInterfaceSpecEmptyNameFails verifies that an interface spec with an
// empty name is rejected by Validate().
func TestInterfaceSpecEmptyNameFails(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces:
      - name: ""
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error for empty interface name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention 'name', got: %v", err)
	}
}

// TestHmIPWiredRejectedAsInterfaceName locks in the §D guard: the CCU
// has no separate HmIP-Wired service. Configuring "HmIP-Wired" as an
// interface would build a duplicate XML-RPC client against the shared
// HmIP-RF port. The validator rejects it with a message that points
// the operator at the ProductGroup-based classification path.
func TestHmIPWiredRejectedAsInterfaceName(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces:
      - name: HmIP-Wired
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error for HmIP-Wired interface name")
	}
	for _, want := range []string{"HmIP-Wired", "HmIP-RF", "HMIPW"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestInterfaceSpecPortOutOfRange verifies that a port value outside
// 1-65535 is rejected.
func TestInterfaceSpecPortOutOfRange(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces:
      - name: HmIP-RF
        port: 99999
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error for port out of range")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("error should mention 'port', got: %v", err)
	}
}

// TestInterfaceSpecInvalidRPCType verifies that an unrecognised rpc_type
// string is rejected.
func TestInterfaceSpecInvalidRPCType(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces:
      - name: HmIP-RF
        rpc_type: grpc
`)
	_, err := Parse(buf)
	if err == nil {
		t.Fatal("expected error for invalid rpc_type")
	}
	if !strings.Contains(err.Error(), "rpc_type") {
		t.Errorf("error should mention 'rpc_type', got: %v", err)
	}
}

// TestInterfaceSpecMultiCCUValidate verifies that two centrals can each
// list different interface sets, and that duplicate interface names within
// one central are valid (same name, different overrides is allowed by
// the spec — the backend dispatching layer deduplicates).
func TestInterfaceSpecMultiCCUValidate(t *testing.T) {
	t.Parallel()
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF, BidCos-RF]
  - name: ccu-02
    host: 192.168.1.20
    interfaces: [HmIP-RF, VirtualDevices]
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Centrals) != 2 {
		t.Fatalf("expected 2 centrals, got %d", len(cfg.Centrals))
	}
	if len(cfg.Centrals[0].Interfaces) != 2 {
		t.Errorf("ccu-01 interfaces count: want 2, got %d", len(cfg.Centrals[0].Interfaces))
	}
	if len(cfg.Centrals[1].Interfaces) != 2 {
		t.Errorf("ccu-02 interfaces count: want 2, got %d", len(cfg.Centrals[1].Interfaces))
	}
}

// TestInterfaceSpecYAMLRoundtrip verifies that marshalling an InterfaceSpec
// back to YAML and re-parsing it produces the same value.
func TestInterfaceSpecYAMLRoundtrip(t *testing.T) {
	t.Parallel()
	original := InterfaceSpec{
		Name:       "BidCos-RF",
		Port:       12345,
		RemotePath: "/rpc2",
		RPCType:    "xmlrpc",
	}
	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded InterfaceSpec
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", decoded, original)
	}
}

// TestInterfaceSpecBackwardsCompatShortList verifies that the
// backwards-compatible shorthand `interfaces: [HmIP-RF]` (previously
// parsed as []string) still works after the migration to []InterfaceSpec.
func TestInterfaceSpecBackwardsCompatShortList(t *testing.T) {
	t.Parallel()
	// This is the exact YAML that pre-existed in config_test.go
	// (minimalCentralYAML). It must parse without error.
	buf := []byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF]
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("backwards-compat short list failed: %v", err)
	}
	if len(cfg.Centrals[0].Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(cfg.Centrals[0].Interfaces))
	}
	if cfg.Centrals[0].Interfaces[0].Name != "HmIP-RF" {
		t.Errorf("Name: want HmIP-RF, got %q", cfg.Centrals[0].Interfaces[0].Name)
	}
}

// mDNS discovery defaults to enabled (opt-out) per ADR-0021.
func TestMDNSDefaultsToEnabled(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.North.Discovery.MDNS.IsEnabled() {
		t.Fatalf("mDNS default should be enabled; got disabled")
	}
}

func TestMDNSExplicitOptOut(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `
north:
  discovery:
    mdns:
      enabled: false
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.North.Discovery.MDNS.IsEnabled() {
		t.Fatalf("expected mDNS disabled after explicit opt-out")
	}
}

func TestWSReplayCapacity_DefaultsTo1024(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.North.REST.WS.ReplayCapacity; got != 1024 {
		t.Fatalf("ReplayCapacity default = %d, want 1024", got)
	}
}

func TestWSReplayCapacity_Override(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `
north:
  rest:
    ws:
      replay_capacity: 4096
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.North.REST.WS.ReplayCapacity; got != 4096 {
		t.Fatalf("ReplayCapacity = %d, want 4096", got)
	}
}

// TestRetainCleanupWindowDefault verifies that zero / unset falls back to
// the 2 s default and that the result satisfies the clamping bounds.
func TestRetainCleanupWindowDefault(t *testing.T) {
	t.Parallel()
	m := NorthMQTT{}
	got := m.EffectiveRetainCleanupWindow()
	const want = 2000 * 1e6 // 2 s in nanoseconds
	if int64(got) != want {
		t.Fatalf("default window = %v, want 2s", got)
	}
}

// TestRetainCleanupWindowClampLow verifies that values below 500 ms are raised
// to 500 ms.
func TestRetainCleanupWindowClampLow(t *testing.T) {
	t.Parallel()
	m := NorthMQTT{RetainCleanupWindowMs: 100}
	got := m.EffectiveRetainCleanupWindow()
	const want = 500 * 1e6 // 500 ms in nanoseconds
	if int64(got) != want {
		t.Fatalf("clamped low window = %v, want 500ms", got)
	}
}

// TestRetainCleanupWindowClampHigh verifies that values above 30 000 ms are
// lowered to 30 000 ms.
func TestRetainCleanupWindowClampHigh(t *testing.T) {
	t.Parallel()
	m := NorthMQTT{RetainCleanupWindowMs: 999999}
	got := m.EffectiveRetainCleanupWindow()
	const want = 30000 * 1e6 // 30 s in nanoseconds
	if int64(got) != want {
		t.Fatalf("clamped high window = %v, want 30s", got)
	}
}

// TestRetainCleanupWindowCustom verifies that a valid in-range value is
// preserved unchanged.
func TestRetainCleanupWindowCustom(t *testing.T) {
	t.Parallel()
	m := NorthMQTT{RetainCleanupWindowMs: 5000}
	got := m.EffectiveRetainCleanupWindow()
	const want = 5000 * 1e6 // 5 s in nanoseconds
	if int64(got) != want {
		t.Fatalf("custom window = %v, want 5s", got)
	}
}

func TestMDNSInstanceNameOverride(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `
north:
  discovery:
    mdns:
      instance_name: my-hm
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.North.Discovery.MDNS.InstanceName; got != "my-hm" {
		t.Fatalf("instance_name = %q, want my-hm", got)
	}
	if !cfg.North.Discovery.MDNS.IsEnabled() {
		t.Fatalf("override without enabled key must still default-on")
	}
}

func TestAuthSchemeGateAccessors(t *testing.T) {
	t.Parallel()
	var a AuthConfig
	if !a.BasicAuthEnabled() || !a.BearerAuthEnabled() {
		t.Fatal("unset flags must default to enabled")
	}
	off, on := false, true
	a.BasicEnabled, a.BearerEnabled = &off, &off
	if a.BasicAuthEnabled() || a.BearerAuthEnabled() {
		t.Fatal("explicit false must disable the scheme")
	}
	a.BasicEnabled, a.BearerEnabled = &on, &on
	if !a.BasicAuthEnabled() || !a.BearerAuthEnabled() {
		t.Fatal("explicit true must keep the scheme enabled")
	}
}

func TestAuthSchemeGatesFromYAML(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(minimalCentralYAML + `
north:
  rest:
    auth:
      basic_enabled: false
      bearer_enabled: false
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.North.REST.Auth.BasicAuthEnabled() || cfg.North.REST.Auth.BearerAuthEnabled() {
		t.Fatal("yaml false must disable both schemes")
	}
	cfg, err = Parse([]byte(minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.North.REST.Auth.BasicAuthEnabled() || !cfg.North.REST.Auth.BearerAuthEnabled() {
		t.Fatal("absent keys must stay enabled (default-on)")
	}
}
