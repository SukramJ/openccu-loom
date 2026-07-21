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
	setBool    atomic.Int32
	setFloat   atomic.Int32

	lastCreate atomic.Pointer[map[string]any]
	lastSet    atomic.Pointer[map[string]any]
	lastDelete atomic.Pointer[string]
	lastRega   atomic.Pointer[string]

	// regaResult is what ReGa.runScript returns as result; the string-only
	// set_system_variable script emits the written value on success and
	// nothing when it declines.
	regaResult atomic.Value
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
		case "SysVar.setBool":
			m.setBool.Add(1)
			cp := copyMap(env.Params)
			m.lastSet.Store(&cp)
			_, _ = w.Write([]byte(`{"result":true}`))
		case "SysVar.setFloat":
			m.setFloat.Add(1)
			cp := copyMap(env.Params)
			m.lastSet.Store(&cp)
			_, _ = w.Write([]byte(`{"result":true}`))
		case "ReGa.runScript":
			m.regaCnt.Add(1)
			script, _ := env.Params["script"].(string)
			m.lastRega.Store(&script)
			res, _ := m.regaResult.Load().(string)
			payload, _ := json.Marshal(map[string]any{"result": res})
			_, _ = w.Write(payload)
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

	if err := w.CreateSysvar(context.Background(), "Alarm", "BOOL", "", "", "", "", nil); err != nil {
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

	if err := w.CreateSysvar(context.Background(), "Temp", "FLOAT", "", "0", "100", "", nil); err != nil {
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
	if err := w.CreateSysvar(context.Background(), "Mode", "ENUM", "", "", "", "", values); err != nil {
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

	if err := w.CreateSysvar(context.Background(), "Counter", "INTEGER", "", "0", "10", "", nil); err != nil {
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

// A description on an otherwise JSON-RPC-eligible BOOL forces the Rega
// fallback, because the native SysVar.createBool/createFloat/createEnum
// methods carry no description parameter. The rendered script must
// carry the description text.
func TestCreateSysvarWithDescriptionFallsBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), "Alarm", "BOOL", "", "", "", "guards the door", nil); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call when a description is set, got %d", got)
	}
	if got := m.createBool.Load() + m.createFlt.Load() + m.createEnum.Load(); got != 0 {
		t.Fatalf("JSON-RPC create path must not run with a description, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, "guards the door") {
		t.Fatalf("rega body missing description text: %v", body)
	}
}

// UpdateSysvar marshals a non-empty newName into the update script's
// ##newname## slot so the CCU renames the variable in place.
func TestUpdateSysvarMarshalsNewName(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.UpdateSysvar(context.Background(), "Old", "Fresh", "", "", "", "", nil); err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call for UpdateSysvar, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil {
		t.Fatal("rega body not captured")
	}
	if !strings.Contains(*body, "Fresh") {
		t.Fatalf("rega body missing new name: %v", *body)
	}
	if !strings.Contains(*body, `sNewName = "Fresh"`) {
		t.Fatalf("rega body did not bind sNewName: %v", *body)
	}
}

// SetSysvar dispatch: the set_system_variable Rega script writes
// string-typed variables ONLY (its `ValueTypeStr() == "String"` guard
// silently declines everything else with empty output), so non-string
// values must use the CCU's native typed JSON-RPC methods.

func TestSetSysvarEnumIndexUsesSetFloat(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	// A LIST sysvar (e.g. "Aus;Niedrig;Normal;Hoch") writes its
	// zero-based index; the string-only Rega script would drop it.
	if err := w.SetSysvar(context.Background(), "Belueftungsanlage_Stufe", 2); err != nil {
		t.Fatalf("SetSysvar: %v", err)
	}
	if got := m.setFloat.Load(); got != 1 {
		t.Fatalf("expected 1 setFloat call, got %d", got)
	}
	if got := m.regaCnt.Load(); got != 0 {
		t.Fatalf("Rega path must not run for numeric sysvar writes, got %d", got)
	}
	p := m.lastSet.Load()
	if p == nil || (*p)["name"] != "Belueftungsanlage_Stufe" || (*p)["value"] != float64(2) {
		t.Fatalf("set params = %v", p)
	}
}

func TestSetSysvarFloatUsesSetFloat(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.SetSysvar(context.Background(), "Aussentemperatur", 21.5); err != nil {
		t.Fatalf("SetSysvar: %v", err)
	}
	if got := m.setFloat.Load(); got != 1 {
		t.Fatalf("expected 1 setFloat call, got %d", got)
	}
	p := m.lastSet.Load()
	if p == nil || (*p)["value"] != 21.5 {
		t.Fatalf("set params = %v", p)
	}
}

func TestSetSysvarBoolUsesSetBool(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.SetSysvar(context.Background(), "Anwesenheit", true); err != nil {
		t.Fatalf("SetSysvar: %v", err)
	}
	if got := m.setBool.Load(); got != 1 {
		t.Fatalf("expected 1 setBool call, got %d", got)
	}
	if got := m.regaCnt.Load(); got != 0 {
		t.Fatalf("Rega path must not run for bool sysvar writes, got %d", got)
	}
	p := m.lastSet.Load()
	// The CCU wire method takes the bool as integer 0/1.
	if p == nil || (*p)["value"] != float64(1) {
		t.Fatalf("set params = %v", p)
	}
}

func TestSetSysvarStringUsesRega(t *testing.T) {
	m := newSysvarMock(t)
	m.regaResult.Store("Fenster offen")
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.SetSysvar(context.Background(), "Statustext", "Fenster offen"); err != nil {
		t.Fatalf("SetSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call for string sysvar, got %d", got)
	}
	if got := m.setBool.Load() + m.setFloat.Load(); got != 0 {
		t.Fatalf("typed JSON-RPC path must not run for strings, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, "Fenster offen") || !strings.Contains(*body, "Statustext") {
		t.Fatalf("rega body missing substitutions: %v", body)
	}
}

func TestSetSysvarStringDeclinedSurfacesError(t *testing.T) {
	m := newSysvarMock(t)
	// Default regaResult is empty — the script's decline signal (sysvar
	// missing or not string-typed).
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.SetSysvar(context.Background(), "Statustext", "Fenster offen"); err == nil {
		t.Fatal("a declined string write must surface an error, not a silent no-op")
	}
}

func TestSetSysvarEmptyStringAllowed(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	// Clearing a string sysvar echoes the empty value — that is not a
	// decline and must not error.
	if err := w.SetSysvar(context.Background(), "Statustext", ""); err != nil {
		t.Fatalf("clearing a string sysvar must succeed, got %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call, got %d", got)
	}
}

func TestCreateSysvarBoolWithUnitFallsBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), "Mit_Unit", "BOOL", "°C", "", "", "", nil); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("BOOL+Unit must use Rega fallback, got rega=%d", got)
	}
	if got := m.createBool.Load(); got != 0 {
		t.Fatalf("JSON-RPC createBool must not run when unit is set, got %d", got)
	}
}

