// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── helper ───────────────────────────────────────────────────────────────────

// newDevicesServer registers route handlers by path prefix and returns a test
// server. The caller maps path prefix → HandlerFunc; unmatched paths reply 404.
func newDevicesServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// writeJSON200 is a helper that marshals v and writes it with a 200 header.
func writeJSON200(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ─── devices list ─────────────────────────────────────────────────────────────

func TestDevicesListCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, deviceListResponse{
				Items: []deviceSummary{
					{Address: "AABBCC", Model: "HmIP-PS", Name: "Lamp", Interface: "HmIP-RF", Available: true},
				},
				Total: 1,
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/devices" {
		t.Errorf("path=%q, want /api/v1/devices", gotPath)
	}
}

func TestDevicesListPrintsTableHeader(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceListResponse{
				Items: []deviceSummary{
					{Address: "DEV001", Model: "HmIP-PS", Name: "Socket", Interface: "HmIP-RF"},
				},
				Total: 1,
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"ADDRESS", "MODEL", "NAME", "INTERFACE"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing column header %q:\n%s", want, out)
		}
	}
}

func TestDevicesListShowsCentralColumnWhenMultipleCentrals(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceListResponse{
				Items: []deviceSummary{
					{Address: "A1", Model: "M", Name: "D1", Interface: "HmIP-RF", Central: "ccu1"},
					{Address: "A2", Model: "M", Name: "D2", Interface: "HmIP-RF", Central: "ccu2"},
				},
				Total: 2,
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "CENTRAL") {
		t.Errorf("expected CENTRAL column for multi-central result:\n%s", stdout.String())
	}
}

func TestDevicesListOmitsCentralColumnWhenSingleCentral(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceListResponse{
				Items: []deviceSummary{
					{Address: "A1", Model: "M", Name: "D1", Interface: "HmIP-RF", Central: "ccu1"},
					{Address: "A2", Model: "M", Name: "D2", Interface: "HmIP-RF", Central: "ccu1"},
				},
				Total: 2,
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(stdout.String(), "CENTRAL") {
		t.Errorf("did not expect CENTRAL column for single-central result:\n%s", stdout.String())
	}
}

func TestDevicesListPrintsTotalCount(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceListResponse{Items: []deviceSummary{}, Total: 42})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "42") {
		t.Errorf("expected total count 42 in output:\n%s", stdout.String())
	}
}

func TestDevicesListJSONFlagEmitsRawJSON(t *testing.T) {
	t.Parallel()
	item := deviceSummary{Address: "AAAA", Model: "HmIP-PS", Name: "Test", Interface: "HmIP-RF"}
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceListResponse{Items: []deviceSummary{item}, Total: 1})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got []deviceSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\noutput: %s", err, stdout.String())
	}
	if len(got) != 1 || got[0].Address != "AAAA" {
		t.Errorf("unexpected JSON output: %+v", got)
	}
}

func TestDevicesListNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		},
	})
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}

// ─── devices get ──────────────────────────────────────────────────────────────

func TestDevicesGetCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/DEV001": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, deviceDetail{
				deviceSummary: deviceSummary{
					Address: "DEV001", Model: "HmIP-PS", Name: "Socket",
					Interface: "HmIP-RF", Available: true,
				},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "get", "--host", ts.URL, "DEV001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/devices/DEV001" {
		t.Errorf("path=%q, want /api/v1/devices/DEV001", gotPath)
	}
}

func TestDevicesGetPrintsDeviceFields(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/ABC123": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceDetail{
				deviceSummary: deviceSummary{
					Address: "ABC123", Model: "HmIP-PSM", Name: "PowerSocket",
					Interface: "HmIP-RF", Central: "main-ccu", Available: true,
				},
				Channels: []channelSummary{
					{Number: 0, Address: "ABC123:0", Type: "MAINTENANCE", Name: "CH0"},
					{Number: 1, Address: "ABC123:1", Type: "SWITCH", Name: "CH1"},
				},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "get", "--host", ts.URL, "ABC123"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"ABC123", "HmIP-PSM", "PowerSocket", "HmIP-RF", "main-ccu", "SWITCH"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDevicesGetMissingAddressReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices", "get", "--host", "http://localhost:1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when address is missing")
	}
}

func TestDevicesGetJSONFlagEmitsRawJSON(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/XYZ": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceDetail{
				deviceSummary: deviceSummary{Address: "XYZ", Model: "HmIP-PS", Name: "N", Interface: "HmIP-RF"},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "get", "--host", ts.URL, "--json", "XYZ"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	if got["address"] != "XYZ" {
		t.Errorf("address=%v, want XYZ", got["address"])
	}
}

// ─── devices get-value ────────────────────────────────────────────────────────

func TestDevicesGetValueCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/DEV:0/channels/1/data-points/STATE": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, dataPointSummary{Parameter: "STATE", Value: true})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "get-value", "--host", ts.URL, "DEV:0", "1", "STATE"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	want := "/api/v1/devices/DEV:0/channels/1/data-points/STATE"
	if gotPath != want {
		t.Errorf("path=%q, want %q", gotPath, want)
	}
}

