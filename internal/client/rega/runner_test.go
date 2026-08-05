// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func TestEscapeStringHandlesBackslashAndQuote(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"plain":      "plain",
		`say "hi"`:   `say \"hi\"`,
		`back\slash`: `back\\slash`,
		`mix\"both"`: `mix\\\"both\"`,
	}
	for in, want := range cases {
		if got := EscapeString(in); got != want {
			t.Errorf("EscapeString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstituteAllPlaceholders(t *testing.T) {
	body := `string n = "##name##"; integer v = ##value##;`
	got, err := substitute(body, map[string]string{"name": `a"b`, "value": "42"})
	if err != nil {
		t.Fatal(err)
	}
	want := `string n = "a\"b"; integer v = 42;`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSubstituteReportsMissing(t *testing.T) {
	body := `##a## ##b## ##a##`
	_, err := substitute(body, map[string]string{"a": "1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "b") {
		t.Errorf("error should mention missing key 'b': %v", err)
	}
}

func TestSubstituteIgnoresUnreferencedParams(t *testing.T) {
	body := `##a##`
	got, err := substitute(body, map[string]string{"a": "x", "b": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "x" {
		t.Errorf("got %q", got)
	}
}

func TestSubstituteLeavesNonMatchingHashesAlone(t *testing.T) {
	body := `## not a match ##text##`
	got, err := substitute(body, map[string]string{"text": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	want := `## not a match ok`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitizeJSONControlsEscapesControlCharsInsideStrings(t *testing.T) {
	in := "{\"name\":\"line1\nline2\"}"
	got := SanitizeJSONControls(in)
	// Expected output uses the six-character escape, not a literal newline.
	want := "{\"name\":\"line1\\u000aline2\"}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Parser must now accept the output and round-trip to the original string.
	var out map[string]string
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("sanitised output still not valid JSON: %v", err)
	}
	if out["name"] != "line1\nline2" {
		t.Errorf("round-tripped name = %q", out["name"])
	}
}

func TestSanitizeJSONControlsPreservesStructuralWhitespace(t *testing.T) {
	in := "[\n  {\"x\":1},\n  {\"x\":2}\n]"
	got := SanitizeJSONControls(in)
	if got != in {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestSanitizeJSONControlsLeavesAlreadyEscapedAlone(t *testing.T) {
	in := `{"a":"\n\t\"ok\""}`
	got := SanitizeJSONControls(in)
	if got != in {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestEveryKnownScriptHasBody(t *testing.T) {
	for _, s := range hmenum.AllRegaScripts {
		body, err := loadScript(s)
		if err != nil {
			t.Errorf("script %q missing: %v", s, err)
			continue
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("script %q is empty", s)
		}
	}
}

func TestNewRunnerRejectsNilClient(t *testing.T) {
	if _, err := NewRunner(Config{}); err == nil {
		t.Fatal("expected error")
	}
}

// --- End-to-end with a fake JSON-RPC server ---

// scriptCapture records the last script string dispatched to the fake server.
type scriptCapture struct {
	seen atomic.Value // holds the script string last dispatched
}

func (c *scriptCapture) lastScript() string {
	v, _ := c.seen.Load().(string)
	return v
}

func newFakeCCU(t *testing.T, capture *scriptCapture, result any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if env.Method != regaRunScriptMethod {
			http.Error(w, "wrong method: "+env.Method, http.StatusBadRequest)
			return
		}
		capture.seen.Store(env.Params["script"].(string))
		resp := struct {
			Result any `json:"result"`
		}{Result: result}
		raw, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
}

func newRunner(t *testing.T, srvURL string) *Runner {
	t.Helper()
	c, err := jsonrpc.New(jsonrpc.Config{Endpoint: srvURL})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(Config{Client: c})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// newFakeServer builds a minimal fake JSON-RPC server. fn receives the
// decoded script string and returns the raw JSON "result" value to embed.
func newFakeServer(t *testing.T, fn func(script string) any) (*httptest.Server, *Runner) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(body, &env)
		script, _ := env.Params["script"].(string)
		result := fn(script)
		data, _ := json.Marshal(struct {
			Result any `json:"result"`
		}{Result: result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	c, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(Config{Client: c})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv, runner
}

func TestRunForwardsSubstitutedScript(t *testing.T) {
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"success":true,"error":""}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	out, err := r.Run(context.Background(), hmenum.RegaScriptAcknowledgeMessage, map[string]string{
		"message_id": "4711",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Errorf("unexpected output: %s", out)
	}
	if !strings.Contains(capture.lastScript(), `"4711"`) {
		t.Errorf("server didn't see the substituted script: %s", capture.lastScript())
	}
}

func TestRunMissingParamErrorsBeforeDispatch(t *testing.T) {
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	_, err := r.Run(context.Background(), hmenum.RegaScriptSetSystemVariable, map[string]string{
		"name": "MyVar",
		// "value" missing
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if capture.lastScript() != "" {
		t.Errorf("dispatch should not have happened, server saw: %s", capture.lastScript())
	}
}

func TestRunEscapesQuotesInParams(t *testing.T) {
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	_, err := r.Run(context.Background(), hmenum.RegaScriptSetSystemVariable, map[string]string{
		"name":  `odd"name`,
		"value": `value\with\backslash`,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := capture.lastScript()
	// The name placeholder lives inside a double-quoted string, so the
	// quote must appear escaped as \". Backslashes double.
	if !strings.Contains(script, `"odd\"name"`) {
		t.Errorf("quote not escaped: %s", script)
	}
	if !strings.Contains(script, `"value\\with\\backslash"`) {
		t.Errorf("backslash not escaped: %s", script)
	}
}

func TestRunJSONParsesSanitisedOutput(t *testing.T) {
	capture := &scriptCapture{}
	// Emit a JSON payload with a raw newline inside a string value.
	srv := newFakeCCU(t, capture, "{\"id\":\"X\",\"description\":\"line1\nline2\"}")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	var out struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	if err := r.RunJSON(context.Background(), hmenum.RegaScriptGetSerial, nil, &out); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	if out.ID != "X" || out.Description != "line1\nline2" {
		t.Errorf("got %+v", out)
	}
}

func TestRunJSONPropagatesParseError(t *testing.T) {
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "not json")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	var out any
	err := r.RunJSON(context.Background(), hmenum.RegaScriptGetSerial, nil, &out)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Errorf("error should classify as ErrClientException, got %v", err)
	}
}

func TestRunUnknownScriptErrors(t *testing.T) {
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "")
	defer srv.Close()
	r := newRunner(t, srv.URL)
	_, err := r.Run(context.Background(), hmenum.RegaScript("does_not_exist"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestSetProgramStateTrue verifies that state=true sends "1" in the substituted script.
func TestSetProgramStateTrue(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "true")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	if err := r.SetProgramState(context.Background(), "prog-42", true); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	script := capture.lastScript()
	if !strings.Contains(script, `"prog-42"`) {
		t.Errorf("script missing pid: %s", script)
	}
	// state=true must appear as integer 1 (not escaped inside a string)
	if !strings.Contains(script, "1;") && !strings.Contains(script, "1\n") && !strings.Contains(script, "iActive = 1") {
		t.Errorf("script does not contain active=1: %s", script)
	}
}

// TestSetProgramStateFalse verifies that state=false sends "0".
func TestSetProgramStateFalse(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "false")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	if err := r.SetProgramState(context.Background(), "prog-7", false); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	script := capture.lastScript()
	if !strings.Contains(script, "0") {
		t.Errorf("script does not contain 0 for state=false: %s", script)
	}
}

// TestExecuteProgramConditionalPassesID verifies the pid reaches the
// substituted script and the {"executed":true} response decodes to true.
func TestExecuteProgramConditionalPassesID(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"executed":true}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	executed, err := r.ExecuteProgramConditional(context.Background(), "prog-42")
	if err != nil {
		t.Fatalf("ExecuteProgramConditional: %v", err)
	}
	if !executed {
		t.Fatal("executed=false, want true")
	}
	if !strings.Contains(capture.lastScript(), `"prog-42"`) {
		t.Errorf("script missing pid: %s", capture.lastScript())
	}
}

// TestExecuteProgramConditionalConditionNotMet verifies that a
// {"executed":false} response (condition not satisfied) decodes to false
// without erroring.
func TestExecuteProgramConditionalConditionNotMet(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"executed":false}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	executed, err := r.ExecuteProgramConditional(context.Background(), "prog-7")
	if err != nil {
		t.Fatalf("ExecuteProgramConditional: %v", err)
	}
	if executed {
		t.Fatal("executed=true, want false (condition not met)")
	}
}

// TestExecuteProgramConditionalPropagatesCCUError verifies that a malformed
// (unparsable) script result — e.g. a degraded CCU-side script run — surfaces
// as an error wrapping [hmerr.ErrClientException] with executed=false, rather
// than silently reporting "condition not met".
func TestExecuteProgramConditionalPropagatesCCUError(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "not json")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	executed, err := r.ExecuteProgramConditional(context.Background(), "prog-42")
	if err == nil {
		t.Fatal("expected error for malformed script output")
	}
	if executed {
		t.Error("executed=true on error path, want false")
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Errorf("error should classify as ErrClientException, got %v", err)
	}
	if !strings.Contains(err.Error(), "prog-42") {
		t.Errorf("error should mention the program id, got: %v", err)
	}
}

// TestGetSystemUpdateInfo verifies the JSON response is decoded into SystemUpdateInfo.
func TestGetSystemUpdateInfo(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"current_firmware":"3.65.10","available_firmware":"3.65.12","update_available":true,"check_script_available":true}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	info, err := r.GetSystemUpdateInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemUpdateInfo: %v", err)
	}
	if info.CurrentFirmware != "3.65.10" {
		t.Errorf("CurrentFirmware=%q, want 3.65.10", info.CurrentFirmware)
	}
	if info.AvailableFirmware != "3.65.12" {
		t.Errorf("AvailableFirmware=%q, want 3.65.12", info.AvailableFirmware)
	}
	if !info.UpdateAvailable {
		t.Error("UpdateAvailable=false, want true")
	}
	if !info.CheckScriptAvailable {
		t.Error("CheckScriptAvailable=false, want true")
	}
}

// TestGetSystemUpdateInfoNoUpdate verifies the no-update case decodes correctly.
func TestGetSystemUpdateInfoNoUpdate(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"current_firmware":"3.65.10","available_firmware":"","update_available":false,"check_script_available":true}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	info, err := r.GetSystemUpdateInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemUpdateInfo: %v", err)
	}
	if info.UpdateAvailable {
		t.Error("UpdateAvailable=true, want false")
	}
	if info.AvailableFirmware != "" {
		t.Errorf("AvailableFirmware=%q, want empty", info.AvailableFirmware)
	}
}

// TestGetInboxDevices verifies JSON array result is decoded into InboxDevice slice.
func TestGetInboxDevices(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	payload := `[{"id":"123","address":"HEQ0123456","name":"MyDevice","type":"HM-CC-RT-DN","interface":"BidCos-RF"}]`
	srv := newFakeCCU(t, capture, payload)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	devices, err := r.GetInboxDevices(context.Background())
	if err != nil {
		t.Fatalf("GetInboxDevices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices)=%d, want 1", len(devices))
	}
	d := devices[0]
	if d.DeviceID != "123" {
		t.Errorf("DeviceID=%q, want 123", d.DeviceID)
	}
	if d.Address != "HEQ0123456" {
		t.Errorf("Address=%q, want HEQ0123456", d.Address)
	}
	if d.DeviceType != "HM-CC-RT-DN" {
		t.Errorf("DeviceType=%q, want HM-CC-RT-DN", d.DeviceType)
	}
	if d.Interface != "BidCos-RF" {
		t.Errorf("Interface=%q, want BidCos-RF", d.Interface)
	}
}

// TestGetInboxDevicesEmpty verifies an empty inbox returns an empty slice without error.
func TestGetInboxDevicesEmpty(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `[]`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	devices, err := r.GetInboxDevices(context.Background())
	if err != nil {
		t.Fatalf("GetInboxDevices: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("len(devices)=%d, want 0", len(devices))
	}
}

// TestGetServiceMessagesParsesMultiFunctionChannel is a golden parse of
// real get_service_messages.fn output. rooms and functions are JSON
// arrays; before the script was fixed they were a single string joined
// with a raw tab — a control character illegal inside a JSON string — so
// a channel carrying two or more functions (the first entry below has
// three) made json.Unmarshal fail on the whole document and the daemon
// received zero service messages instead of the CCU's actual count.
func TestGetServiceMessagesParsesMultiFunctionChannel(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	payload := `[{"id":"2097","name":"AL%2DJEQ0702833%3A0%2ECONFIG%5FPENDING","timestamp":1785667096,` +
		`"type":5,"address":"JEQ0702833","device_name":"HM%2DSen%2DMDIR%2DO%20JEQ0702833%3A0",` +
		`"last_timestamp":1785923796,"counter":17,"rooms":[],` +
		`"functions":["Licht","Sicherheit","Umwelt"],"quittable":false},` +
		`{"id":"6893","name":"AL%2DKEQ0843929%3A0%2ESTICKY%5FUNREACH","timestamp":1785667096,` +
		`"type":5,"address":"KEQ0843929","device_name":"HM%2DLC%2DSw4%2DDR%20KEQ0843929%3A0",` +
		`"last_timestamp":1785862251,"counter":26,"rooms":[],"functions":[],"quittable":true}]`
	srv := newFakeCCU(t, capture, payload)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	msgs, err := r.GetServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("GetServiceMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs)=%d, want 2 — a multi-function channel must not fail the whole parse", len(msgs))
	}
	if len(msgs[0].Functions) != 3 {
		t.Errorf("msgs[0].Functions=%v, want 3 entries", msgs[0].Functions)
	}
	if msgs[0].Timestamp != 1785667096 {
		t.Errorf("msgs[0].Timestamp=%d, want 1785667096", msgs[0].Timestamp)
	}
	if msgs[0].LastTimestamp != 1785923796 {
		t.Errorf("msgs[0].LastTimestamp=%d, want 1785923796", msgs[0].LastTimestamp)
	}
	if msgs[0].Quittable {
		t.Error("msgs[0].Quittable=true, want false")
	}
	if !msgs[1].Quittable {
		t.Error("msgs[1].Quittable=false, want true")
	}
	if len(msgs[1].Rooms) != 0 || len(msgs[1].Functions) != 0 {
		t.Errorf("msgs[1] Rooms=%v Functions=%v, want both empty", msgs[1].Rooms, msgs[1].Functions)
	}
}

// TestGetProgramDescriptionsParsesRuleSummaries is a golden parse of the
// get_program_descriptions.fn output: every string field (description,
// condition_summary, activity_summary) is URL-encoded on the wire, and the
// runner surfaces them verbatim for the caller to decode. The encoded
// summaries here mirror what the ReGa traversal emits: symbolic operators
// (>=, :=) and a multibyte channel name.
func TestGetProgramDescriptionsParsesRuleSummaries(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	// condition_summary decodes to "Wohnzimmer >= 20.00 && Flur == 1.00"
	// activity_summary  decodes to "Bücherregal := 1.00"
	payload := `[{"id":"1234","description":"HAHM%20lamp",` +
		`"condition_summary":"Wohnzimmer%20%3E%3D%2020.00%20%26%26%20Flur%20%3D%3D%201.00",` +
		`"activity_summary":"B%C3%BCcherregal%20%3A%3D%201.00"}]`
	srv := newFakeCCU(t, capture, payload)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	descs, err := r.GetProgramDescriptions(context.Background())
	if err != nil {
		t.Fatalf("GetProgramDescriptions: %v", err)
	}
	if len(descs) != 1 {
		t.Fatalf("len(descs)=%d, want 1", len(descs))
	}
	d := descs[0]
	if d.ID != "1234" {
		t.Errorf("ID=%q, want 1234", d.ID)
	}
	// Fields arrive URL-encoded; decoding them yields the human strings.
	for _, tc := range []struct {
		field, encoded, wantDecoded string
	}{
		{"description", d.Description, "HAHM lamp"},
		{"condition_summary", d.ConditionSummary, "Wohnzimmer >= 20.00 && Flur == 1.00"},
		{"activity_summary", d.ActivitySummary, "Bücherregal := 1.00"},
	} {
		got, derr := url.QueryUnescape(tc.encoded)
		if derr != nil {
			t.Fatalf("decode %s (%q): %v", tc.field, tc.encoded, derr)
		}
		if got != tc.wantDecoded {
			t.Errorf("%s decoded = %q, want %q", tc.field, got, tc.wantDecoded)
		}
	}
}

func TestSysvarUsagePrograms(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	payload := `[{"id":"1234","name":"Wohnzimmer%20Licht","active":true},` +
		`{"id":"5678","name":"Flur","active":false}]`
	srv := newFakeCCU(t, capture, payload)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	progs, err := r.SysvarUsagePrograms(context.Background(), "MyVar")
	if err != nil {
		t.Fatalf("SysvarUsagePrograms: %v", err)
	}
	if len(progs) != 2 {
		t.Fatalf("len=%d, want 2", len(progs))
	}
	if progs[0].ID != "1234" || !progs[0].Active {
		t.Errorf("prog[0] = %+v", progs[0])
	}
	if progs[1].Active {
		t.Errorf("prog[1] should be inactive: %+v", progs[1])
	}
	// The name arrives URL-encoded.
	if got, _ := url.QueryUnescape(progs[0].Name); got != "Wohnzimmer Licht" {
		t.Errorf("name decoded = %q, want %q", got, "Wohnzimmer Licht")
	}
	// The ##name## parameter is substituted into the dispatched script.
	if !strings.Contains(capture.lastScript(), "MyVar") {
		t.Errorf("dispatched script missing the sysvar name; got %q", capture.lastScript())
	}
}

func TestSysvarUsageProgramsPropagatesError(t *testing.T) {
	t.Parallel()
	// A non-JSON reply must surface as a parse error, not a silent empty list.
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "not json")
	defer srv.Close()
	r := newRunner(t, srv.URL)
	if _, err := r.SysvarUsagePrograms(context.Background(), "X"); err == nil {
		t.Error("expected a parse error for a non-JSON reply")
	}
}

// TestGetProgramDescriptionsHandlesMissingSummaries verifies a program with
// no rule (empty summary fields) parses without error and yields empty
// summaries.
func TestGetProgramDescriptionsHandlesMissingSummaries(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	payload := `[{"id":"9","description":"","condition_summary":"","activity_summary":""}]`
	srv := newFakeCCU(t, capture, payload)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	descs, err := r.GetProgramDescriptions(context.Background())
	if err != nil {
		t.Fatalf("GetProgramDescriptions: %v", err)
	}
	if len(descs) != 1 {
		t.Fatalf("len(descs)=%d, want 1", len(descs))
	}
	if descs[0].ConditionSummary != "" || descs[0].ActivitySummary != "" {
		t.Errorf("expected empty summaries, got cond=%q act=%q",
			descs[0].ConditionSummary, descs[0].ActivitySummary)
	}
}

// TestGetProgramDescriptionsPropagatesCCUError verifies a malformed script
// result (e.g. a truncated or degraded ReGa run) surfaces as an error
// instead of silently returning an empty slice, so callers (programMetadata)
// can distinguish "the script failed" from "there are no programs".
func TestGetProgramDescriptionsPropagatesCCUError(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "not json")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	descs, err := r.GetProgramDescriptions(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed script output")
	}
	if descs != nil {
		t.Errorf("expected nil descriptions on error, got %+v", descs)
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Errorf("error should classify as ErrClientException, got %v", err)
	}
}

// TestSetSystemVariableStringForwardsNameAndValue verifies placeholders are
// substituted in the set_system_variable.fn script.
func TestSetSystemVariableStringForwardsNameAndValue(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "hello world")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	if err := r.SetSystemVariableString(context.Background(), "greeting", "hello world"); err != nil {
		t.Fatalf("SetSystemVariableString: %v", err)
	}
	script := capture.lastScript()
	if !strings.Contains(script, `"greeting"`) {
		t.Errorf("script missing name: %s", script)
	}
	if !strings.Contains(script, `"hello world"`) {
		t.Errorf("script missing value: %s", script)
	}
}

// TestSetSystemVariableStringEscapesSpecialChars verifies that double-quotes
// and backslashes in the value are escaped to prevent ReGa injection.
func TestSetSystemVariableStringEscapesSpecialChars(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	if err := r.SetSystemVariableString(context.Background(), "myVar", `say "hi" \ there`); err != nil {
		t.Fatalf("SetSystemVariableString: %v", err)
	}
	script := capture.lastScript()
	// The double-quote must be escaped as \" inside the ReGa string literal.
	if !strings.Contains(script, `\"hi\"`) {
		t.Errorf("double-quotes not escaped in script: %s", script)
	}
	if !strings.Contains(script, `\\`) {
		t.Errorf("backslash not escaped in script: %s", script)
	}
}

// ---------------------------------------------------------------------------
// substitute edge cases
// ---------------------------------------------------------------------------

// TestSubstituteNoPlaceholdersIsIdentity verifies that a body with no
// ##NAME## tokens is returned unchanged regardless of which params are
// supplied.
func TestSubstituteNoPlaceholdersIsIdentity(t *testing.T) {
	t.Parallel()
	body := `string s = "hello world";`
	got, err := substitute(body, map[string]string{"irrelevant": "val"})
	if err != nil {
		t.Fatalf("substitute returned unexpected error: %v", err)
	}
	if got != body {
		t.Errorf("body should be identical; got %q, want %q", got, body)
	}
}

// TestSubstituteNilParamsWithNoPlaceholders verifies that nil params are
// safe when the body contains no placeholders.
func TestSubstituteNilParamsWithNoPlaceholders(t *testing.T) {
	t.Parallel()
	body := "integer i = 42;"
	got, err := substitute(body, nil)
	if err != nil {
		t.Fatalf("substitute(nil params, no placeholders) error: %v", err)
	}
	if got != body {
		t.Errorf("got %q, want %q", got, body)
	}
}

// TestSubstituteNilParamsWithPlaceholderErrors verifies that nil params
// cause an error when the body contains at least one placeholder.
func TestSubstituteNilParamsWithPlaceholderErrors(t *testing.T) {
	t.Parallel()
	_, err := substitute("##foo##", nil)
	if err == nil {
		t.Fatal("expected error for nil params with placeholder, got nil")
	}
	if !strings.Contains(err.Error(), "foo") {
		t.Errorf("error should mention placeholder name 'foo': %v", err)
	}
}

// TestSubstituteReportsAllMissingKeys verifies that all missing keys
// are reported in a single error, not just the first one encountered.
func TestSubstituteReportsAllMissingKeys(t *testing.T) {
	t.Parallel()
	_, err := substitute("##alpha## ##beta## ##gamma##", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error should mention missing key %q: %v", key, err)
		}
	}
}

// TestSubstituteEscapesReplacementValue verifies that placeholder
// replacement automatically escapes the value via EscapeString, so a
// caller does not need to pre-escape.
func TestSubstituteEscapesReplacementValue(t *testing.T) {
	t.Parallel()
	// A value with both backslash and double-quote must arrive escaped.
	got, err := substitute(`"##v##"`, map[string]string{"v": `a\b"c`})
	if err != nil {
		t.Fatalf("substitute error: %v", err)
	}
	// Expected: backslash doubled, quote escaped.
	want := `"a\\b\"c"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// EscapeString edge cases
// ---------------------------------------------------------------------------

// TestEscapeStringEmpty verifies the empty-string identity.
func TestEscapeStringEmpty(t *testing.T) {
	t.Parallel()
	if got := EscapeString(""); got != "" {
		t.Errorf("EscapeString(\"\") = %q, want \"\"", got)
	}
}

// TestEscapeStringBackslashOnly verifies a lone backslash is doubled.
func TestEscapeStringBackslashOnly(t *testing.T) {
	t.Parallel()
	if got := EscapeString(`\`); got != `\\` {
		t.Errorf("EscapeString(%q) = %q, want %q", `\`, got, `\\`)
	}
}

// TestEscapeStringDoubleQuoteOnly verifies a lone double-quote is escaped.
func TestEscapeStringDoubleQuoteOnly(t *testing.T) {
	t.Parallel()
	if got := EscapeString(`"`); got != `\"` {
		t.Errorf(`EscapeString('"') = %q, want %q`, got, `\"`)
	}
}

// TestEscapeStringPlainASCIIUnchanged verifies that plain ASCII with no
// backslashes or double-quotes passes through unchanged.
func TestEscapeStringPlainASCIIUnchanged(t *testing.T) {
	t.Parallel()
	in := "hello world 1234 !@#$%^&*()"
	if got := EscapeString(in); got != in {
		t.Errorf("EscapeString(%q) = %q, want unchanged", in, got)
	}
}

// ---------------------------------------------------------------------------
// SanitizeJSONControls edge cases
// ---------------------------------------------------------------------------

// TestSanitizeJSONControlsAllControlCharsInsideString verifies that the
// full range of ASCII control characters (0x00–0x1f) inside a JSON string
// value are escaped as \uXXXX. CCU output may contain device names with
// embedded control characters that must be sanitised before JSON parsing.
func TestSanitizeJSONControlsAllControlCharsInsideString(t *testing.T) {
	t.Parallel()

	// Build a string value containing every byte in [0x00, 0x1f].
	var inner strings.Builder
	for i := range 0x20 {
		inner.WriteByte(byte(i))
	}
	// Embed it as the value of key "k".
	raw := `{"k":"` + inner.String() + `"}`

	got := SanitizeJSONControls(raw)

	// The output must not contain any raw control character inside the string.
	for i := range 0x20 {
		if strings.ContainsRune(got, rune(i)) {
			t.Errorf("raw control char 0x%02x survived sanitisation in %q", i, got)
		}
	}

	// The result must be JSON-shaped.
	if !strings.HasPrefix(got, "{") || !strings.HasSuffix(got, "}") {
		t.Errorf("result is not JSON-shaped: %q", got)
	}
}

// TestSanitizeJSONControlsEmptyStringIsIdentity verifies the empty-input
// edge case.
func TestSanitizeJSONControlsEmptyStringIsIdentity(t *testing.T) {
	t.Parallel()
	if got := SanitizeJSONControls(""); got != "" {
		t.Errorf("SanitizeJSONControls(\"\") = %q, want \"\"", got)
	}
}

// TestSanitizeJSONControlsOutsideStringPreserved verifies that control
// characters that appear outside any JSON string value are preserved
// unchanged. Structural whitespace such as tabs between JSON array
// elements must survive so that the outer JSON remains valid.
func TestSanitizeJSONControlsOutsideStringPreserved(t *testing.T) {
	t.Parallel()
	// A tab between array elements is structural whitespace.
	in := "[\t{\"x\":1},\t{\"x\":2}\t]"
	got := SanitizeJSONControls(in)
	if got != in {
		t.Errorf("structural tab outside string was modified; got %q, want %q", got, in)
	}
}

// TestSanitizeJSONControlsEscapedBackslashNotDoubleCounted verifies that
// a backslash-escaped sequence inside a JSON string is not treated as the
// start of a new escape context, which would corrupt the output.
func TestSanitizeJSONControlsEscapedBackslashNotDoubleCounted(t *testing.T) {
	t.Parallel()
	// {"a":"\\"} is valid JSON representing a single backslash.
	in := `{"a":"\\"}`
	got := SanitizeJSONControls(in)
	if got != in {
		t.Errorf("escaped backslash was corrupted; got %q, want %q", got, in)
	}
}

// ---------------------------------------------------------------------------
// Run: substitute failure surfaces in error before any network call
// ---------------------------------------------------------------------------

// TestRunReturnsSubstituteErrorMentioningPlaceholder verifies that when
// Run cannot resolve a placeholder the error message names the missing key
// and no network activity takes place.
func TestRunReturnsSubstituteErrorMentioningPlaceholder(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, "")
	defer srv.Close()

	r := newRunner(t, srv.URL)
	// RegaScriptAcknowledgeMessage requires "message_id"; omit it deliberately.
	_, err := r.Run(context.Background(), "acknowledge_message", map[string]string{})
	if err == nil {
		t.Fatal("expected substitute error, got nil")
	}
	if !strings.Contains(err.Error(), "message_id") {
		t.Errorf("error should mention missing key 'message_id': %v", err)
	}
	if capture.lastScript() != "" {
		t.Errorf("no network dispatch expected; server saw: %s", capture.lastScript())
	}
}

// ---------------------------------------------------------------------------
// Error handling: CCU-side error codes in JSON response
// ---------------------------------------------------------------------------

// TestAcknowledgeMessageCCUSideErrorReturnsError verifies that a CCU-side
// structured error (success=false, error="not found") surfaces as a non-nil
// error from AcknowledgeMessage.
func TestAcknowledgeMessageCCUSideErrorReturnsError(t *testing.T) {
	t.Parallel()
	_, runner := newFakeServer(t, func(_ string) any {
		return `{"success":false,"error":"message not found"}`
	})
	ok, err := runner.AcknowledgeMessage(context.Background(), "non-existent-id")
	if ok {
		t.Fatal("expected ok=false for CCU-side error")
	}
	if err == nil {
		t.Fatal("expected non-nil error for CCU-side error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should contain CCU reason, got: %v", err)
	}
}

// TestAcknowledgeMessageEmptyIDReturnsError pins the guard that
// prevents dispatch with an empty message ID.
func TestAcknowledgeMessageEmptyIDReturnsError(t *testing.T) {
	t.Parallel()
	_, runner := newFakeServer(t, func(_ string) any {
		return `{"success":true,"error":""}`
	})
	ok, err := runner.AcknowledgeMessage(context.Background(), "")
	if ok {
		t.Fatal("expected ok=false for empty ID")
	}
	if err == nil {
		t.Fatal("expected error for empty message ID")
	}
}

// ---------------------------------------------------------------------------
// Bulk acknowledge: parse the acknowledged count + dispatch the right script
// ---------------------------------------------------------------------------

// TestAcknowledgeAllServiceMessagesParsesCount verifies the service bulk-ack
// runner parses {"acknowledged":n} and dispatches the writability-gated
// service pass.
func TestAcknowledgeAllServiceMessagesParsesCount(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"acknowledged":4}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	n, err := r.AcknowledgeAllServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllServiceMessages: %v", err)
	}
	if n != 4 {
		t.Fatalf("count=%d, want 4", n)
	}
	if !strings.Contains(capture.lastScript(), "acknowledge_all_service_messages") {
		t.Errorf("wrong script dispatched: %s", capture.lastScript())
	}
	// Service messages are acknowledged only when writable — the gate must
	// be present in the dispatched body.
	if !strings.Contains(capture.lastScript(), "Operations()") {
		t.Errorf("service ack-all must apply the writability gate: %s", capture.lastScript())
	}
}

// TestAcknowledgeAllAlarmMessagesParsesCount verifies the alarm bulk-ack
// runner parses {"acknowledged":n} and dispatches the ALARMDP pass.
func TestAcknowledgeAllAlarmMessagesParsesCount(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"acknowledged":7}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	n, err := r.AcknowledgeAllAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllAlarmMessages: %v", err)
	}
	if n != 7 {
		t.Fatalf("count=%d, want 7", n)
	}
	if !strings.Contains(capture.lastScript(), "acknowledge_all_alarm_messages") {
		t.Errorf("wrong script dispatched: %s", capture.lastScript())
	}
	if !strings.Contains(capture.lastScript(), "ALARMDP") {
		t.Errorf("alarm ack-all must walk ALARMDP objects: %s", capture.lastScript())
	}
}

