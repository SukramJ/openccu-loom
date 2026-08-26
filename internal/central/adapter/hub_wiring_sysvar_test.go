// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
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
	detailCnt  atomic.Int32

	// fleet maps a device or channel address to its ReGa ise id and backs
	// the Device.listAllDetail answer — the CCU's only address→ise-id
	// source. The server deliberately does NOT serve
	// Interface.getIseIDByAddress: real CCU firmware has no such method, so
	// answering it here would keep a call alive that fails on every real
	// installation. detailFault turns the read into a server error, the
	// "CCU unreachable" case, which must not look like a bad address.
	fleet       atomic.Pointer[map[string]string]
	detailFault atomic.Bool

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
	m.setFleet(map[string]string{"ABC0000001": "4320", "ABC0000001:1": "4321"})
	// Default the ReGa result to a success token so a create ("id") and an
	// update ("ok") both read as accepted — the writers now reject an empty
	// result (a decline / failed create). "ok" is non-empty (create's success
	// signal) and equals the update script's success token, so it satisfies
	// both. A test that wants a decline stores an explicit "" itself.
	m.regaResult.Store("ok")
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
		case "Device.listAllDetail":
			m.detailCnt.Add(1)
			if m.detailFault.Load() {
				http.Error(w, "ccu unreachable", http.StatusBadGateway)
				return
			}
			payload, _ := json.Marshal(map[string]any{"result": m.detailPayload()})
			_, _ = w.Write(payload)
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

// setFleet replaces the address→ise-id table the server reports through
// Device.listAllDetail. Keys carrying a ":" are channels of the device
// named by their prefix.
func (m *sysvarMockServer) setFleet(fleet map[string]string) {
	cp := make(map[string]string, len(fleet))
	maps.Copy(cp, fleet)
	m.fleet.Store(&cp)
}

// detailPayload renders the fleet in the shape the CCU's
// Device.listAllDetail answers with: one entry per device carrying its own
// id plus a nested channel list, each channel with its own id and address.
func (m *sysvarMockServer) detailPayload() []map[string]any {
	fleet := map[string]string{}
	if p := m.fleet.Load(); p != nil {
		fleet = *p
	}
	byDevice := make(map[string][]map[string]any)
	for addr, id := range fleet {
		device, _, isChannel := strings.Cut(addr, ":")
		if !isChannel {
			if _, seen := byDevice[addr]; !seen {
				byDevice[addr] = nil
			}
			continue
		}
		byDevice[device] = append(byDevice[device], map[string]any{
			"id": id, "address": addr, "name": addr,
		})
	}
	out := make([]map[string]any, 0, len(byDevice))
	for device, channels := range byDevice {
		out = append(out, map[string]any{
			"id":       fleet[device],
			"address":  device,
			"name":     device,
			"channels": channels,
		})
	}
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

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Alarm", ValueType: "BOOL"}); err != nil {
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

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Temp", ValueType: "FLOAT", Min: "0", Max: "100"}); err != nil {
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
	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Mode", ValueType: "ENUM", ValueList: values}); err != nil {
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

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Counter", ValueType: "INTEGER", Min: "0", Max: "10"}); err != nil {
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

// ALARM has no native JSON-RPC counterpart (there is no
// SysVar.createAlarm), so it must always fall back to the
// create_system_variable Rega script, carrying the ALARM type marker
// so the script's OT_ALARMDP branch fires.
func TestCreateSysvarAlarmFallsBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Einbruch", ValueType: "ALARM"}); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call for ALARM, got %d", got)
	}
	if got := m.createBool.Load() + m.createFlt.Load() + m.createEnum.Load(); got != 0 {
		t.Fatalf("JSON-RPC create path must not run for ALARM, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, `"ALARM"`) {
		t.Fatalf("rega body missing ALARM type marker: %v", body)
	}
}

