// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// Addresses from the default godevccu fleet.
const (
	switchChannel = "VCU0000321:1" // HM-LC-Sw1-Pl on BidCos-RF, STATE
	bidcosRF      = "BidCos-RF"
	hmipRF        = "HmIP-RF"
)

// exactInitRefusal is the fault string clients match on, spelled out
// here independently of the fake's own constant.
const exactInitRefusal = "init is not available remotely on openccu-lite: " +
	"subscribe to /api/rpc/v1/events - see docs/rpc-remote.md"

func startFake(t *testing.T, opts litefake.Options) *litefake.Fake {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, err := litefake.Start(ctx, opts)
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// reply is the status line and headers of a finished request; the body
// is read and closed by the helper that made it.
type reply struct {
	StatusCode int
	Header     http.Header
}

// get performs a GET with an optional bearer token.
func get(t *testing.T, f *litefake.Fake, path, token string) (resp reply, body []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.URL()+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = r.Body.Close() }()
	body, err = io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{StatusCode: r.StatusCode, Header: r.Header}, body
}

// errorCode decodes the "error" member of a JSON error answer.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("not a JSON error body: %q: %v", body, err)
	}
	return e.Error
}

// rpcClient is Loom's own XML-RPC client pointed at the proxy, with the
// token as the Basic-auth password.
func rpcClient(t *testing.T, f *litefake.Fake, iface, token string) *xmlrpc.Client {
	t.Helper()
	c, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:      f.URL() + "/api/rpc/v1/xmlrpc/" + iface,
		Username: "loom",
		Password: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// rawRPC posts one XML-RPC call over plain HTTP with a bearer token.
func rawRPC(t *testing.T, f *litefake.Fake, iface, token, method string, params ...xmlrpc.Value) (resp reply, raw []byte) {
	t.Helper()
	var body bytes.Buffer
	if err := xmlrpc.EncodeCall(&body, &xmlrpc.MethodCall{Method: method, Params: params}); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		f.URL()+"/api/rpc/v1/xmlrpc/"+iface, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/xml")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST xmlrpc: %v", err)
	}
	defer func() { _ = r.Body.Close() }()
	raw, err = io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{StatusCode: r.StatusCode, Header: r.Header}, raw
}

// multicall builds the parameter of a system.multicall.
func multicall(calls ...xmlrpc.MethodCall) xmlrpc.Value {
	arr := make(xmlrpc.ArrayValue, 0, len(calls))
	for _, c := range calls {
		params := xmlrpc.ArrayValue(c.Params)
		if params == nil {
			params = xmlrpc.ArrayValue{}
		}
		arr = append(arr, xmlrpc.StructValue{Members: []xmlrpc.Member{
			{Name: "methodName", Value: xmlrpc.StringValue(c.Method)},
			{Name: "params", Value: params},
		}})
	}
	return arr
}

// asFault extracts the XML-RPC fault from a client error.
func asFault(t *testing.T, err error) *hmerr.XMLRPCFault {
	t.Helper()
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want an XML-RPC fault, got %v", err)
	}
	return fault
}

// frame is one parsed event-stream block: a comment or a message.
type frame struct {
	comment string
	id      string
	hasID   bool
	event   string
	data    string
}

// sseStream reads frames from an open event stream in the background.
type sseStream struct {
	body   io.ReadCloser
	frames chan frame
	cancel context.CancelFunc
}

// openStream opens /api/rpc/v1/events. A non-200 answer is returned
// with a nil stream.
func openStream(t *testing.T, f *litefake.Fake, token, query, lastEventID string) (*sseStream, reply) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	url := f.URL() + "/api/rpc/v1/events"
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // an open stream's body is closed by sseStream.close
	if err != nil {
		cancel()
		t.Fatalf("GET events: %v", err)
	}
	rp := reply{StatusCode: resp.StatusCode, Header: resp.Header}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		cancel()
		return nil, rp
	}
	s := &sseStream{body: resp.Body, frames: make(chan frame, 256), cancel: cancel}
	go s.read()
	t.Cleanup(s.close)
	return s, rp
}