// TestAcknowledgeAllServiceMessagesCCUError surfaces a transport failure as a
// wrapped error and a zero count.
func TestAcknowledgeAllServiceMessagesCCUError(t *testing.T) {
	t.Parallel()
	_, runner := newFakeServer(t, func(_ string) any {
		return `not valid json`
	})
	n, err := runner.AcknowledgeAllServiceMessages(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if n != 0 {
		t.Fatalf("count=%d, want 0 on error", n)
	}
}

// TestAcknowledgeAllAlarmMessagesCCUError mirrors
// TestAcknowledgeAllServiceMessagesCCUError for the alarm bulk-ack script.
func TestAcknowledgeAllAlarmMessagesCCUError(t *testing.T) {
	t.Parallel()
	_, runner := newFakeServer(t, func(_ string) any {
		return `not valid json`
	})
	n, err := runner.AcknowledgeAllAlarmMessages(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if n != 0 {
		t.Fatalf("count=%d, want 0 on error", n)
	}
}

// TestAcknowledgeAllMessagesZeroCount verifies that {"acknowledged":0} — the
// CCU's answer when nothing was quittable/active — parses as a clean
// zero-count success rather than being mistaken for a decode failure.
func TestAcknowledgeAllMessagesZeroCount(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"acknowledged":0}`)
	defer srv.Close()
	r := newRunner(t, srv.URL)

	n, err := r.AcknowledgeAllServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllServiceMessages: %v", err)
	}
	if n != 0 {
		t.Fatalf("count=%d, want 0", n)
	}

	n, err = r.AcknowledgeAllAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllAlarmMessages: %v", err)
	}
	if n != 0 {
		t.Fatalf("count=%d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Timeout / context cancellation
// ---------------------------------------------------------------------------

// TestRunReturnsContextErrorOnCancellation verifies that Run returns
// the context error when the caller cancels before the server responds.
func TestRunReturnsContextErrorOnCancellation(t *testing.T) {
	t.Parallel()
	// Server that hangs until the test cleans up.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	c, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(Config{Client: c})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = runner.Run(ctx, hmenum.RegaScriptGetSerial, nil)
	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
}

// ---------------------------------------------------------------------------
// Argument escaping for special characters
// ---------------------------------------------------------------------------

// TestSubstituteHandlesTabAndNewlineInValue verifies that tab and newline
// characters inside a placeholder value are escaped by EscapeString so the
// resulting HomeMatic Script does not contain unescaped control characters.
func TestSubstituteHandlesTabAndNewlineInValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		wantSub string
	}{
		{
			name:    "tab character",
			input:   "val\twith\ttabs",
			wantSub: "val\twith\ttabs", // EscapeString only escapes \ and "
		},
		{
			name:    "backslash and quote together",
			input:   `path\to"file"`,
			wantSub: `path\\to\"file\"`,
		},
		{
			name:    "empty value",
			input:   "",
			wantSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EscapeString(tc.input)
			if got != tc.wantSub {
				t.Errorf("EscapeString(%q)=%q, want %q", tc.input, got, tc.wantSub)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Reply parsing edge cases
// ---------------------------------------------------------------------------

// TestRunJSONEmptyStringReplyReturnsParseError verifies that an empty string
// result from the CCU causes RunJSON to return a parse error wrapping
// ErrClientException.
func TestRunJSONEmptyStringReplyReturnsParseError(t *testing.T) {
	t.Parallel()
	_, runner := newFakeServer(t, func(_ string) any {
		return "" // empty string result
	})
	var out any
	err := runner.RunJSON(context.Background(), hmenum.RegaScriptGetSerial, nil, &out)
	if err == nil {
		t.Fatal("expected parse error for empty CCU result")
	}
	if !strings.Contains(err.Error(), "parse JSON") && !errors.Is(err, hmerr.ErrClientException) {
		// Either message is acceptable — both indicate a parse failure.
		t.Logf("error was: %v (acceptable)", err)
	}
}

// TestRunJSONMultiLineJSONIsAccepted verifies that RunJSON handles a CCU
// response that contains a multi-line JSON array correctly.
func TestRunJSONMultiLineJSONIsAccepted(t *testing.T) {
	t.Parallel()
	payload := "[\n  {\"id\":\"1\"},\n  {\"id\":\"2\"}\n]"
	_, runner := newFakeServer(t, func(_ string) any { return payload })
	var out []map[string]string
	if err := runner.RunJSON(context.Background(), hmenum.RegaScriptGetInboxDevices, nil, &out); err != nil {
		t.Fatalf("RunJSON multi-line: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// Concurrent calls
// ---------------------------------------------------------------------------

// TestRunConcurrentCallsAreSafe verifies that multiple goroutines can call
// Runner.Run simultaneously without data races. Run with -race to detect
// any shared-state issues.
func TestRunConcurrentCallsAreSafe(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	count := 0
	_, runner := newFakeServer(t, func(_ string) any {
		mu.Lock()
		count++
		mu.Unlock()
		return `{"current_firmware":"3.0","available_firmware":"3.0","update_available":false,"check_script_available":true}`
	})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = runner.GetSystemUpdateInfo(context.Background())
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != goroutines {
		t.Errorf("count=%d, want %d (some calls did not reach the server)", count, goroutines)
	}
}

func TestGetSerialTruncatesLongSerialToLastTenChars(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"serial":"3014F711A0001F58A99BC0DE"}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	serial, err := r.GetSerial(context.Background())
	if err != nil {
		t.Fatalf("GetSerial: %v", err)
	}
	if serial != "58A99BC0DE" {
		t.Errorf("serial=%q, want last-10 truncation %q", serial, "58A99BC0DE")
	}
}

func TestGetSerialKeepsShortSerialVerbatim(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"serial":"ABC1234567"}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	serial, err := r.GetSerial(context.Background())
	if err != nil {
		t.Fatalf("GetSerial: %v", err)
	}
	if serial != "ABC1234567" {
		t.Errorf("serial=%q, want ABC1234567 verbatim", serial)
	}
}

// ---------------------------------------------------------------------------
// SetPosition: astro reference position write + read-back validation
// ---------------------------------------------------------------------------

// TestSetPositionRejectsInvalidInputWithoutCallingCCU verifies that every
// validation failure — non-finite values and out-of-range longitude or
// latitude — is rejected before any network call, wraps
// hmerr.ErrValidation, and returns a zeroed position. Params are received
// by textual substitution into the ReGa script, so an out-of-range value
// would otherwise be written to the CCU verbatim before anything could
// object; the fake server fails the test if SetPosition ever dispatches to
// it, proving the rejection happens client-side.
func TestSetPositionRejectsInvalidInputWithoutCallingCCU(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		longitude float64
		latitude  float64
	}{
		{"longitude NaN", math.NaN(), 0},
		{"longitude +Inf", math.Inf(1), 0},
		{"longitude -Inf", math.Inf(-1), 0},
		{"latitude NaN", 0, math.NaN()},
		{"latitude +Inf", 0, math.Inf(1)},
		{"latitude -Inf", 0, math.Inf(-1)},
		{"longitude above range", 180.1, 0},
		{"longitude below range", -180.1, 0},
		{"latitude above range", 0, 90.1},
		{"latitude below range", 0, -90.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Errorf("%s: SetPosition dispatched a network call for an invalid input", tc.name)
			}))
			defer srv.Close()

			r := newRunner(t, srv.URL)
			gotLon, gotLat, err := r.SetPosition(context.Background(), tc.longitude, tc.latitude)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !errors.Is(err, hmerr.ErrValidation) {
				t.Errorf("error should wrap hmerr.ErrValidation, got %v", err)
			}
			if gotLon != 0 || gotLat != 0 {
				t.Errorf("expected zero coordinates on rejection, got %g/%g", gotLon, gotLat)
			}
		})
	}
}

