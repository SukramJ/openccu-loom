// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestRESTSectionAuthGateResolution walks the three payload shapes a
// `north.rest` row can have through the real overlay path and pins what each
// one means for the two auth gates the REST middleware consults.
//
// The middle case is the one that cost an upgrade: while the switches were
// unread plain bools every stored row carried them as literal false, and
// nothing distinguished that from an operator's decision once they became
// tri-state gates. The row shape produced by
// migrations/038_config_sections_auth_gates.sql is the first case — keys
// removed, so the documented nil default applies.
func TestRESTSectionAuthGateResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		payload    string
		wantBasic  bool
		wantBearer bool
	}{
		{
			name:       "no gate keys resolves to the enabled default",
			payload:    `{"enabled":true,"public_url":"https://loom.example"}`,
			wantBasic:  true,
			wantBearer: true,
		},
		{
			name:       "explicit false rejects the scheme",
			payload:    `{"enabled":true,"auth":{"basic_enabled":false,"bearer_enabled":false}}`,
			wantBasic:  false,
			wantBearer: false,
		},
		{
			name:       "explicit true keeps the scheme",
			payload:    `{"enabled":true,"auth":{"basic_enabled":true,"bearer_enabled":true}}`,
			wantBasic:  true,
			wantBearer: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			if err := ApplySectionToConfig(SectionREST, []byte(tc.payload), cfg); err != nil {
				t.Fatalf("ApplySectionToConfig: %v", err)
			}
			cfg.ApplyDefaults()
			if got := cfg.North.REST.Auth.BasicAuthEnabled(); got != tc.wantBasic {
				t.Errorf("BasicAuthEnabled() = %v, want %v", got, tc.wantBasic)
			}
			if got := cfg.North.REST.Auth.BearerAuthEnabled(); got != tc.wantBearer {
				t.Errorf("BearerAuthEnabled() = %v, want %v", got, tc.wantBearer)
			}
		})
	}
}

// TestRESTSectionRowNeverCarriesTheRemovedSessionGate pins the invariant the
// repair migration keys on. `session_enabled` was removed by the same change
// that made basic_enabled/bearer_enabled load-bearing, so its presence in a
// stored row dates that row to before the semantic change — which is what lets
// the migration tell "a value nobody chose" from "the operator switched this
// scheme off". Reintroducing a field with that json name would make the
// migration rewrite live rows, so the name must stay retired.
func TestRESTSectionRowNeverCarriesTheRemovedSessionGate(t *testing.T) {
	t.Parallel()

	const retired = "session_enabled"

	cfg := config.Default()
	cfg.North.REST.Auth.BasicEnabled = boolPtr(false)
	cfg.North.REST.Auth.BearerEnabled = boolPtr(true)
	raw, ok, err := marshalSection(SectionREST, cfg)
	if !ok || err != nil {
		t.Fatalf("marshalSection: ok=%v err=%v", ok, err)
	}
	if strings.Contains(string(raw), retired) {
		t.Errorf("north.rest row carries the retired %q key: %s", retired, raw)
	}

	// The struct itself, not just one marshal of it: a field added anywhere
	// under AuthConfig with that json name would re-enter the payload.
	rt := reflect.TypeOf(config.AuthConfig{})
	for i := range rt.NumField() {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if key == retired {
			t.Errorf("config.AuthConfig.%s reuses the retired json key %q; "+
				"migrations/038_config_sections_auth_gates.sql keys on its absence",
				rt.Field(i).Name, retired)
		}
	}
}

// TestRESTSectionRowOmitsUnsetAuthGates pins the other half of the shape the
// migration restores: with the gates unset, a freshly written row must not
// carry them at all. A row that spelled out `false` for an unset gate would
// disable the scheme on the next boot — that is exactly the defect the
// migration repairs, and omitempty on the pointers is what prevents it
// recurring.
func TestRESTSectionRowOmitsUnsetAuthGates(t *testing.T) {
	t.Parallel()

	raw, ok, err := marshalSection(SectionREST, config.Default())
	if !ok || err != nil {
		t.Fatalf("marshalSection: ok=%v err=%v", ok, err)
	}
	for _, key := range []string{"basic_enabled", "bearer_enabled"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("default north.rest row spells out the unset gate %q: %s", key, raw)
		}
	}
}
