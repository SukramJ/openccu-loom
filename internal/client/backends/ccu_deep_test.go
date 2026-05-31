// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Helpers shared across this file.
// ---------------------------------------------------------------------------

// loadArgs extracts the (method, args) pair stored by fakeCaller.
func loadArgs(f *fakeCaller) (method string, args []any, ok bool) {
	stored, exists := f.lastArg.Load().([]any)
	if !exists || len(stored) != 2 {
		return "", nil, false
	}
	method, _ = stored[0].(string)
	args, _ = stored[1].([]any)
	return method, args, true
}

// ---------------------------------------------------------------------------
// GetParamset
// ---------------------------------------------------------------------------

func TestCcuBackendGetParamsetReturnsValues(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: map[string]any{"TEMPERATURE": 21.5, "HUMIDITY": 55}}
	b := NewCcuBackend(x, nil, nil)

	out, err := b.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("GetParamset: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len=%d, want 2", len(out))
	}
	if out["TEMPERATURE"].(float64) != 21.5 {
		t.Fatalf("TEMPERATURE=%v", out["TEMPERATURE"])
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "getParamset" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 2 || args[0] != "0001ABCD:1" || args[1] != "VALUES" {
		t.Fatalf("args=%v", args)
	}
}

func TestCcuBackendGetParamsetWithoutXMLRPC(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, nil, nil)
	_, err := b.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues)
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// PutParamset
// ---------------------------------------------------------------------------

func TestCcuBackendPutParamsetDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	values := map[string]any{"SET_TEMPERATURE": 22.0}
	if err := b.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyMaster, values, hmenum.CommandRxModeUnset); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "putParamset" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 3 {
		t.Fatalf("arg count=%d, want 3", len(args))
	}
	if args[0] != "0001ABCD:1" {
		t.Fatalf("args[0]=%v, want 0001ABCD:1", args[0])
	}
	if args[1] != "MASTER" {
		t.Fatalf("args[1]=%v, want MASTER", args[1])
	}
	vals, ok2 := args[2].(map[string]any)
	if !ok2 || vals["SET_TEMPERATURE"].(float64) != 22.0 {
		t.Fatalf("args[2]=%v", args[2])
	}
}

// ---------------------------------------------------------------------------
// GetValue
// ---------------------------------------------------------------------------

func TestCcuBackendGetValueDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: true}
	b := NewCcuBackend(x, nil, nil)

	v, err := b.GetValue(context.Background(), "0001ABCD:1", hmenum.ParameterState)
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if v.(bool) != true {
		t.Fatalf("v=%v", v)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "getValue" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 2 || args[0] != "0001ABCD:1" || args[1] != string(hmenum.ParameterState) {
		t.Fatalf("args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// GetLinks
// ---------------------------------------------------------------------------

func TestCcuBackendGetLinksDecodesArray(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: []any{
		map[string]any{
			"SENDER":      "0001ABCD:1",
			"RECEIVER":    "0002DCBA:1",
			"NAME":        "Treppe",
			"DESCRIPTION": "Auto",
			"FLAGS":       3,
		},
	}}
	b := NewCcuBackend(x, nil, nil)

	links, err := b.GetLinks(context.Background(), "0001ABCD:1")
	if err != nil {
		t.Fatalf("GetLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len=%d, want 1", len(links))
	}
	ld := links[0]
	if ld.Sender != "0001ABCD:1" {
		t.Fatalf("Sender=%s", ld.Sender)
	}
	if ld.Receiver != "0002DCBA:1" {
		t.Fatalf("Receiver=%s", ld.Receiver)
	}
	if ld.Name != "Treppe" {
		t.Fatalf("Name=%s", ld.Name)
	}
	if ld.Flags != 3 {
		t.Fatalf("Flags=%d, want 3", ld.Flags)
	}

	// Verify getLinks is called with flags=0 (full detail).
	method, args, ok := loadArgs(x)
	if !ok || method != "getLinks" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 2 || args[0] != "0001ABCD:1" || args[1] != 0 {
		t.Fatalf("args=%v", args)
	}
}

func TestCcuBackendGetLinksSkipsEntriesWithoutSenderReceiver(t *testing.T) {
	t.Parallel()
	// Entry without SENDER should be silently dropped.
	x := &fakeCaller{reply: []any{
		map[string]any{"NAME": "no sender"},
		map[string]any{"SENDER": "0001ABCD:1", "RECEIVER": "0002DCBA:1", "NAME": "ok"},
	}}
	b := NewCcuBackend(x, nil, nil)
	links, err := b.GetLinks(context.Background(), "0001ABCD:1")
	if err != nil {
		t.Fatalf("GetLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 valid link (incomplete entry dropped), got %d", len(links))
	}
}

// ---------------------------------------------------------------------------
// GetLinkPeers
// ---------------------------------------------------------------------------

func TestCcuBackendGetLinkPeersDecodesStrings(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: []any{"peer1", "peer2"}}
	b := NewCcuBackend(x, nil, nil)

	peers, err := b.GetLinkPeers(context.Background(), "0001ABCD:1")
	if err != nil {
		t.Fatalf("GetLinkPeers: %v", err)
	}
	if len(peers) != 2 || peers[0] != "peer1" || peers[1] != "peer2" {
		t.Fatalf("peers=%v", peers)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "getLinkPeers" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 1 || args[0] != "0001ABCD:1" {
		t.Fatalf("args=%v", args)
	}
}

func TestCcuBackendGetLinkPeersDropsEmpty(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: []any{"peer1", "", "peer2"}}
	b := NewCcuBackend(x, nil, nil)
	peers, err := b.GetLinkPeers(context.Background(), "0001ABCD:1")
	if err != nil {
		t.Fatalf("GetLinkPeers: %v", err)
	}
	// Empty string peers must be dropped.
	if len(peers) != 2 {
		t.Fatalf("len=%d, want 2 (empty dropped)", len(peers))
	}
}

// ---------------------------------------------------------------------------
// AddLink
// ---------------------------------------------------------------------------

func TestCcuBackendAddLinkDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	if err := b.AddLink(context.Background(), "0001ABCD:1", "0002DCBA:1", "Bind", "auto"); err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "addLink" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 4 {
		t.Fatalf("arg count=%d, want 4", len(args))
	}
	if args[0] != "0001ABCD:1" || args[1] != "0002DCBA:1" || args[2] != "Bind" || args[3] != "auto" {
		t.Fatalf("args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// RemoveLink
// ---------------------------------------------------------------------------

func TestCcuBackendRemoveLinkDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	if err := b.RemoveLink(context.Background(), "0001ABCD:1", "0002DCBA:1"); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "removeLink" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 2 || args[0] != "0001ABCD:1" || args[1] != "0002DCBA:1" {
		t.Fatalf("args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// GetLinkParamsetDescription — uses literal "LINK" key, peer ignored
// ---------------------------------------------------------------------------

func TestCcuBackendGetLinkParamsetDescriptionDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: map[string]any{
		"SHORT_ACTION_TYPE": map[string]any{"TYPE": "INTEGER", "OPERATIONS": 1},
	}}
	b := NewCcuBackend(x, nil, nil)

	out, err := b.GetLinkParamsetDescription(context.Background(), "0001ABCD:1", "0002DCBA:1")
	if err != nil {
		t.Fatalf("GetLinkParamsetDescription: %v", err)
	}
	if _, ok := out["SHORT_ACTION_TYPE"]; !ok {
		t.Fatalf("missing SHORT_ACTION_TYPE in %v", out)
	}

	// The CCU contract: description is always keyed by literal "LINK".
	method, args, ok := loadArgs(x)
	if !ok || method != "getParamsetDescription" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 2 || args[0] != "0001ABCD:1" || args[1] != "LINK" {
		t.Fatalf("args=%v, want [0001ABCD:1 LINK]", args)
	}
}

// ---------------------------------------------------------------------------
// GetLinkParamset / PutLinkParamset — keyed by peer address (not "LINK")
// ---------------------------------------------------------------------------

func TestCcuBackendGetLinkParamsetDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: map[string]any{"SHORT_ACTION_TYPE": 1}}
	b := NewCcuBackend(x, nil, nil)

	out, err := b.GetLinkParamset(context.Background(), "0001ABCD:1", "0002DCBA:1")
	if err != nil {
		t.Fatalf("GetLinkParamset: %v", err)
	}
	if out["SHORT_ACTION_TYPE"].(int) != 1 {
		t.Fatalf("val=%v", out["SHORT_ACTION_TYPE"])
	}

	// Values are keyed by peer address, not "LINK".
	method, args, ok := loadArgs(x)
	if !ok || method != "getParamset" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 2 || args[0] != "0001ABCD:1" || args[1] != "0002DCBA:1" {
		t.Fatalf("args=%v, want [0001ABCD:1 0002DCBA:1]", args)
	}
}

func TestCcuBackendPutLinkParamsetDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	values := map[string]any{"SHORT_ACTION_TYPE": 2}
	if err := b.PutLinkParamset(context.Background(), "0001ABCD:1", "0002DCBA:1", values); err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "putParamset" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 3 || args[0] != "0001ABCD:1" || args[1] != "0002DCBA:1" {
		t.Fatalf("args=%v, want [0001ABCD:1 0002DCBA:1 ...]", args)
	}
}