// read parses frames until the body ends; the channel then closes.
func (s *sseStream) read() {
	defer close(s.frames)
	sc := bufio.NewScanner(s.body)
	var cur frame
	dirty := false
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if dirty {
				s.frames <- cur
			}
			cur, dirty = frame{}, false
			continue
		}
		dirty = true
		switch {
		case strings.HasPrefix(line, ":"):
			cur.comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
		case strings.HasPrefix(line, "id: "):
			cur.id, cur.hasID = strings.TrimPrefix(line, "id: "), true
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (s *sseStream) close() {
	s.cancel()
	_ = s.body.Close()
}

// next returns the next frame, failing after timeout. ok is false when
// the stream ended.
func (s *sseStream) next(t *testing.T, timeout time.Duration) (frame, bool) {
	t.Helper()
	select {
	case fr, ok := <-s.frames:
		return fr, ok
	case <-time.After(timeout):
		t.Fatalf("no frame within %s", timeout)
		return frame{}, false
	}
}

// nextMessage skips comments and returns the next message frame.
func (s *sseStream) nextMessage(t *testing.T, timeout time.Duration) frame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		fr, ok := s.next(t, time.Until(deadline))
		if !ok {
			t.Fatal("stream ended while waiting for a message")
		}
		if fr.event != "" {
			return fr
		}
	}
}

// expectEnd waits for the stream to end, skipping frames on the way;
// it returns the frames seen.
func (s *sseStream) expectEnd(t *testing.T, timeout time.Duration) []frame {
	t.Helper()
	var seen []frame
	deadline := time.After(timeout)
	for {
		select {
		case fr, ok := <-s.frames:
			if !ok {
				return seen
			}
			seen = append(seen, fr)
		case <-deadline:
			t.Fatalf("stream did not end within %s; saw %+v", timeout, seen)
			return seen
		}
	}
}

// eventPayload is the data of an event message.
type eventPayload struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	TS        string          `json:"ts"`
	Batch     uint64          `json:"batch"`
	Confirmed bool            `json:"confirmed"`
}

func decode[T any](t *testing.T, data string) T { // T: the payload type the test expects.
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

// fireSwitch makes the BidCos-RF switch report STATE through its own
// interface process, the path a device-originated value takes.
func fireSwitch(t *testing.T, f *litefake.Fake, on bool) {
	t.Helper()
	rpc := f.V().InterfaceRPC(bidcosRF)
	if rpc == nil {
		t.Fatal("no BidCos-RF interface process")
	}
	if err := rpc.SimulateDeviceEvent(switchChannel, "STATE", on); err != nil {
		t.Fatalf("SimulateDeviceEvent: %v", err)
	}
}

// TestFakeRefusesInitIncludingInsideMulticall pins the init refusal: a
// fault -1 with the exact text over HTTP 200, alone and as one inner
// call of a multicall. getVersion through the same path succeeds, so
// the refusal is specific to init.
func TestFakeRefusesInitIncludingInsideMulticall(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := rpcClient(t, f, hmipRF, litefake.DefaultToken)
	ctx := context.Background()

	if _, err := c.Call(ctx, "getVersion", nil); err != nil {
		t.Fatalf("getVersion through the proxy: %v", err)
	}

	_, err := c.Call(ctx, "init", []xmlrpc.Value{xmlrpc.StringValue("http://127.0.0.1:1/"), xmlrpc.StringValue("loom")})
	fault := asFault(t, err)
	if fault.Code != -1 || fault.Message != exactInitRefusal {
		t.Errorf("init alone: fault %d %q, want -1 %q", fault.Code, fault.Message, exactInitRefusal)
	}

	_, err = c.Call(ctx, "system.multicall", []xmlrpc.Value{multicall(
		xmlrpc.MethodCall{Method: "getVersion"},
		xmlrpc.MethodCall{Method: "init", Params: []xmlrpc.Value{xmlrpc.StringValue("http://127.0.0.1:1/"), xmlrpc.StringValue("x")}},
	)})
	fault = asFault(t, err)
	if fault.Code != -1 || fault.Message != exactInitRefusal {
		t.Errorf("init in multicall: fault %d %q, want -1 %q", fault.Code, fault.Message, exactInitRefusal)
	}

	resp, body := rawRPC(t, f, hmipRF, litefake.DefaultToken, "init", xmlrpc.StringValue("http://x/"), xmlrpc.StringValue("y"))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("init refusal status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/xml; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	if !bytes.HasPrefix(body, []byte(`<?xml version="1.0" encoding="UTF-8"?>`)) {
		t.Errorf("answer not declared UTF-8: %.80q", body)
	}
}

// TestFakeAnswersStartingWhileNotReady pins the not-ready box: every
// /api/* path answers 503 starting with Retry-After 5, and after
// SetReady(true) the same request is served.
func TestFakeAnswersStartingWhileNotReady(t *testing.T) {
	f := startFake(t, litefake.Options{StartNotReady: true})

	for _, path := range []string{"/api/meta/v1/version", "/api/rpc/v1/interfaces", "/api/nope"} {
		resp, body := get(t, f, path, litefake.DefaultToken)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Retry-After"); got != "5" {
			t.Errorf("%s: Retry-After %q, want 5", path, got)
		}
		if code := errorCode(t, body); code != "starting" {
			t.Errorf("%s: error %q, want starting", path, code)
		}
		if !strings.Contains(string(body), "occulited is not answering yet") {
			t.Errorf("%s: body %s", path, body)
		}
	}
	if resp, _ := get(t, f, "/ise/checkrega.cgi", ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("non-API path while not ready: %d, want 503", resp.StatusCode)
	}

	f.SetReady(true)
	resp, body := get(t, f, "/api/meta/v1/version", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("version after SetReady: %d %s", resp.StatusCode, body)
	}
	v := decode[struct {
		API          string `json:"api"`
		Capabilities struct {
			APIs map[string]int `json:"apis"`
		} `json:"capabilities"`
	}](t, string(body))
	if v.API != "meta" || v.Capabilities.APIs["rpc"] != 1 {
		t.Errorf("version document %s", body)
	}
}

