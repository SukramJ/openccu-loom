// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
)

// roleGateWriteCommands is a representative slice of writeCommandRoles
// spanning both gated tiers: three operator-tier commands sourced from
// different registration functions (paramset.put, sysvars.set,
// device.rename) and the two admin-tier commands (backup.trigger,
// ccu.cache_clear).
var roleGateWriteCommands = []struct {
	name string
	args string
	tier auth.Role
}{
	{"paramset.put", `{"channel_address":"ABC0001:1","paramset_key":"VALUES","values":{"STATE":true}}`, auth.RoleOperator},
	{"sysvars.set", `{"name":"PartyMode","value":true}`, auth.RoleOperator},
	{"device.rename", `{"address":"ABC0001","name":"Test"}`, auth.RoleOperator},
	{"backup.trigger", `{}`, auth.RoleAdmin},
	{"backups.trigger", `{"central_name":"alpha"}`, auth.RoleAdmin},
	{"ccu.cache_clear", `{}`, auth.RoleAdmin},
}

// newRoleGateRouter wires just enough of the command surface to exercise
// every role tier the gate enforces — operator-tier commands from both
// RegisterDefaultCommands (sysvars.set) and RegisterExtendedCommands
// (paramset.put, device.rename), the admin-tier ccu.cache_clear, plus one
// ungated read command (devices.list) — without pulling in the full
// daemon wiring.
func newRoleGateRouter() *Router {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{
		Devices: &stubDeviceQuery{},
		Hub:     &stubHub{},
		Backups: &stubBackups{},
	})
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Devices:      &stubDevices{},
		Paramsets:    &stubParamsetWriter{},
		CacheClearer: &stubCacheClearer{},
	})
	return r
}

// TestViewerCannotInvokeWriteCommands asserts the gate rejects a
// below-minimum-role identity with `forbidden` for every tier. For the
// two admin-tier commands it also checks that an operator identity —
// enough for every other write command — is still forbidden, since
// admin is a strictly higher bar.
func TestViewerCannotInvokeWriteCommands(t *testing.T) {
	r := newRoleGateRouter()
	for _, tc := range roleGateWriteCommands {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Dispatch(viewerCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorForbidden {
				t.Fatalf("viewer dispatch of %s = %+v, want forbidden", tc.name, res.Error)
			}
			if tc.tier != auth.RoleAdmin {
				return
			}
			res = r.Dispatch(opCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorForbidden {
				t.Fatalf("operator dispatch of admin-tier %s = %+v, want forbidden", tc.name, res.Error)
			}
		})
	}
}

// TestUnauthenticatedCannotInvokeWriteCommands asserts every write
// command rejects a bare context (no identity at all) with
// `unauthorized`, distinct from the role-mismatch `forbidden` case.
func TestUnauthenticatedCannotInvokeWriteCommands(t *testing.T) {
	r := newRoleGateRouter()
	for _, tc := range roleGateWriteCommands {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Dispatch(context.Background(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorUnauthorized {
				t.Fatalf("unauthenticated dispatch of %s = %+v, want unauthorized", tc.name, res.Error)
			}
		})
	}
}

// TestOperatorCanInvokeOperatorCommands asserts an operator identity
// clears the gate for every operator-tier command. The handler may still
// fail for an unrelated reason (a stub returning a domain error, a
// bad_request from minimal args) — only the auth outcome is asserted.
func TestOperatorCanInvokeOperatorCommands(t *testing.T) {
	r := newRoleGateRouter()
	for _, tc := range roleGateWriteCommands {
		if tc.tier != auth.RoleOperator {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			res := r.Dispatch(opCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error != nil && (res.Error.Code == CommandErrorUnauthorized || res.Error.Code == CommandErrorForbidden) {
				t.Fatalf("operator dispatch of %s blocked by role gate: %+v", tc.name, res.Error)
			}
		})
	}
}

// TestReadCommandAllowedForViewer asserts an ungated (read) command is
// never touched by the writeCommandRoles gate — a viewer identity, the
// lowest role, must clear it.
func TestReadCommandAllowedForViewer(t *testing.T) {
	r := newRoleGateRouter()
	res := r.Dispatch(viewerCtx(), "devices.list", nil)
	if res.Error != nil && (res.Error.Code == CommandErrorUnauthorized || res.Error.Code == CommandErrorForbidden) {
		t.Fatalf("devices.list blocked for viewer: %+v", res.Error)
	}
}

// TestWriteCommandRolesAreRegistered guards against a stale or
// misspelled writeCommandRoles entry: every command it lists must
// actually be registered by one of the production Register*Commands
// functions when every optional dependency is wired. A key that never
// resolves to a real handler is dead policy — either a typo (silently
// never enforced) or a command that was removed without updating the
// table.
func TestWriteCommandRolesAreRegistered(t *testing.T) {
	r := NewRouter()

	RegisterDefaultCommands(r, DefaultCommandsConfig{
		Health:          &stubHealth{},
		Devices:         &stubDeviceQuery{},
		Hub:             &stubHub{},
		Links:           &stubLinks{},
		Schedules:       &stubSchedules{},
		Sessions:        configui.NewSessionStore(),
		SessionBackend:  &stubBackend{},
		DeviceReloader:  &stubDeviceReloader{},
		ChannelReloader: &stubChannelReloader{},
		Backups:         &stubBackups{},
	})

	paramsets := &stubParamsetReaderWriter{}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Devices:              &stubDevices{},
		Paramsets:            paramsets,
		ChangeHistory:        &stubChangeHistory{},
		ChangeHistoryClearer: &stubChangeHistoryClearer{},
		Central:              &stubCentral{},
		ExtendedHub:          &stubExtendedHub{},
		MasterProfiles:       masterprofile.New(),
		ThrottleStats:        &stubThrottleStats{},
		CacheClearer:         &stubCacheClearer{},
		DeviceStatistics:     &stubDeviceStats{},
		FirmwareRefresher:    &stubFirmwareRefresher{},
		IncidentClearer:      &stubIncidentClearer{},
		IncidentLister:       &stubIncidentLister{},
		UISchema:             &stubUISchema{},
		ParamsetReader:       paramsets,
		CentralLinks:         &fakeCentralLinks{},
		SessionRecorder:      &fakeSessionRecorder{},
	})

	RegisterMissingCommands(r, MissingCommandsConfig{
		ScheduleEnabler: &stubScheduleEnabler{},
	})

	RegisterCustomDPCommands(r, CustomDPCommandsConfig{
		Index:   &stubCustomDPIndex{},
		Invoker: &stubCustomDPInvoker{},
	})

	for cmd := range writeCommandRoles {
		if !r.Has(cmd) {
			t.Errorf("writeCommandRoles[%q] has no registered handler — dead entry or typo", cmd)
		}
	}
}
