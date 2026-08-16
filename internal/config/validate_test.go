// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// validCentral returns a single central that satisfies every check ahead
// of validateCentralBehavior in [Config.Validate] (name, host, at least
// one interface), so a test that targets one specific field does not
// also trip on an unrelated central-level requirement.
func validCentral() CentralConfig {
	return CentralConfig{
		Name:       "ccu",
		Host:       "192.0.2.10",
		Interfaces: []InterfaceSpec{{Name: "HmIP-RF"}},
	}
}

// assertRejected fails the test unless err is non-nil and its message
// contains want — the point of every case below is not just that a bad
// value is rejected, but that the operator is told which field to fix.
func assertRejected(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Validate() accepted an invalid value, want an error naming %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error does not name the offending field %q: %v", want, err)
	}
}

func assertAccepted(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Validate() rejected a valid value: %v", err)
	}
}

// TestValidateRejectsAnUnknownLocale pins that an unrecognised locale tag
// is refused at save time rather than silently falling through to
// English on every north-bound surface.
func TestValidateRejectsAnUnknownLocale(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		locale  string
		wantErr bool
	}{
		{"unknown tag", "xx", true},
		{"english", "en", false},
		{"german", "de", false},
		{"unset selects the default", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Locale = tc.locale
			err := cfg.Validate()
			if tc.wantErr {
				assertRejected(t, err, "locale")
				return
			}
			assertAccepted(t, err)
		})
	}
}

// TestValidateRejectsAMalformedCallbackPortRange pins that
// callback.port_range is parsed at save time via [ParsePortRange] rather
// than only at bind time, so a malformed value surfaces immediately
// instead of as a boot failure.
func TestValidateRejectsAMalformedCallbackPortRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		portRange string
		wantErr   bool
	}{
		{"empty selects Port", "", false},
		{"a valid range", "30000-30099", false},
		{"not a range at all", "not-a-range", true},
		{"a single port, no dash", "30000", true},
		{"lo greater than hi", "30099-30000", true},
		{"non-numeric bounds", "abc-def", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Callback.PortRange = tc.portRange
			err := cfg.Validate()
			if tc.wantErr {
				assertRejected(t, err, "callback.port_range")
				return
			}
			assertAccepted(t, err)
		})
	}
}

// TestValidateChecksTheWebhookEndpoint pins the outbound webhook block:
// the URL must be a well-formed http(s) URL with a host whenever it is
// set, and TimeoutMs must not be negative.
func TestValidateChecksTheWebhookEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("url", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			url     string
			wantErr bool
		}{
			{"empty is off", "", false},
			{"a valid https URL", "https://h/p", false},
			{"an unsupported scheme", "ftp://h", true},
			{"not a URL at all", "not a url at all", true},
			{"missing host", "//nohost", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.Centrals = []CentralConfig{validCentral()}
				cfg.North.Webhook.URL = tc.url
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.webhook.url")
					return
				}
				assertAccepted(t, err)
			})
		}
	})

	t.Run("timeout_ms", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			ms      int
			wantErr bool
		}{
			{"zero selects the default", 0, false},
			{"a positive timeout", 5000, false},
			{"negative", -1, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.Centrals = []CentralConfig{validCentral()}
				cfg.North.Webhook.TimeoutMs = tc.ms
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.webhook.timeout_ms")
					return
				}
				assertAccepted(t, err)
			})
		}
	})
}

// TestValidateChecksTheMCPMountPath pins that north.mcp.path, when set, is
// something http.ServeMux can register: an absolute mount prefix built from
// unreserved characters. ServeMux rejects a malformed pattern by panicking
// during registration, so a value that gets past this check kills the daemon
// in bring-up — on every start, since the value is persisted.
func TestValidateChecksTheMCPMountPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty selects the default", "", false},
		{"a normal mount", "/mcp", false},
		{"a nested mount", "/api/mcp-v1", false},
		{"no leading slash", "mcp", true},
		{"trailing slash", "/mcp/", true},
		// The mount registers "/" itself as the fall-through to the REST
		// router, so a root MCP mount registers that pattern twice and
		// ServeMux panics with "conflicts with pattern".
		{"root mount", "/", true},
		// A brace is a ServeMux wildcard segment; an unbalanced or
		// non-identifier one panics in the pattern parser.
		{"wildcard brace", "/mcp{id}", true},
		{"embedded space", "/mcp adapter", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.North.MCP.Path = tc.path
			err := cfg.Validate()
			if tc.wantErr {
				assertRejected(t, err, "north.mcp.path")
				return
			}
			assertAccepted(t, err)
		})
	}
}