// TestFakeServesHTMLShellForNonAPIPaths pins the catch-all: a CCU probe
// path gets 200 text/html (never "OK"), a static-extension miss 404,
// an unknown /api/ path a JSON 404.
func TestFakeServesHTMLShellForNonAPIPaths(t *testing.T) {
	f := startFake(t, litefake.Options{})
	for _, path := range []string{"/ise/checkrega.cgi", "/VERSION", "/some/route"} {
		resp, body := get(t, f, path, "")
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if strings.TrimSpace(string(body)) == "OK" {
			t.Errorf("%s answered OK like a CCU", path)
		}
	}
	if resp, _ := get(t, f, "/missing.js", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("static miss: %d, want 404", resp.StatusCode)
	}
	resp, body := get(t, f, "/api/unknown/v1/x", litefake.DefaultToken)
	if resp.StatusCode != http.StatusNotFound || errorCode(t, body) != "not-found" {
		t.Errorf("unknown api path: %d %s", resp.StatusCode, body)
	}
}

// TestFakeServesHealthAndUPnP pins the two open identity endpoints.
func TestFakeServesHealthAndUPnP(t *testing.T) {
	f := startFake(t, litefake.Options{Serial: "SGTIN0042", Hostname: "lite-box"})

	resp, body := get(t, f, "/api/system/v1/health", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	h := decode[map[string]json.RawMessage](t, string(body))
	for _, k := range []string{"ok", "version", "release", "base", "uptime_s", "meta"} {
		if _, ok := h[k]; !ok {
			t.Errorf("health lacks %q: %s", k, body)
		}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("health Content-Type %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("health Cache-Control %q", cc)
	}

	resp, body = get(t, f, "/upnp/basic_dev.cgi", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upnp: %d", resp.StatusCode)
	}
	for _, want := range []string{
		"<manufacturer>openccu-lite</manufacturer>", "<modelName>openccu-lite</modelName>",
		"<serialNumber>SGTIN0042</serialNumber>", "<UDN>uuid:upnp-BasicDevice-1_0-SGTIN0042</UDN>",
		"<friendlyName>lite-box</friendlyName>", "<modelDescription>openccu-lite SGTIN0042</modelDescription>",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("upnp lacks %s:\n%s", want, body)
		}
	}
}

// TestFakeRecordsAPICalls pins the call log: path, status, subject and
// the XML-RPC methods of a proxy request.
func TestFakeRecordsAPICalls(t *testing.T) {
	f := startFake(t, litefake.Options{})
	get(t, f, "/api/system/v1/health", "")
	rawRPC(t, f, hmipRF, litefake.DefaultToken, "system.multicall", multicall(
		xmlrpc.MethodCall{Method: "getVersion"}, xmlrpc.MethodCall{Method: "ping", Params: []xmlrpc.Value{xmlrpc.StringValue("x")}},
	))
	get(t, f, "/ise/checkrega.cgi", "")

	calls := f.Calls()
	if len(calls) != 2 {
		t.Fatalf("recorded %d calls, want 2 (non-API paths are not recorded): %+v", len(calls), calls)
	}
	if calls[0].Path != "/api/system/v1/health" || calls[0].Status != http.StatusOK {
		t.Errorf("first call %+v", calls[0])
	}
	rpc := calls[1]
	if rpc.Subject != "token:"+litefake.TokenName(litefake.DefaultToken) {
		t.Errorf("subject %q", rpc.Subject)
	}
	if strings.Join(rpc.RPCMethods, ",") != "getVersion,ping" {
		t.Errorf("rpc methods %v", rpc.RPCMethods)
	}
}