// A description on an otherwise JSON-RPC-eligible BOOL forces the Rega
// fallback, because the native SysVar.createBool/createFloat/createEnum
// methods carry no description parameter. The rendered script must
// carry the description text.
func TestCreateSysvarWithDescriptionFallsBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Alarm", ValueType: "BOOL", Description: "guards the door"}); err != nil {
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

	if err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "Old", NewName: "Fresh"}); err != nil {
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
	// An empty result is the script's decline signal (sysvar missing or not
	// string-typed); store it explicitly to override the mock's success default.
	m.regaResult.Store("")
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

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Mit_Unit", ValueType: "BOOL", Unit: "°C"}); err != nil {
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

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Alarm", ValueType: "BOOL"})
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

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Alarm", ValueType: "BOOL", Description: "guards the door"})
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "script execution failed") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}

// ALARM always takes the Rega path (no native createAlarm); a CCU-side
// failure there (e.g. the OT_ALARMDP object could not be created) must
// propagate to the caller like any other Rega-path error.
func TestCreateSysvarAlarmPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "OT_ALARMDP creation failed")
	w := newWriterAgainst(t, url)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Einbruch", ValueType: "ALARM"})
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "OT_ALARMDP creation failed") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}

// UpdateSysvar (the rename-capable path) must propagate a CCU-side
// failure instead of reporting success.
func TestUpdateSysvarPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "object not found")
	w := newWriterAgainst(t, url)

	err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "Old", NewName: "New"})
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

// UpdateSysvar must not swallow the update script's result: the script
// writes "ok" only inside its `if (oSv)` guard, so an empty output means the
// target name no longer resolves to a sysvar on the CCU. A PATCH against a
// deleted variable must surface ErrSysvarNotFound (mapped to 404) rather than
// answer a false 202 and re-key the local cache to a name the CCU never
// renamed.
func TestUpdateSysvarMissingTargetSurfacesNotFound(t *testing.T) {
	m := newSysvarMock(t)
	// The update script declines (target not found) by emitting nothing.
	m.regaResult.Store("")
	w := newWriterAgainst(t, m.srv.URL)

	err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "Ghost", NewName: "Fresh"})
	if err == nil {
		t.Fatal("an update against a missing sysvar must surface an error, not a silent no-op")
	}
	if !errors.Is(err, hub.ErrSysvarNotFound) {
		t.Fatalf("err = %v, want ErrSysvarNotFound", err)
	}
}

// CreateSysvar via the Rega path must not swallow the create script's result:
// an empty output means dom.CreateObject failed. A failed create must surface
// an error rather than answer a false 202 (STRING forces the Rega path).
func TestCreateSysvarEmptyOutputSurfacesError(t *testing.T) {
	m := newSysvarMock(t)
	// dom.CreateObject failure → the create script emits an empty string.
	m.regaResult.Store("")
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Broken", ValueType: "STRING"})
	if err == nil {
		t.Fatal("a failed create (empty rega output) must surface an error, not a silent no-op")
	}
}

// Custom binary value labels have no native JSON-RPC create parameter, so
// a BOOL create carrying ValueName0/1 must fall back to the Rega script
// and the rendered script must bind the operator's labels.
func TestCreateSysvarCustomLabelsFallBackToRega(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Tuer", ValueType: "BOOL", ValueName0: "zu", ValueName1: "offen",
	})
	if err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.regaCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Rega call for custom labels, got %d", got)
	}
	if got := m.createBool.Load(); got != 0 {
		t.Fatalf("native createBool must not run when labels are set, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil {
		t.Fatal("rega body not captured")
	}
	if !strings.Contains(*body, `sValueName0 = "zu"`) || !strings.Contains(*body, `sValueName1 = "offen"`) {
		t.Fatalf("rega body missing custom value labels: %v", *body)
	}
}