// TestValidateChecksTheMatterBridgeParameters pins the bridge's listen
// address and the commissioning parameters a certified commissioner
// verifies during PASE — a value outside the accepted window presents to
// the operator as a pairing that silently aborts.
func TestValidateChecksTheMatterBridgeParameters(t *testing.T) {
	t.Parallel()

	t.Run("listen", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			listen  string
			wantErr bool
		}{
			{"empty selects the default", "", false},
			{"bare port", ":5540", false},
			{"host and port", "0.0.0.0:5540", false},
			{"no host:port shape", "5540", true},
			{"non-numeric port", ":notaport", true},
			{"port out of range", ":70000", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.North.Matter.Listen = tc.listen
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.matter.listen")
					return
				}
				assertAccepted(t, err)
			})
		}
	})

	t.Run("discriminator", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name          string
			discriminator uint16
			wantErr       bool
		}{
			{"zero", 0, false},
			{"a mid-range value", 0xF00, false},
			{"the 12-bit ceiling", 0xFFF, false},
			{"one past the 12-bit ceiling", 0x1000, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.North.Matter.Discriminator = tc.discriminator
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.matter.discriminator")
					return
				}
				assertAccepted(t, err)
			})
		}
	})

	t.Run("commissioning_iterations", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name       string
			iterations int
			wantErr    bool
		}{
			{"zero selects the default", 0, false},
			{"a valid iteration count", 1000, false},
			{"the upper bound", 100000, false},
			{"just below the lower bound", 999, true},
			{"just above the upper bound", 100001, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.North.Matter.Commissioning.Iterations = tc.iterations
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.matter.commissioning.iterations")
					return
				}
				assertAccepted(t, err)
			})
		}
	})
}

// TestValidateChecksTheRESTCapacityKnobs pins the REST rate-limiter and
// WebSocket replay-buffer knobs: all three treat zero as "use the
// default", so only a negative (or non-finite) value is wrong — and a
// bad one used to be silently clamped at the point of use, leaving the
// operator with a limiter they never configured.
func TestValidateChecksTheRESTCapacityKnobs(t *testing.T) {
	t.Parallel()

	t.Run("requests_per_second", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			rps     float64
			wantErr bool
		}{
			{"zero selects the default", 0, false},
			{"a fractional rate", 10.5, false},
			{"negative", -1, true},
			{"not a number", math.NaN(), true},
			{"positive infinity", math.Inf(1), true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.North.REST.RateLimit.RequestsPerSecond = tc.rps
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.rest.rate_limit.requests_per_second")
					return
				}
				assertAccepted(t, err)
			})
		}
	})

	t.Run("burst", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			burst   int
			wantErr bool
		}{
			{"zero selects the default", 0, false},
			{"negative", -1, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.North.REST.RateLimit.Burst = tc.burst
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.rest.rate_limit.burst")
					return
				}
				assertAccepted(t, err)
			})
		}
	})

	t.Run("ws_replay_capacity", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name     string
			capacity int
			wantErr  bool
		}{
			{"zero disables replay", 0, false},
			{"a configured buffer", 1024, false},
			{"negative", -1, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := Default()
				cfg.North.REST.WS.ReplayCapacity = tc.capacity
				err := cfg.Validate()
				if tc.wantErr {
					assertRejected(t, err, "north.rest.ws.replay_capacity")
					return
				}
				assertAccepted(t, err)
			})
		}
	})
}

// TestValidateRejectsNegativeDurations is table-driven over one setter per
// duration knob reachable from Config, each pinning that a negative value
// is rejected by name. The reflective walk in [validateNonNegativeDurations]
// exists so nobody has to remember to extend this table when a new
// duration knob is added — [TestEveryDurationLeafIsReachedByTheNegativeCheck]
// is the guard for that; this table instead pins the field-path spelling
// of a representative knob from each config section.
func TestValidateRejectsNegativeDurations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		path  string
		apply func(c *Config)
	}{
		{
			name: "backup schedule",
			path: "backup.schedule",
			apply: func(c *Config) {
				c.Backup.Schedule = -time.Second
			},
		},
		{
			name: "addon-update check interval",
			path: "addon_update.check_interval",
			apply: func(c *Config) {
				c.AddonUpdate.CheckInterval = -time.Second
			},
		},
		{
			name: "history retention",
			path: "persistence.history.retention",
			apply: func(c *Config) {
				c.Persistence.History.Retention = -time.Second
			},
		},
		{
			name: "history flush interval",
			path: "persistence.history.flush_interval",
			apply: func(c *Config) {
				c.Persistence.History.FlushInterval = -time.Second
			},
		},
		{
			name: "command retry initial delay",
			path: "reliability.command_retry_initial_delay",
			apply: func(c *Config) {
				c.Reliability.CommandRetryInitialDelay = -time.Second
			},
		},
		{
			name: "command throttle inter-command delay",
			path: "reliability.command_throttle_inter_command_delay",
			apply: func(c *Config) {
				c.Reliability.CommandThrottleInterCommandDelay = -time.Second
			},
		},
		{
			name: "per-central sysvar scan interval",
			path: "centrals[0].behavior.sysvar_scan_interval",
			apply: func(c *Config) {
				c.Centrals = []CentralConfig{validCentral()}
				c.Centrals[0].Behavior.SysvarScanInterval = -time.Second
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			tc.apply(cfg)
			err := cfg.Validate()
			assertRejected(t, err, tc.path)
		})
	}
}

// TestValidateAcceptsTheDefaultConfig pins that [Default] validates
// cleanly — the safety net against a new validator rejecting every
// unconfigured boot.
func TestValidateAcceptsTheDefaultConfig(t *testing.T) {
	t.Parallel()
	assertAccepted(t, Default().Validate())
}

