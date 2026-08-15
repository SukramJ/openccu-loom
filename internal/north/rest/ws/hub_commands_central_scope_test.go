// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"encoding/json"
	"testing"
)

// perCentralHubCommands is every hub-family command that acts on exactly
// one CCU, with an otherwise-valid argument set. The three cross-central
// members of the family — sysvars.fetch, sysvars.usage and
// install_mode.search — are deliberately absent, and so is inbox.accept:
// they carry the central themselves or walk every one, so an empty name is
// a documented case there rather than a guess.
var perCentralHubCommands = []struct {
	name string
	args string
}{
	{"programs.list", `{}`},
	{"programs.execute", `{"id":"P1"}`},
	{"programs.delete", `{"id":"P1"}`},
	{"sysvars.list", `{}`},
	{"sysvars.set", `{"name":"PartyMode","value":true}`},
	{"alarm_messages.list", `{}`},
	{"alarm_messages.ack", `{"id":"A1"}`},
	{"alarm_messages.ack_all", `{}`},
	{"service_messages.list", `{}`},
	{"service_messages.ack", `{"id":"S1"}`},
	{"service_messages.ack_all", `{}`},
	{"install_mode.status", `{}`},
	{"install_mode.enable", `{"interface_id":"HmIP-RF","duration_seconds":60}`},
	{"install_mode.disable", `{"interface_id":"HmIP-RF"}`},
	{"backup.trigger", `{}`},
	{"backup.status", `{}`},
	{"firmware.info", `{}`},
	{"firmware.update", `{}`},
	{"inbox.list", `{}`},
}

// withCentral splices central_name into a command's argument object.
func withCentral(args, central string) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		panic("test args are not an object: " + args)
	}
	m["central_name"] = central
	out, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return out
}

// newMultiCentralHubRouter wires the hub command family against a stub
// that knows two CCUs — the shape in which picking one is a wrong-CCU
// write rather than a harmless default.
func newMultiCentralHubRouter() (*Router, *stubHub) {
	h := &stubHub{
		centralHubs:   []string{"attic", "basement"},
		installStatus: map[string]any{"interfaces": []any{}},
		backupStatus:  map[string]any{"status": "idle"},
		firmwareInfo:  map[string]any{"observed": false},
	}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: h})
	return r, h
}

// TestHubCommandsTargetTheCentralTheyName verifies every per-central hub
// command routes to the CCU named in central_name. The whole family used
// to resolve through one unscoped accessor that returned the
// alphabetically first central, so a sysvar write meant for `basement`
// silently landed on `attic` and reported success.
func TestHubCommandsTargetTheCentralTheyName(t *testing.T) {
	t.Parallel()
	for _, tc := range perCentralHubCommands {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, h := newMultiCentralHubRouter()
			res := r.Dispatch(adminCtx(), tc.name, withCentral(tc.args, "basement"))
			if res.Error != nil {
				t.Fatalf("dispatch = %+v, want success", res.Error)
			}
			if h.boundCentral != "basement" {
				t.Fatalf("command reached central %q, want basement", h.boundCentral)
			}
		})
	}
}

// TestHubCommandsRefuseToGuessTheCentral verifies that omitting
// central_name on a multi-CCU daemon is an error rather than a silent
// pick. A wrong-CCU write answers success, which is why guessing is worse
// than failing.
func TestHubCommandsRefuseToGuessTheCentral(t *testing.T) {
	t.Parallel()
	for _, tc := range perCentralHubCommands {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, h := newMultiCentralHubRouter()
			res := r.Dispatch(adminCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
				t.Fatalf("dispatch = %+v, want bad_request", res.Error)
			}
			if h.boundCentral != "" {
				t.Fatalf("an unresolved command still reached central %q", h.boundCentral)
			}
		})
	}
}

// TestHubCommandsRejectAnUnknownCentral verifies a name that resolves to
// no CCU is reported rather than falling back to some other central.
func TestHubCommandsRejectAnUnknownCentral(t *testing.T) {
	t.Parallel()
	r, _ := newMultiCentralHubRouter()
	res := r.Dispatch(adminCtx(), "sysvars.set", withCentral(`{"name":"PartyMode","value":true}`, "garage"))
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("dispatch = %+v, want bad_request", res.Error)
	}
}

// TestHubCommandsKeepTheSingleCentralConvenience verifies the common
// deployment is unaffected: one CCU, no central_name, everything works.
func TestHubCommandsKeepTheSingleCentralConvenience(t *testing.T) {
	t.Parallel()
	for _, tc := range perCentralHubCommands {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &stubHub{
				centralHubs:   []string{"attic"},
				installStatus: map[string]any{"interfaces": []any{}},
				backupStatus:  map[string]any{"status": "idle"},
				firmwareInfo:  map[string]any{"observed": false},
			}
			r := NewRouter()
			RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: h})
			res := r.Dispatch(adminCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error != nil {
				t.Fatalf("dispatch = %+v, want success", res.Error)
			}
			if h.boundCentral != "attic" {
				t.Fatalf("command reached central %q, want attic", h.boundCentral)
			}
		})
	}
}