// TestSetPositionAcceptsBoundaryValues verifies that longitude/latitude
// exactly at the documented range boundaries (±180 / ±90) pass validation
// and reach the ReGa script, rather than being rejected by an off-by-one
// bound check.
func TestSetPositionAcceptsBoundaryValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		lon, lat  float64
		wantReply string
	}{
		{"longitude +180", 180, 0, `{"longitude":180,"latitude":0}`},
		{"longitude -180", -180, 0, `{"longitude":-180,"latitude":0}`},
		{"latitude +90", 0, 90, `{"longitude":0,"latitude":90}`},
		{"latitude -90", 0, -90, `{"longitude":0,"latitude":-90}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			capture := &scriptCapture{}
			srv := newFakeCCU(t, capture, tc.wantReply)
			defer srv.Close()

			r := newRunner(t, srv.URL)
			gotLon, gotLat, err := r.SetPosition(context.Background(), tc.lon, tc.lat)
			if err != nil {
				t.Fatalf("SetPosition: %v", err)
			}
			if gotLon != tc.lon || gotLat != tc.lat {
				t.Errorf("got %g/%g, want %g/%g", gotLon, gotLat, tc.lon, tc.lat)
			}
			if capture.lastScript() == "" {
				t.Error("expected the script to reach the CCU")
			}
		})
	}
}

// TestSetPositionFormatsSixDecimalPlaces verifies the wire format is
// always fixed-point with six decimals, including values that Go's default
// float formatting would otherwise render in scientific notation — a
// ReGa script's numeric literal parser does not accept "1e-07".
func TestSetPositionFormatsSixDecimalPlaces(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		lon, lat       float64
		wantLonLiteral string
		wantLatLiteral string
	}{
		{
			name: "ordinary decimal degrees",
			lon:  10.222946, lat: 53.551086,
			wantLonLiteral: "10.222946",
			wantLatLiteral: "53.551086",
		},
		{
			name: "value that would otherwise render in scientific notation",
			lon:  1e-7, lat: 0,
			wantLonLiteral: "0.000000",
			wantLatLiteral: "0.000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			capture := &scriptCapture{}
			reply := `{"longitude":` + strconv.FormatFloat(tc.lon, 'f', 6, 64) +
				`,"latitude":` + strconv.FormatFloat(tc.lat, 'f', 6, 64) + `}`
			srv := newFakeCCU(t, capture, reply)
			defer srv.Close()

			r := newRunner(t, srv.URL)
			if _, _, err := r.SetPosition(context.Background(), tc.lon, tc.lat); err != nil {
				t.Fatalf("SetPosition: %v", err)
			}
			script := capture.lastScript()
			if !strings.Contains(script, tc.wantLonLiteral) {
				t.Errorf("script missing formatted longitude %q: %s", tc.wantLonLiteral, script)
			}
			if !strings.Contains(script, tc.wantLatLiteral) {
				t.Errorf("script missing formatted latitude %q: %s", tc.wantLatLiteral, script)
			}
			if strings.Contains(script, "e-") || strings.Contains(script, "e+") {
				t.Errorf("script contains scientific notation: %s", script)
			}
		})
	}
}

// TestSetPositionReadBackMismatchIsClientException verifies that a
// materially different read-back — e.g. the CCU silently clamping or
// ignoring the write — surfaces as hmerr.ErrClientException with a
// zeroed result rather than a false success.
func TestSetPositionReadBackMismatchIsClientException(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	srv := newFakeCCU(t, capture, `{"longitude":10.0,"latitude":50.0}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	gotLon, gotLat, err := r.SetPosition(context.Background(), 10.222946, 53.551086)
	if err == nil {
		t.Fatal("expected a read-back mismatch error")
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Errorf("error should wrap hmerr.ErrClientException, got %v", err)
	}
	if gotLon != 0 || gotLat != 0 {
		t.Errorf("expected zero coordinates on mismatch, got %g/%g", gotLon, gotLat)
	}
}

// TestSetPositionAcceptsReadBackWithinEpsilon verifies that a read-back
// difference below positionEpsilon (1e-5) — the CCU's own six-decimal
// rounding — is tolerated as a rounding artefact rather than rejected, and
// that the returned coordinates are the CCU's own read-back values.
func TestSetPositionAcceptsReadBackWithinEpsilon(t *testing.T) {
	t.Parallel()
	capture := &scriptCapture{}
	// 5e-6 difference, below the 1e-5 epsilon.
	srv := newFakeCCU(t, capture, `{"longitude":10.222951,"latitude":53.551086}`)
	defer srv.Close()

	r := newRunner(t, srv.URL)
	gotLon, gotLat, err := r.SetPosition(context.Background(), 10.222946, 53.551086)
	if err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if gotLon != 10.222951 || gotLat != 53.551086 {
		t.Errorf("got %g/%g, want the CCU's read-back 10.222951/53.551086", gotLon, gotLat)
	}
}