// TestEveryDurationLeafIsReachedByTheNegativeCheck builds a Config with
// every reachable time.Duration leaf set to -1 and asserts
// [validateNonNegativeDurations] reports at least one offending path per
// leaf. The reflective walk in production code is what keeps a newly
// added duration knob covered without anyone remembering to extend a
// hand-written list — this test is the guard that the walk itself still
// reaches everything the struct declares, so a refactor that changes the
// struct shape (a new nested block, a renamed embedded field) cannot
// quietly shrink the set walkDurationLeaves visits.
func TestEveryDurationLeafIsReachedByTheNegativeCheck(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Centrals = []CentralConfig{validCentral()}

	leaves := 0
	setAllDurationLeavesNegative(reflect.ValueOf(cfg), &leaves)
	if leaves == 0 {
		t.Fatal("setAllDurationLeavesNegative found no time.Duration leaf — the reflective walk helper is broken")
	}

	err := validateNonNegativeDurations(cfg)
	if err == nil {
		t.Fatalf("validateNonNegativeDurations found no negative duration among %d leaves set to -1", leaves)
	}

	// A leaf whose negative value is a documented instruction is exempt by
	// design; every other leaf must be reported. The exemption list is part
	// of the contract, so counting against it keeps a silently growing list
	// from eroding the coverage this test exists to hold.
	exempt := len(durationsWhereNegativeIsMeaningful) * len(cfg.Centrals)
	got := strings.Count(err.Error(), "(-1ns)")
	if got < leaves-exempt {
		t.Errorf("validateNonNegativeDurations reported %d offending paths, want at least %d "+
			"(%d duration leaves minus %d documented exemptions)", got, leaves-exempt, leaves, exempt)
	}
}

// TestNegativeCheckExemptsTheDocumentedDisableSentinel pins the one
// duration whose negative value is an instruction: a per-central
// check_connection_interval below zero switches the poll off, and
// rejecting it would remove the only way to express that (zero already
// means "use the default").
func TestNegativeCheckExemptsTheDocumentedDisableSentinel(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Centrals = []CentralConfig{validCentral()}
	cfg.Centrals[0].CheckConnectionInterval = -time.Second

	if err := cfg.Validate(); err != nil {
		t.Fatalf("a negative check_connection_interval disables the poll and must validate: %v", err)
	}
}

// setAllDurationLeavesNegative walks rv exactly like
// [walkDurationLeaves] and sets every time.Duration leaf it finds to -1,
// counting how many it touched. It is a separate, deliberately
// mechanical walk (rather than a call into production code) so the test
// does not depend on validateNonNegativeDurations to discover its own
// leaves — that would make the assertion trivially true.
func setAllDurationLeavesNegative(rv reflect.Value, count *int) {
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		rt := rv.Type()
		for i := range rt.NumField() {
			if !rt.Field(i).IsExported() {
				continue
			}
			setAllDurationLeavesNegative(rv.Field(i), count)
		}
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			setAllDurationLeavesNegative(rv.Index(i), count)
		}
	default:
		// time.Duration's Kind() is Int64, so it lands here rather than
		// in the Struct case above — mirrors walkDurationLeaves.
		if rv.Type() == durationType && rv.CanSet() {
			rv.SetInt(-1)
			*count++
		}
	}
}

// TestValidateRejectsNonFiniteFloats pins the finite-float rule on a leaf that
// has no range check of its own. YAML spells `.nan` and `.inf`, both survive
// parsing, and both are unrepresentable in JSON — the encoding every deep copy
// of the config goes through. Accepting one would make [Clone] fail on a
// config the operator wrote and validated.
func TestValidateRejectsNonFiniteFloats(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		price float64
		want  bool
	}{
		{"a real tariff", 0.32, false},
		{"unset", 0, false},
		{"not a number", math.NaN(), true},
		{"positive infinity", math.Inf(1), true},
		{"negative infinity", math.Inf(-1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Centrals = []CentralConfig{validCentral()}
			cfg.Persistence.History.EnergyPricePerKWh = tc.price
			err := cfg.Validate()
			if tc.want {
				assertRejected(t, err, "persistence.history.energy_price_per_kwh")
				return
			}
			assertAccepted(t, err)
		})
	}
}

// TestParseRejectsNonFiniteFloatLiteral crosses the YAML boundary the value
// actually enters through: `.nan` is a legal YAML float, so only Validate can
// keep it out of a running config.
func TestParseRejectsNonFiniteFloatLiteral(t *testing.T) {
	t.Parallel()

	const doc = `
persistence:
  history:
    energy_price_per_kwh: .nan
centrals:
  - name: ccu1
    host: 192.0.2.10
    interfaces:
      - HmIP-RF
`
	if _, err := Parse([]byte(doc)); err == nil {
		t.Fatal("Parse accepted energy_price_per_kwh: .nan")
	} else if !strings.Contains(err.Error(), "energy_price_per_kwh") {
		t.Errorf("error does not name the offending field: %v", err)
	}
}