// ─── CCU error propagation ──────────────────────────────────────────────────
//
// newErrorJSONRPCServer stands up a bare JSON-RPC endpoint that answers
// every call with a wire-level error object (HTTP 200 + `{"error": …}`,
// the shape the CCU itself uses) so the writer methods' error path can be
// exercised without a success-scripted mock. errCode avoids the CCU's
// session-expiry sentinel (400) so the client does not attempt a
// re-login/retry cycle before surfacing the failure.

func newErrorJSONRPCServer(t *testing.T, message string) string {
	t.Helper()
	const errCode = 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload, err := json.Marshal(map[string]any{
			"error": map[string]any{"code": errCode, "message": message},
		})
		if err != nil {
			t.Fatalf("marshal error payload: %v", err)
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A CCU-side error on the native JSON-RPC create path (BOOL, no unit/
// description) must propagate to the caller, not be swallowed.
func TestCreateSysvarNativePathPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "sysvar name already exists")
	w := newWriterAgainst(t, url)

	err := w.CreateSysvar(context.Background(), "Alarm", "BOOL", "", "", "", "", nil)
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "sysvar name already exists") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}

// A CCU-side error on the Rega fallback path (forced here by a
// description) must also propagate.
func TestCreateSysvarRegaPathPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "script execution failed")
	w := newWriterAgainst(t, url)

	err := w.CreateSysvar(context.Background(), "Alarm", "BOOL", "", "", "", "guards the door", nil)
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "script execution failed") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}

// UpdateSysvar (the rename-capable path) must propagate a CCU-side
// failure instead of reporting success.
func TestUpdateSysvarPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "object not found")
	w := newWriterAgainst(t, url)

	err := w.UpdateSysvar(context.Background(), "Old", "New", "", "", "", "", nil)
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "object not found") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}

// DeleteSysvar's native JSON-RPC path must propagate a CCU-side failure.
func TestDeleteSysvarPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "sysvar not found")
	w := newWriterAgainst(t, url)

	err := w.DeleteSysvar(context.Background(), "Ghost")
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "sysvar not found") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}