// A BOOL create forced onto the Rega path (here by a custom unit) without
// explicit labels must still bind the CCU's own "false"/"true" defaults so
// the script never overwrites the labels with a blank string.
func TestCreateSysvarRegaFillsDefaultLabels(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Mit_Unit", ValueType: "BOOL", Unit: "°C",
	})
	if err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	body := m.lastRega.Load()
	if body == nil {
		t.Fatal("rega body not captured")
	}
	if !strings.Contains(*body, `sValueName0 = "false"`) || !strings.Contains(*body, `sValueName1 = "true"`) {
		t.Fatalf("rega body missing default value labels: %v", *body)
	}
}

// UpdateSysvar threads the value labels and the tri-state visibility /
// archive flags into the update script slots. A nil flag stays "" (leave
// the CCU value untouched); a non-nil flag binds "true"/"false".
func TestUpdateSysvarPassesLabelsAndFlags(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	visible := true
	logged := false
	err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{
		Name: "Tuer", ValueName0: "zu", ValueName1: "offen",
		Visible: &visible, Logged: &logged,
	})
	if err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	body := m.lastRega.Load()
	if body == nil {
		t.Fatal("rega body not captured")
	}
	for _, want := range []string{
		`sValueName0 = "zu"`,
		`sValueName1 = "offen"`,
		`sVisible = "true"`,
		`sLogged = "false"`,
	} {
		if !strings.Contains(*body, want) {
			t.Fatalf("rega body missing %q: %v", want, *body)
		}
	}
}

// An UpdateSysvar that leaves the flags nil must bind them to the empty
// string so the script's `if (sVisible != "")` guards skip the write and
// the CCU flag stays as-is.
func TestUpdateSysvarNilFlagsLeaveScriptSlotsEmpty(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "X", Unit: "°C"}); err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	body := m.lastRega.Load()
	if body == nil {
		t.Fatal("rega body not captured")
	}
	if !strings.Contains(*body, `sVisible = "";`) || !strings.Contains(*body, `sLogged = "";`) {
		t.Fatalf("rega body should leave visibility/archive slots empty: %v", *body)
	}
}