// ---------------------------------------------------------------------------
// Init / Deinit — delegate to Announcer
// ---------------------------------------------------------------------------

func TestCcuBackendInitDelegatesToAnnouncer(t *testing.T) {
	t.Parallel()
	ann := &recordingAnnouncer{}
	b := NewCcuBackend(&fakeCaller{}, nil, ann)
	if err := b.Init(context.Background(), "HmIP-RF", "http://10.0.0.1:8120/RPC2/ccu"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := b.Deinit(context.Background(), "HmIP-RF"); err != nil {
		t.Fatalf("Deinit: %v", err)
	}
	if ann.inits != 1 || ann.deinits != 1 {
		t.Fatalf("inits=%d deinits=%d", ann.inits, ann.deinits)
	}
}

func TestCcuBackendInitWithoutAnnouncerIsNoop(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	if err := b.Init(context.Background(), "HmIP-RF", "http://cb"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := b.Deinit(context.Background(), "HmIP-RF"); err != nil {
		t.Fatalf("Deinit: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ping
// ---------------------------------------------------------------------------

func TestCcuBackendPingDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	if err := b.Ping(context.Background(), "HmIP-RF"); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	method, args, ok := loadArgs(x)
	if !ok || method != "ping" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 1 || args[0] != "HmIP-RF" {
		t.Fatalf("args=%v", args)
	}
}

func TestCcuBackendPingWithoutXMLRPCErrors(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, nil, nil)
	if err := b.Ping(context.Background(), "HmIP-RF"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error propagation
// ---------------------------------------------------------------------------

func TestCcuBackendXMLRPCErrorPropagates(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("xmlrpc: server fault 42")
	x := &fakeCaller{err: sentinel}
	b := NewCcuBackend(x, nil, nil)
	ctx := context.Background()

	if _, err := b.ListDevices(ctx); !errors.Is(err, sentinel) {
		t.Errorf("ListDevices: want sentinel, got %v", err)
	}
	if _, err := b.GetParamset(ctx, "0001ABCD:1", hmenum.ParamsetKeyValues); !errors.Is(err, sentinel) {
		t.Errorf("GetParamset: want sentinel, got %v", err)
	}
	if err := b.SetValue(ctx, "0001ABCD:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset); !errors.Is(err, sentinel) {
		t.Errorf("SetValue: want sentinel, got %v", err)
	}
	if err := b.PutParamset(ctx, "0001ABCD:1", hmenum.ParamsetKeyValues, map[string]any{}, hmenum.CommandRxModeUnset); !errors.Is(err, sentinel) {
		t.Errorf("PutParamset: want sentinel, got %v", err)
	}
	if _, err := b.GetLinks(ctx, "0001ABCD:1"); !errors.Is(err, sentinel) {
		t.Errorf("GetLinks: want sentinel, got %v", err)
	}
	if _, err := b.GetLinkPeers(ctx, "0001ABCD:1"); !errors.Is(err, sentinel) {
		t.Errorf("GetLinkPeers: want sentinel, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

func TestCcuBackendCapabilitiesIncludeRPCCallback(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	caps := b.Capabilities()
	if !caps.RPCCallback {
		t.Error("RPCCallback must be true for KindCCU")
	}
	if !caps.PingPong {
		t.Error("PingPong must be true for KindCCU")
	}
	if !caps.FirmwareUpdate {
		t.Error("FirmwareUpdate must be true for KindCCU")
	}
	if !caps.ListDevices {
		t.Error("ListDevices must be true for KindCCU")
	}
}

func TestCcuBackendKindIsKindCCU(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	if b.Kind() != KindCCU {
		t.Fatalf("Kind=%s, want ccu", b.Kind())
	}
}

// ---------------------------------------------------------------------------
// Operations interface compliance
// ---------------------------------------------------------------------------

func TestCcuBackendOperationsCompliance(t *testing.T) {
	var _ Operations = (*CcuBackend)(nil)
}

// ---------------------------------------------------------------------------
// DownloadFirmware
// ---------------------------------------------------------------------------

// TestCcuBackendDownloadFirmware verifies that DownloadFirmware posts the
// correct form fields to the CCU's maintenance CGI when a base URL and
// session-ID provider are wired.
func TestCcuBackendDownloadFirmware(t *testing.T) {
	t.Parallel()
	const wantSID = "session-xyz"
	const wantURL = "http://firmware.example.com/update.tar.gz"

	var gotSID, gotAction, gotURL string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotSID = r.FormValue("sid")
		gotAction = r.FormValue("action")
		gotURL = r.FormValue("url")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	b.SetDownloadFirmwareTransport(srv.URL, srv.Client(), func() string { return wantSID })

	if err := b.DownloadFirmware(context.Background(), wantURL); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method=%s, want POST", gotMethod)
	}
	if gotSID != wantSID {
		t.Errorf("sid=%q, want %q", gotSID, wantSID)
	}
	if gotAction != "download_firmware" {
		t.Errorf("action=%q, want download_firmware", gotAction)
	}
	if gotURL != wantURL {
		t.Errorf("url=%q, want %q", gotURL, wantURL)
	}
}

// TestCcuBackendDownloadFirmwareRequiresHTTPS verifies that non-http/https
// schemes are rejected before any network call is made.
func TestCcuBackendDownloadFirmwareRequiresHTTPS(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	b.SetDownloadFirmwareTransport("http://ccu", http.DefaultClient, func() string { return "sid" })

	err := b.DownloadFirmware(context.Background(), "ftp://badscheme.example.com/fw.tar")
	if err == nil {
		t.Fatal("expected error for ftp:// scheme, got nil")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("error=%v, want to wrap ErrUnsupported", err)
	}
}

// TestCcuBackendDownloadFirmwareNoTransport verifies that DownloadFirmware
// returns ErrUnsupported when SetDownloadFirmwareTransport has not been called.
func TestCcuBackendDownloadFirmwareNoTransport(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	err := b.DownloadFirmware(context.Background(), "http://example.com/fw.tar")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("without transport, want ErrUnsupported, got %v", err)
	}
}

// TestCcuBackendDownloadFirmwareCCUError verifies that a non-200 HTTP response
// from the CCU is surfaced as an error.
func TestCcuBackendDownloadFirmwareCCUError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	b.SetDownloadFirmwareTransport(srv.URL, srv.Client(), func() string { return "s" })

	err := b.DownloadFirmware(context.Background(), "https://example.com/fw.tar")
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}