func TestDevicesGetValuePrintsValue(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/ADDR/channels/2/data-points/LEVEL": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, dataPointSummary{Parameter: "LEVEL", Value: 0.75})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "get-value", "--host", ts.URL, "ADDR", "2", "LEVEL"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "0.75") {
		t.Errorf("expected 0.75 in output, got: %q", stdout.String())
	}
}

func TestDevicesGetValueJSONFlagEmitsFullDTO(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/A/channels/0/data-points/RSSI_DEVICE": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, dataPointSummary{Parameter: "RSSI_DEVICE", Value: -67.0, Observed: true})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "get-value", "--host", ts.URL, "--json", "A", "0", "RSSI_DEVICE"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got dataPointSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	if got.Parameter != "RSSI_DEVICE" {
		t.Errorf("parameter=%q, want RSSI_DEVICE", got.Parameter)
	}
}

func TestDevicesGetValueMissingArgsReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices", "get-value", "ADDR", "1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when parameter is missing")
	}
}

// ─── devices set ──────────────────────────────────────────────────────────────

func TestDevicesSetCallsCorrectEndpointAndMethod(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/DEV/channels/1/data-points/STATE/value": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "set", "--host", ts.URL, "DEV", "1", "STATE", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	want := "/api/v1/devices/DEV/channels/1/data-points/STATE/value"
	if gotPath != want {
		t.Errorf("path=%q, want %q", gotPath, want)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method=%q, want PUT", gotMethod)
	}
}

func TestDevicesSetSendsBooleanValue(t *testing.T) {
	t.Parallel()
	var gotBody setValueRequest
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/channels/1/data-points/ON/value": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "set", "--host", ts.URL, "D", "1", "ON", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotBody.Value != true {
		t.Errorf("value=%v (type %T), want true (bool)", gotBody.Value, gotBody.Value)
	}
}

func TestDevicesSetSendsIntegerValue(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/channels/1/data-points/LEVEL/value": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "set", "--host", ts.URL, "D", "1", "LEVEL", "42"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	// JSON numbers decode to float64 by default; 42 → 42.0 is fine.
	if gotBody["value"] != float64(42) {
		t.Errorf("value=%v (type %T), want 42", gotBody["value"], gotBody["value"])
	}
}

func TestDevicesSetSendsStringValue(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/channels/1/data-points/NAME/value": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "set", "--host", ts.URL, "D", "1", "NAME", "hello"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotBody["value"] != "hello" {
		t.Errorf("value=%v, want hello", gotBody["value"])
	}
}

func TestDevicesSetWithPriorityFlag(t *testing.T) {
	t.Parallel()
	var gotBody setValueRequest
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/channels/1/data-points/STATE/value": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "set", "--host", ts.URL, "--priority", "high", "D", "1", "STATE", "false"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotBody.Priority != "high" {
		t.Errorf("priority=%q, want high", gotBody.Priority)
	}
}

func TestDevicesSetPrintsOkOnSuccess(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/channels/1/data-points/STATE/value": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "set", "--host", ts.URL, "D", "1", "STATE", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("expected 'ok' in stdout, got: %q", stdout.String())
	}
}

func TestDevicesSetMissingArgsReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices", "set", "ADDR", "1", "PARAM"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when value is missing")
	}
}

func TestDevicesSetNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/channels/1/data-points/STATE/value": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		},
	})
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices", "set", "--host", ts.URL, "D", "1", "STATE", "true"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention 403, got: %v", err)
	}
}

// ─── routing ──────────────────────────────────────────────────────────────────

func TestDevicesMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when devices has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestDevicesUnknownOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"devices", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown devices operation")
	}
}

// ─── coerceValue ──────────────────────────────────────────────────────────────

func TestCoerceValueBool(t *testing.T) {
	t.Parallel()
	if v := coerceValue("true"); v != true {
		t.Errorf("coerceValue(true)=%v (%T), want bool true", v, v)
	}
	if v := coerceValue("false"); v != false {
		t.Errorf("coerceValue(false)=%v (%T), want bool false", v, v)
	}
	if v := coerceValue("True"); v != true {
		t.Errorf("coerceValue(True)=%v (%T), want bool true (case-insensitive)", v, v)
	}
}

func TestCoerceValueInt(t *testing.T) {
	t.Parallel()
	if v := coerceValue("99"); v != int64(99) {
		t.Errorf("coerceValue(99)=%v (%T), want int64(99)", v, v)
	}
	if v := coerceValue("-5"); v != int64(-5) {
		t.Errorf("coerceValue(-5)=%v (%T), want int64(-5)", v, v)
	}
}

func TestCoerceValueFloat(t *testing.T) {
	t.Parallel()
	if v := coerceValue("3.14"); v != 3.14 {
		t.Errorf("coerceValue(3.14)=%v (%T), want float64(3.14)", v, v)
	}
}

func TestCoerceValueString(t *testing.T) {
	t.Parallel()
	if v := coerceValue("hello"); v != "hello" {
		t.Errorf("coerceValue(hello)=%v (%T), want string", v, v)
	}
}