// loadSysvars must parse the value labels and the visibility / archive
// flags that SysVar.getAll reports for LOGIC and ALARM variables onto the
// hub Sysvar so every north-bound plane (REST, WS, MQTT) sees them.
func TestLoadSysvarsParsesLabelsAndFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req["method"] == "SysVar.getAll" {
			result = []map[string]any{{
				"id": "200", "name": "Tuer", "type": "LOGIC", "value": "true",
				"isInternal": false, "isVisible": true, "isLogged": true,
				"valueName0": "zu", "valueName1": "offen",
			}}
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	h := hub.NewHub("c")
	if err := loadSysvars(context.Background(), jc, nil, h, nil, hubScanOptions{enableSysvarScan: true}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	sv, ok := h.Sysvar("Tuer")
	if !ok {
		t.Fatal("sysvar Tuer should have been loaded")
	}
	if sv.ValueName0 != "zu" || sv.ValueName1 != "offen" {
		t.Fatalf("value labels = %q/%q, want zu/offen", sv.ValueName0, sv.ValueName1)
	}
	if !sv.IsVisible || !sv.IsLogged {
		t.Fatalf("flags: IsVisible=%v IsLogged=%v, want true/true", sv.IsVisible, sv.IsLogged)
	}
}

// A non-binary variable (FLOAT here) reports no value labels and can carry
// isVisible=false / isLogged=false — loadSysvars must not default the
// flags to true, which a naive zero-value read would mask since Go's bool
// zero value happens to be "false" only when the parse actually ran.
func TestLoadSysvarsParsesFlagsFalseAndNoLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req["method"] == "SysVar.getAll" {
			result = []map[string]any{{
				"id": "201", "name": "Temp", "type": "FLOAT", "value": "21.5",
				"isInternal": false, "isVisible": false, "isLogged": false,
			}}
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	h := hub.NewHub("c")
	if err := loadSysvars(context.Background(), jc, nil, h, nil, hubScanOptions{enableSysvarScan: true}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	sv, ok := h.Sysvar("Temp")
	if !ok {
		t.Fatal("sysvar Temp should have been loaded")
	}
	if sv.IsVisible || sv.IsLogged {
		t.Fatalf("flags: IsVisible=%v IsLogged=%v, want false/false", sv.IsVisible, sv.IsLogged)
	}
	if sv.ValueName0 != "" || sv.ValueName1 != "" {
		t.Fatalf("value labels = %q/%q, want both empty for a non-binary variable", sv.ValueName0, sv.ValueName1)
	}
}

// ALARM variables are the other binary type SysVar.getAll reports value
// labels for (alongside LOGIC); loadSysvars must not special-case away
// from LOGIC.
func TestLoadSysvarsAlarmTypeParsesLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req["method"] == "SysVar.getAll" {
			result = []map[string]any{{
				"id": "202", "name": "Einbruch", "type": "ALARM", "value": "false",
				"isInternal": false, "isVisible": true, "isLogged": false,
				"valueName0": "ruhig", "valueName1": "alarm",
			}}
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	h := hub.NewHub("c")
	if err := loadSysvars(context.Background(), jc, nil, h, nil, hubScanOptions{enableSysvarScan: true}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	sv, ok := h.Sysvar("Einbruch")
	if !ok {
		t.Fatal("sysvar Einbruch should have been loaded")
	}
	if sv.ValueName0 != "ruhig" || sv.ValueName1 != "alarm" {
		t.Fatalf("value labels = %q/%q, want ruhig/alarm", sv.ValueName0, sv.ValueName1)
	}
	if !sv.IsVisible || sv.IsLogged {
		t.Fatalf("flags: IsVisible=%v IsLogged=%v, want true/false", sv.IsVisible, sv.IsLogged)
	}
}

// A CCU-side error on SysVar.getAll itself (not the write-side create/
// update/delete calls covered above) must propagate to the caller instead
// of loadSysvars silently returning an empty catalogue.
func TestLoadSysvarsPropagatesCCUError(t *testing.T) {
	url := newErrorJSONRPCServer(t, "backend temporarily unavailable")

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: url})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	h := hub.NewHub("c")
	err = loadSysvars(context.Background(), jc, nil, h, nil, hubScanOptions{enableSysvarScan: true})
	if err == nil {
		t.Fatal("expected a CCU error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "backend temporarily unavailable") {
		t.Fatalf("error = %v, want it to carry the CCU message", err)
	}
}

// boolFlagParam renders the tri-state Visible/Logged pointer into the Rega
// script parameter text: nil leaves the CCU flag untouched ("") while a
// non-nil pointer binds the literal "true"/"false".
func TestBoolFlagParam(t *testing.T) {
	trueVal, falseVal := true, false
	tests := []struct {
		name string
		in   *bool
		want string
	}{
		{"nil leaves flag untouched", nil, ""},
		{"true", &trueVal, "true"},
		{"false", &falseVal, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boolFlagParam(tt.in); got != tt.want {
				t.Errorf("boolFlagParam(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// UpdateSysvar's two flags are independent tri-state pointers: setting one
// must not perturb the other's "leave untouched" (nil → "") slot.
func TestUpdateSysvarIndependentFlags(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	visible := false
	err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{
		Name: "X", Visible: &visible,
	})
	if err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	body := m.lastRega.Load()
	if body == nil {
		t.Fatal("rega body not captured")
	}
	if !strings.Contains(*body, `sVisible = "false"`) {
		t.Fatalf("rega body missing sVisible=false: %v", *body)
	}
	if !strings.Contains(*body, `sLogged = "";`) {
		t.Fatalf("rega body should leave sLogged untouched when Logged is nil: %v", *body)
	}
}

// ─── Channel assignment ─────────────────────────────────────────────────────
//
// A create/patch carrying a channel address resolves it to the channel's
// ReGa ise id before it reaches the CCU. The resolution reads
// Device.listAllDetail, the CCU's only address→ise-id source; the mock
// server serves no Interface.getIseIDByAddress, exactly as real firmware
// does not, so a writer reaching for that method fails here the way it
// fails on a CCU. The native create path threads the id into chn_id (no
// longer hard-coded -1); the Rega paths bind it into the script's
// ##channel## slot.

// A BOOL create with a channel address resolves the address and threads the
// numeric ise id into SysVar.createBool's chn_id (replacing the -1 default).
func TestCreateSysvarChannelResolvesIntoNativeChnID(t *testing.T) {
	m := newSysvarMock(t)
	m.setFleet(map[string]string{"ABC0000001": "5677", "ABC0000001:3": "5678"})
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Alarm", ValueType: "BOOL", Channel: "ABC0000001:3",
	})
	if err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.detailCnt.Load(); got != 1 {
		t.Fatalf("expected 1 Device.listAllDetail call, got %d", got)
	}
	got := m.lastCreate.Load()
	if got == nil || (*got)["chn_id"] != float64(5678) {
		t.Fatalf("create chn_id = %v, want 5678", got)
	}
}

// The device address itself resolves too — the CCU lists a device's own ise
// id next to its channels', and an operator may bind a variable to either.
func TestCreateSysvarChannelResolvesDeviceAddress(t *testing.T) {
	m := newSysvarMock(t)
	m.setFleet(map[string]string{"ABC0000001": "5677", "ABC0000001:3": "5678"})
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Alarm", ValueType: "BOOL", Channel: "ABC0000001",
	})
	if err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	got := m.lastCreate.Load()
	if got == nil || (*got)["chn_id"] != float64(5677) {
		t.Fatalf("create chn_id = %v, want 5677", got)
	}
}

// A create forced onto the Rega path (INTEGER) with a channel address resolves
// the id and binds it into the script's ##channel## slot.
func TestCreateSysvarChannelViaRegaBindsResolvedID(t *testing.T) {
	m := newSysvarMock(t)
	m.setFleet(map[string]string{"ABC0000001": "9000", "ABC0000001:3": "9001"})
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Counter", ValueType: "INTEGER", Channel: "ABC0000001:3",
	})
	if err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.detailCnt.Load(); got != 1 {
		t.Fatalf("expected 1 resolve call, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, `sChannel = "9001"`) {
		t.Fatalf("rega body missing resolved channel id: %v", body)
	}
}

// A create without a channel leaves chn_id at the unassigned -1 default and
// never resolves an address.
func TestCreateSysvarNoChannelKeepsUnassigned(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{Name: "Alarm", ValueType: "BOOL"}); err != nil {
		t.Fatalf("CreateSysvar: %v", err)
	}
	if got := m.detailCnt.Load(); got != 0 {
		t.Fatalf("no channel must not resolve an address, got %d calls", got)
	}
	got := m.lastCreate.Load()
	if got == nil || (*got)["chn_id"] != float64(-1) {
		t.Fatalf("create chn_id = %v, want -1", got)
	}
}

