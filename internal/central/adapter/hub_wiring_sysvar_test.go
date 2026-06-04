// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// CreateSysvar / DeleteSysvar prefer the CCU's native JSON-RPC methods
// (`SysVar.createBool/createFloat/createEnum`, `SysVar.deleteSysVarByName`)
// and only fall back to Rega scripts when the request needs something the
// JSON-RPC API cannot express (INTEGER, STRING, custom unit).

type sysvarMockServer struct {
	srv *httptest.Server

	createBool atomic.Int32
	createEnum atomic.Int32
	createFlt  atomic.Int32
	deleteCnt  atomic.Int32
	regaCnt    atomic.Int32

	lastCreate atomic.Pointer[map[string]any]
	lastDelete atomic.Pointer[string]
	lastRega   atomic.Pointer[string]
}

func newSysvarMock(t *testing.T) *sysvarMockServer {
	t.Helper()
	m := &sysvarMockServer{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch env.Method {
		case "SysVar.createBool":
			m.createBool.Add(1)
			cp := copyMap(env.Params)
			m.lastCreate.Store(&cp)
			_, _ = w.Write([]byte(`{"result":{}}`))
		case "SysVar.createFloat":
			m.createFlt.Add(1)
			cp := copyMap(env.Params)
			m.lastCreate.Store(&cp)
			_, _ = w.Write([]byte(`{"result":{}}`))
		case "SysVar.createEnum":
			m.createEnum.Add(1)
			cp := copyMap(env.Params)
			m.lastCreate.Store(&cp)
			_, _ = w.Write([]byte(`{"result":{}}`))
		case "SysVar.deleteSysVarByName":
			m.deleteCnt.Add(1)
			name, _ := env.Params["name"].(string)
			m.lastDelete.Store(&name)
			_, _ = w.Write([]byte(`{"result":true}`))
		case "ReGa.runScript":
			m.regaCnt.Add(1)
			script, _ := env.Params["script"].(string)
			m.lastRega.Store(&script)
			_, _ = w.Write([]byte(`{"result":""}`))
		default:
			http.Error(w, "unknown method "+env.Method, http.StatusNotFound)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func newWriterAgainst(t *testing.T, srvURL string) *hubJSONRPCWriter {
	t.Helper()
	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srvURL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	runner, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return &hubJSONRPCWriter{json: jc, rega: runner}
}

func TestDeleteSysvarUsesJSONRPC(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.DeleteSysvar(context.Background(), "Heizung_Aus"); err != nil {
		t.Fatalf("DeleteSysvar: %v", err)
	}
	if got := m.deleteCnt.Load(); got != 1 {
		t.Fatalf("expected 1 delete call, got %d", got)
	}
	if got := m.regaCnt.Load(); got != 0 {
		t.Fatalf("Rega path must not run, got %d calls", got)
	}
	if name := m.lastDelete.Load(); name == nil || *name != "Heizung_Aus" {
		t.Fatalf("delete name = %v", name)
	}
}

func TestCreateSysvarBoolUsesJSONRPC(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), "Alarm", "BOOL", "", "", "", nil); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.createBool.Load(); got != 1 {
		t.Fatalf("expected 1 createBool call, got %d", got)
	}
	if got := m.regaCnt.Load(); got != 0 {
		t.Fatalf("Rega path must not run for BOOL without unit, got %d", got)
	}
	got := m.lastCreate.Load()
	if got == nil || (*got)["name"] != "Alarm" {
		t.Fatalf("create params = %v", got)
	}
}

func TestCreateSysvarFloatUsesJSONRPC(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), "Temp", "FLOAT", "", "0", "100", nil); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.createFlt.Load(); got != 1 {
		t.Fatalf("expected 1 createFloat call, got %d", got)
	}
	got := m.lastCreate.Load()
	if got == nil || (*got)["min_value"] != "0" || (*got)["max_value"] != "100" {
		t.Fatalf("create params = %v", got)
	}
}

func TestCreateSysvarEnumUsesJSONRPC(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	values := []string{"a", "b", "c"}
	if err := w.CreateSysvar(context.Background(), "Mode", "ENUM", "", "", "", values); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.createEnum.Load(); got != 1 {
		t.Fatalf("expected 1 createEnum call, got %d", got)
	}
	got := m.lastCreate.Load()
	if got == nil || (*got)["value_list"] != "a;b;c" {
		t.Fatalf("create params = %v", got)
	}
}

func TestCreateSysvarIntegerFallsBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), "Counter", "INTEGER", "", "0", "10", nil); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call for INTEGER, got %d", got)
	}
	if got := m.createBool.Load() + m.createFlt.Load() + m.createEnum.Load(); got != 0 {
		t.Fatalf("JSON-RPC path must not run for INTEGER, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, `"INTEGER"`) {
		t.Fatalf("rega body missing INTEGER type marker: %v", body)
	}
}

func TestCreateSysvarBoolWithUnitFallsBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), "Mit_Unit", "BOOL", "°C", "", "", nil); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("BOOL+Unit must use Rega fallback, got rega=%d", got)
	}
	if got := m.createBool.Load(); got != 0 {
		t.Fatalf("JSON-RPC createBool must not run when unit is set, got %d", got)
	}
}
