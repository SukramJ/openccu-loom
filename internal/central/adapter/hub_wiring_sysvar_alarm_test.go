// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The CCU answers SysVar.getAll with an EMPTY value for every ALARM
// variable — the variable's state lives in AlState(), which getAll does
// not read — while LOGIC variables in the same response carry
// "true"/"false". The payloads below are the shape both live CCUs
// return.
const (
	alarmSysvarGetAll = `[` +
		`{"id":"10639","name":"Rauchmelder-Alarm","type":"ALARM","unit":"","value":"",` +
		`"channelId":"65535","valueName0":"nicht ausgelöst","valueName1":"ausgelöst",` +
		`"isLogged":false,"isVisible":true,"isInternal":false},` +
		`{"id":"38562","name":"Anwesenheit","type":"LOGIC","unit":"","value":"false",` +
		`"channelId":"65535","valueName0":"nicht anwesend","valueName1":"anwesend",` +
		`"isLogged":false,"isVisible":true,"isInternal":false}` +
		`]`
	alarmSysvarName = "Rauchmelder-Alarm"
	logicSysvarName = "Anwesenheit"
)

// alarmSysvarServer serves SysVar.getAll with the live ALARM/LOGIC shape
// and a swappable description-script output, so one server can play
// successive refreshes.
type alarmSysvarServer struct {
	srv  *httptest.Server
	rega atomic.Value // string: raw script stdout (a JSON array)
}

func (s *alarmSysvarServer) setRega(out string) { s.rega.Store(out) }

// newAlarmSysvarServer serves SysVar.getAll with the live ALARM/LOGIC
// shape and returns the given description-script output for
// ReGa.runScript.
func newAlarmSysvarServer(t *testing.T, regaOut string) *alarmSysvarServer {
	t.Helper()
	s := &alarmSysvarServer{}
	s.rega.Store(regaOut)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req["method"] {
		case "SysVar.getAll":
			var vars []map[string]any
			if err := json.Unmarshal([]byte(alarmSysvarGetAll), &vars); err != nil {
				t.Errorf("fixture: %v", err)
			}
			result = vars
		case "ReGa.runScript":
			result, _ = s.rega.Load().(string)
		default:
			result = nil
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// TestLoadSysvarsAlarmValueComesFromAlarmState pins where an ALARM
// variable's state comes from. Taken from SysVar.getAll alone it has none
// — the CCU reports an empty value for every alarm variable — and the
// north-bound binary sensor declared for it (payload_on "true",
// payload_off "false", both rendered from a bool by the bridge) would
// never see either payload. The description script reads AlState(), and
// that flag is what the model records.
func TestLoadSysvarsAlarmValueComesFromAlarmState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		alarmState string
		wantKind   hmtypes.ValueKind
		wantBool   bool
		wantObs    bool
	}{
		{"raised", `"1"`, hmtypes.ValueKindBool, true, true},
		{"not raised", `"0"`, hmtypes.ValueKindBool, false, true},
		// A script result without the flag (a degraded run) must leave the
		// variable without a value rather than record a made-up "not
		// triggered" — a smoke alarm reading "all clear" from missing data
		// is worse than one reading "unknown".
		{"flag absent", `""`, hmtypes.ValueKindNone, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newAlarmSysvarServer(t, `[`+
				`{"id":"10639","description":"","channel_address":"","alarm_state":`+tc.alarmState+`},`+
				`{"id":"38562","description":"","channel_address":"","alarm_state":""}`+
				`]`)
			jc, runner := newSysvarChannelRunner(t, s.srv)

			h := hub.NewHub("c")
			if err := loadSysvars(context.Background(), jc, runner, h, nil,
				hubScanOptions{enableSysvarScan: true}); err != nil {
				t.Fatalf("loadSysvars: %v", err)
			}

			sv, ok := h.Sysvar(alarmSysvarName)
			if !ok {
				t.Fatalf("sysvar %q not loaded", alarmSysvarName)
			}
			val, observed := sv.Value()
			if observed != tc.wantObs {
				t.Fatalf("observed = %v, want %v (value %#v)", observed, tc.wantObs, val)
			}
			if val.Kind != tc.wantKind {
				t.Fatalf("value kind = %v, want %v (value %#v)", val.Kind, tc.wantKind, val)
			}
			if tc.wantObs && val.Bool != tc.wantBool {
				t.Fatalf("value = %v, want %v", val.Bool, tc.wantBool)
			}

			// Control: a LOGIC variable in the same response still parses
			// from its own value, so the alarm path is not a blanket
			// override of the boolean types.
			logic, ok := h.Sysvar(logicSysvarName)
			if !ok {
				t.Fatalf("sysvar %q not loaded", logicSysvarName)
			}
			lv, lobs := logic.Value()
			if !lobs || lv.Kind != hmtypes.ValueKindBool || lv.Bool {
				t.Fatalf("logic value = %#v observed=%v, want bool false", lv, lobs)
			}
		})
	}
}

// TestLoadSysvarsAlarmStateRefreshFlips verifies the flag is re-read on
// every scan: the periodic sysvar refresh is the only path that carries
// an alarm variable's state, so a raised alarm has to reach the model
// through it.
func TestLoadSysvarsAlarmStateRefreshFlips(t *testing.T) {
	t.Parallel()
	s := newAlarmSysvarServer(t,
		`[{"id":"10639","description":"","channel_address":"","alarm_state":"0"}]`)
	jc, runner := newSysvarChannelRunner(t, s.srv)

	h := hub.NewHub("c")
	opts := hubScanOptions{enableSysvarScan: true}
	if err := loadSysvars(context.Background(), jc, runner, h, nil, opts); err != nil {
		t.Fatalf("loadSysvars (initial): %v", err)
	}
	sv, ok := h.Sysvar(alarmSysvarName)
	if !ok {
		t.Fatalf("sysvar %q not loaded", alarmSysvarName)
	}
	if val, observed := sv.Value(); !observed || val.Bool {
		t.Fatalf("initial value = %#v observed=%v, want false", val, observed)
	}

	changed := make(chan hmtypes.ParamValue, 1)
	sv.OnUpdate(func(_, next hmtypes.ParamValue) {
		select {
		case changed <- next:
		default:
		}
	})

	s.setRega(`[{"id":"10639","description":"","channel_address":"","alarm_state":"1"}]`)
	if err := loadSysvars(context.Background(), jc, runner, h, nil, opts); err != nil {
		t.Fatalf("loadSysvars (raised): %v", err)
	}
	if val, observed := sv.Value(); !observed || !val.Bool {
		t.Fatalf("value after refresh = %#v observed=%v, want true", val, observed)
	}
	select {
	case got := <-changed:
		if !got.Bool {
			t.Fatalf("update notified %#v, want true", got)
		}
	default:
		t.Fatal("a flipped alarm must notify subscribers so the state topic is republished")
	}
}