// An address the CCU does not list surfaces as the
// hub.ErrSysvarChannelUnknown sentinel, which the REST handler maps to 422.
func TestCreateSysvarChannelUnknownAddressReturnsSentinel(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Alarm", ValueType: "BOOL", Channel: "BAD:9",
	})
	if !errors.Is(err, hub.ErrSysvarChannelUnknown) {
		t.Fatalf("err = %v, want hub.ErrSysvarChannelUnknown", err)
	}
	if got := m.createBool.Load(); got != 0 {
		t.Fatalf("createBool must not run when the channel does not resolve, got %d", got)
	}
}

// A non-positive listed id (the address appears but names nothing usable)
// is also treated as unresolvable.
func TestCreateSysvarChannelZeroIDReturnsSentinel(t *testing.T) {
	m := newSysvarMock(t)
	m.setFleet(map[string]string{"ABC": "0", "ABC:1": "0"})
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Alarm", ValueType: "BOOL", Channel: "ABC:1",
	})
	if !errors.Is(err, hub.ErrSysvarChannelUnknown) {
		t.Fatalf("err = %v, want hub.ErrSysvarChannelUnknown", err)
	}
}

// A failed device read is a transport problem, not a bad address: it must
// NOT carry the sentinel, so the REST handler answers 502 instead of
// blaming the operator's input with a 422.
func TestCreateSysvarChannelReadFailureIsNotAValidationError(t *testing.T) {
	m := newSysvarMock(t)
	m.detailFault.Store(true)
	w := newWriterAgainst(t, m.srv.URL)

	err := w.CreateSysvar(context.Background(), hub.SysvarCreateSpec{
		Name: "Alarm", ValueType: "BOOL", Channel: "ABC0000001:1",
	})
	if err == nil {
		t.Fatal("expected an error when the device list cannot be read")
	}
	if errors.Is(err, hub.ErrSysvarChannelUnknown) {
		t.Fatalf("a failed read must not report an unknown channel: %v", err)
	}
	if got := m.createBool.Load(); got != 0 {
		t.Fatalf("createBool must not run when the address could not be checked, got %d", got)
	}
}

