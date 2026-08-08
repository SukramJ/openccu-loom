// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// End-to-end test for the /api/v1/alarm/codes CRUD surface plus the
// arm/disarm verbs' `code` body field, layered on the same
// central/godevccu stack alarm_rest_e2e_test.go drives. Where that file
// exercises the sensor/output plane, this one walks the codes plane end
// to end through the real HTTP surface: create an zone, create a PIN
// code via POST /alarm/codes (argon2id hashing happens inside the real
// codes facade — see internal/alarm/codes's own unit tests for that
// layer in isolation), arm, disarm supplying the code, and confirm the
// zone actually left the armed state.
package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newAlarmRestCodesHarness mirrors newAlarmRestHarness
// (alarm_rest_e2e_test.go) but additionally wires AlarmCodes onto the
// router, so /alarm/codes is live rather than the base harness's 503
// (most REST e2e coverage never touches the codes CRUD surface).
func newAlarmRestCodesHarness(t *testing.T) *alarmRestHarness {
	t.Helper()
	ah := newAlarmHarness(t)
	ah.start()

	mw := auth.NewMiddleware(nil, nil)
	operatorResolve := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.ContextWithIdentity(r.Context(), auth.Identity{Subject: "test-operator", Role: auth.RoleOperator})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	router := rest.NewRouter(rest.Deps{
		StartedAt:       time.Now(),
		Alarm:           ah.svc,
		AlarmCodes:      handlers.NewAlarmCodeStoreAdmin(ah.stores.Codes),
		AuditRecorder:   audit.NewBuffer(100),
		AuthResolve:     operatorResolve,
		AuthRequire:     mw.Require,
		RequireOperator: func(next http.Handler) http.Handler { return mw.RequireRole(auth.RoleOperator, next) },
	})
	api := httptest.NewServer(router)
	t.Cleanup(api.Close)
	return &alarmRestHarness{alarmHarness: ah, api: api, client: &http.Client{Timeout: 10 * time.Second}}
}

// TestAlarmRestCodeRequiredDisarmFlow walks a PIN code through the real
// argon2id-backed store: create zone, create the code, arm, disarm
// supplying the correct code (204, zone actually disarms). It then
// re-arms and disarms again supplying a code the store does not
// recognize — this also succeeds, pinning the S6 break-glass rule
// (notes/concepts/alarm-concept.md §11) at the full-stack level: every REST write
// is attributed the "rest-operator" source, which the engine's
// CodePolicy always treats as a strongly-authenticated bypass. A wrong
// or missing code therefore never yields the REST 403 invalid_code
// mapping through this surface today — only a non-operator source
// (MQTT, a keypad, or the still-open "alarm-control" token from §11)
// can reach that branch. The fake-validator-driven refusal/duress
// matrix lives in internal/north/rest/handlers/alarm_verb_code_test.go.
func TestAlarmRestCodeRequiredDisarmFlow(t *testing.T) {
	h := newAlarmRestCodesHarness(t)

	areaCfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 30},
		},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	var zone hmapi.AlarmZone
	res := h.do(http.MethodPost, "/alarm/zones", hmapi.AlarmZone{Name: "Erdgeschoss", Config: areaCfg}, &zone)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /alarm/zones: status %d", res.StatusCode)
	}

	var code hmapi.AlarmCode
	res = h.do(http.MethodPost, "/alarm/codes", hmapi.AlarmCodeRequest{
		Name: "Markus", Kind: "pin", PIN: "1234", Enabled: true,
		Perms: hmapi.AlarmCodePerms{Disarm: true}, Zones: []string{zone.ID},
	}, &code)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /alarm/codes: status %d", res.StatusCode)
	}
	if code.ID == "" {
		t.Fatal("POST /alarm/codes: response carried no server-generated id")
	}

	armReq := hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeFull), SkipDelay: true}
	res = h.do(http.MethodPost, "/alarm/zones/"+zone.ID+"/arm", armReq, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST .../arm: status %d", res.StatusCode)
	}
	if st := h.waitAlarmState(zone.ID, hmenum.AlarmZoneStateArmed, 2*time.Second); st != hmenum.AlarmZoneStateArmed {
		t.Fatalf("zone state after arm = %q, want armed", st)
	}

	res = h.do(http.MethodPost, "/alarm/zones/"+zone.ID+"/disarm", hmapi.AlarmVerbRequest{Code: "1234"}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("POST .../disarm with the correct code: status %d", res.StatusCode)
	}
	if st := h.waitAlarmState(zone.ID, hmenum.AlarmZoneStateDisarmed, 2*time.Second); st != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state after disarm = %q, want disarmed", st)
	}

	// Re-arm, then disarm with a code the store does not recognize.
	res = h.do(http.MethodPost, "/alarm/zones/"+zone.ID+"/arm", armReq, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST .../arm (2nd): status %d", res.StatusCode)
	}
	res = h.do(http.MethodPost, "/alarm/zones/"+zone.ID+"/disarm", hmapi.AlarmVerbRequest{Code: "0000"}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("POST .../disarm with an unrecognized code: status %d, want 204 (operator-session bypass)", res.StatusCode)
	}
	if st := h.waitAlarmState(zone.ID, hmenum.AlarmZoneStateDisarmed, 2*time.Second); st != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("zone state after wrong-code disarm = %q, want disarmed", st)
	}
}