// A PATCH with a channel address resolves it and binds the id into the update
// script's ##channel## slot.
func TestUpdateSysvarChannelResolvesAndMarshals(t *testing.T) {
	m := newSysvarMock(t)
	m.setFleet(map[string]string{"ABC0000001": "7776", "ABC0000001:5": "7777"})
	w := newWriterAgainst(t, m.srv.URL)

	addr := "ABC0000001:5"
	if err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "X", Channel: &addr}); err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	if got := m.detailCnt.Load(); got != 1 {
		t.Fatalf("expected 1 resolve call, got %d", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, `sChannel = "7777"`) {
		t.Fatalf("rega body missing resolved channel id: %v", body)
	}
}

// A PATCH clearing the assignment (empty-string pointer) marshals -1 into the
// ##channel## slot and never resolves an address.
func TestUpdateSysvarChannelClearMarshalsMinusOne(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	empty := ""
	if err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "X", Channel: &empty}); err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	if got := m.detailCnt.Load(); got != 0 {
		t.Fatalf("clearing must not resolve an address, got %d calls", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, `sChannel = "-1"`) {
		t.Fatalf("rega body should clear the channel with -1: %v", body)
	}
}

// A PATCH that leaves Channel nil binds "" into the slot so the script's
// `if (sChannel != "")` guard skips the write and the CCU assignment stays.
func TestUpdateSysvarChannelNilLeavesSlotEmpty(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	if err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "X", Unit: "°C"}); err != nil {
		t.Fatalf("UpdateSysvar: %v", err)
	}
	if got := m.detailCnt.Load(); got != 0 {
		t.Fatalf("nil channel must not resolve an address, got %d calls", got)
	}
	body := m.lastRega.Load()
	if body == nil || !strings.Contains(*body, `sChannel = "";`) {
		t.Fatalf("rega body should leave the channel slot empty: %v", body)
	}
}

// A PATCH channel address the CCU cannot resolve surfaces the sentinel and
// never dispatches the update script.
func TestUpdateSysvarChannelUnknownAddressReturnsSentinel(t *testing.T) {
	m := newSysvarMock(t)
	w := newWriterAgainst(t, m.srv.URL)

	addr := "BAD:9"
	err := w.UpdateSysvar(context.Background(), hub.SysvarUpdateSpec{Name: "X", Channel: &addr})
	if !errors.Is(err, hub.ErrSysvarChannelUnknown) {
		t.Fatalf("err = %v, want hub.ErrSysvarChannelUnknown", err)
	}
	if got := m.regaCnt.Load(); got != 0 {
		t.Fatalf("update script must not run when the channel does not resolve, got %d", got)
	}
}
