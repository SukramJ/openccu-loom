// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// ccu_wiring_paramset_consistency_test.go drives the HmIP stale-descriptor
// sweep the way a boot does: a real wireInterface against a CCU that reports
// a MASTER parameter in its description and omits it from the live paramset.
//
// The sweep exists to detect exactly that state — the HmIPServer keeps stale
// descriptor files after a firmware update — and it spent its whole life
// reporting a clean bill of health, because it looked the channels up under
// the bare interface name while every registry is keyed by the canonical
// `<central>-<interface>` wire id. Every lookup missed, so it compared
// nothing and found nothing.
//
// A source-level pin cannot catch that: the call is there either way, and
// which identifier it carries is the entire defect. So the fixture is a CCU
// whose description and values disagree, and the assertion is the warning an
// operator would see. Hand the wrong identifier to the check and the log
// stays empty.
//
// The scenario is a warm boot, because that is the only boot in which the
// sweep has anything to look at: the device-description registry it resolves
// a device's channels through is filled by the descriptor cache
// ([WireDescriptorPersistence]) or by a later newDevices callback — never by
// the ingest the sweep runs behind. The persisted rows here are written
// through the same registry-plus-sink path a previous run writes them
// through, so the identifier they are keyed by is production's, not the
// test's.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The fixture fleet: one HmIP device with one channel whose MASTER
// description carries a parameter the CCU no longer serves.
const (
	staleDeviceAddress  = "HMIP0STALE"
	staleChannelAddress = "HMIP0STALE:1"
	staleParameter      = "CHANNEL_OPERATION_MODE"
)

// staleParamsetCCU answers the XML-RPC calls one interface bring-up makes,
// plus the checkrega.cgi readiness probe. Its MASTER paramset description
// declares [staleParameter]; its MASTER paramset values are empty, which is
// what an HmIPServer with stale descriptor files looks like from the outside.
type staleParamsetCCU struct {
	srv *httptest.Server

	mu               sync.Mutex
	masterValueReads []string
}

func newStaleParamsetCCU(t *testing.T) *staleParamsetCCU {
	t.Helper()
	f := &staleParamsetCCU{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *staleParamsetCCU) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == checkRegaPath {
		_, _ = w.Write([]byte(checkRegaReadyBody))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	call, err := xmlrpc.DecodeCall(bytes.NewReader(body))
	w.Header().Set("Content-Type", "text/xml")
	if err != nil {
		f.respond(w, xmlrpc.StringValue(""))
		return
	}
	switch call.Method {
	case "listDevices":
		f.respond(w, xmlrpc.ArrayValue{
			xmlrpc.StructValue{Members: []xmlrpc.Member{
				{Name: "ADDRESS", Value: xmlrpc.StringValue(staleDeviceAddress)},
				{Name: "TYPE", Value: xmlrpc.StringValue("HmIP-BSM")},
				{Name: "PARAMSETS", Value: xmlrpc.ArrayValue{xmlrpc.StringValue("MASTER")}},
				{Name: "CHILDREN", Value: xmlrpc.ArrayValue{xmlrpc.StringValue(staleChannelAddress)}},
			}},
			xmlrpc.StructValue{Members: []xmlrpc.Member{
				{Name: "ADDRESS", Value: xmlrpc.StringValue(staleChannelAddress)},
				{Name: "TYPE", Value: xmlrpc.StringValue("SWITCH_VIRTUAL_RECEIVER")},
				{Name: "PARENT", Value: xmlrpc.StringValue(staleDeviceAddress)},
				{Name: "PARAMSETS", Value: xmlrpc.ArrayValue{
					xmlrpc.StringValue("MASTER"), xmlrpc.StringValue("VALUES"),
				}},
			}},
		})
	case "getParamsetDescription":
		address, key := callArgs(call)
		if address == staleChannelAddress && key == string(hmenum.ParamsetKeyMaster) {
			f.respond(w, xmlrpc.StructValue{Members: []xmlrpc.Member{
				{Name: staleParameter, Value: xmlrpc.StructValue{Members: []xmlrpc.Member{
					{Name: "TYPE", Value: xmlrpc.StringValue("INTEGER")},
					{Name: "OPERATIONS", Value: xmlrpc.IntValue(3)},
					{Name: "FLAGS", Value: xmlrpc.IntValue(1)},
					{Name: "MIN", Value: xmlrpc.IntValue(0)},
					{Name: "MAX", Value: xmlrpc.IntValue(3)},
					{Name: "DEFAULT", Value: xmlrpc.IntValue(0)},
				}}},
			}})
			return
		}
		// Anything else — the device-level MASTER probe above all — answers
		// the way a CCU answers for a paramset that does not exist.
		f.fault(w, -3, "Unknown paramset")
	case "getParamset":
		address, key := callArgs(call)
		if key == string(hmenum.ParamsetKeyMaster) {
			f.mu.Lock()
			f.masterValueReads = append(f.masterValueReads, address)
			f.mu.Unlock()
		}
		// The stale half of the fixture: the CCU serves no value for the
		// parameter its own description still advertises.
		f.respond(w, xmlrpc.StructValue{})
	default:
		f.respond(w, xmlrpc.StringValue(""))
	}
}

func (f *staleParamsetCCU) respond(w http.ResponseWriter, v xmlrpc.Value) {
	_ = xmlrpc.EncodeResponse(w, &xmlrpc.MethodResponse{Params: []xmlrpc.Value{v}})
}

func (f *staleParamsetCCU) fault(w http.ResponseWriter, code int, message string) {
	_ = xmlrpc.EncodeResponse(w, &xmlrpc.MethodResponse{
		Fault: &hmerr.XMLRPCFault{Code: code, Message: message},
	})
}

// readsOfMasterValues returns the channel addresses the daemon asked for the
// live MASTER paramset of.
func (f *staleParamsetCCU) readsOfMasterValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.masterValueReads...)
}

// callArgs pulls the (address, paramsetKey) pair both paramset calls carry.
func callArgs(call *xmlrpc.MethodCall) (address, key string) {
	if len(call.Params) > 0 {
		if s, ok := call.Params[0].(xmlrpc.StringValue); ok {
			address = string(s)
		}
	}
	if len(call.Params) > 1 {
		if s, ok := call.Params[1].(xmlrpc.StringValue); ok {
			key = string(s)
		}
	}
	return address, key
}

// syncBuffer is a writer several goroutines can log into. The consistency
// sweep runs on its own goroutine while the wiring path is still logging.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// staleFleetDescriptions is the inventory both halves of the fixture agree
// on: what the CCU answers listDevices with, and what a previous run
// persisted.
func staleFleetDescriptions() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{
			Address:   staleDeviceAddress,
			Type:      "HmIP-BSM",
			Children:  []string{staleChannelAddress},
			Paramsets: []string{"MASTER"},
		},
		{
			Address:   staleChannelAddress,
			Type:      "SWITCH_VIRTUAL_RECEIVER",
			Parent:    staleDeviceAddress,
			Paramsets: []string{"MASTER", "VALUES"},
		},
	}
}

// descriptorStoresWithStaleFleet returns descriptor stores holding the fleet
// as a previous run of this central left it.
//
// The rows are written through a throwaway central's registry rather than
// straight into SQLite, because the sink that mirrors a registry Put into the
// table is what decides which interface id the row carries — and that id is
// the one the warm boot hydrates back and the sweep then looks up by.
func descriptorStoresWithStaleFleet(t *testing.T, centralName string, wireID hmtypes.WireInterfaceID) DescriptorStores {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, sqlite.FileDSN(filepath.Join(t.TempDir(), "descriptors.db")))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stores := DescriptorStores{Devices: sqlite.NewDeviceStore(db), Paramsets: sqlite.NewParamsetStore(db)}

	previousRun, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	t.Cleanup(previousRun.Stop)
	WireDescriptorPersistence(ctx, previousRun, stores, nil)
	descs := staleFleetDescriptions()
	for i := range descs {
		previousRun.DescRegistry.Put(wireID, descs[i])
	}
	return stores
}

// TestWireInterfaceReportsStaleHmIPParamsetDescriptors is the behavioural pin
// for the sweep: bring one HmIP-RF interface up against a CCU whose MASTER
// description and MASTER values disagree, and require the mismatch to be
// reported.
//
// It goes through wireInterface — the same function the daemon's per-central
// bring-up calls — so the registries are populated by the real hydration
// pipeline and keyed the way production keys them. That is the whole point:
// the defect was an identifier mismatch between the pipeline that writes
// those registries and the sweep that reads them, and only a test that lets
// both halves run can see it.
func TestWireInterfaceReportsStaleHmIPParamsetDescriptors(t *testing.T) {
	t.Parallel()

	ccu := newStaleParamsetCCU(t)
	host, port := hostPortOf(t, ccu.srv.URL)

	cc := config.CentralConfig{
		// A named central is load-bearing: the wire id only differs from the
		// bare interface name when there is a central name to prefix, so a
		// test on an unnamed central would agree with the defect.
		Name:        "ccu-stale-paramsets",
		Host:        host,
		Port:        port,
		JSONRPCPort: port,
	}

	wireID := hmtypes.NewWireInterfaceID(cc.Name, hmenum.InterfaceHmIPRF)
	stores := descriptorStoresWithStaleFleet(t, cc.Name, wireID)

	unit, err := central.New(central.Config{Name: cc.Name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	t.Cleanup(unit.Stop)

	// The warm boot: the descriptions a previous run persisted come back into
	// the registry under the wire id the rows carry.
	if devices, _ := WireDescriptorPersistence(context.Background(), unit, stores, nil); devices != 2 {
		t.Fatalf("hydrated %d device descriptions, want 2 (the device and its channel)", devices)
	}

	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closer, _, err := wireInterface(
		ctx, cc, hmenum.InterfaceHmIPRF, unit, NewDevicePipeline(unit), client.NewValueWriter(),
		nil, // runner: the ReGa value seed needs a second (JSON-RPC) surface and no MASTER value rides it
		"",  // callbackURL: no push registration; the sweep runs off the ingest, not off a callback
		config.ReliabilityConfig{},
		nil, // masterValues: HmIP-RF is gated to a nil MasterPoller
		newBackendRegistry(),
		nil,     // jsonCaller: every call this scenario makes is XML-RPC
		nil, "", // BIN-RPC callback server/addr: CUxD only
		logger,
	)
	if closer != nil {
		t.Cleanup(closer)
	}
	if err != nil {
		t.Fatalf("wireInterface: %v", err)
	}

	// The sweep is scheduled on its own goroutine so bring-up does not wait
	// for it, so the assertion polls rather than assuming it has run.
	rec := awaitLogRecord(t, logs, "wire.paramset_inconsistency")

	if got := rec["device"]; got != staleDeviceAddress {
		t.Errorf("device = %v, want %q", got, staleDeviceAddress)
	}
	if got := rec["interface"]; got != wireID.String() {
		t.Errorf("interface = %v, want the canonical wire id %q", got, wireID.String())
	}
	if got, ok := rec["missing"].(float64); !ok || got != 1 {
		t.Errorf("missing = %v, want 1 (the parameter the description declares and the CCU omits)", got)
	}

	// The sweep has to have asked the CCU for the channel's live MASTER
	// values; with the channels resolved under the wrong key it would report
	// nothing simply because it looked at nothing.
	var sawChannel bool
	for _, addr := range ccu.readsOfMasterValues() {
		if addr == staleChannelAddress {
			sawChannel = true
			break
		}
	}
	if !sawChannel {
		t.Errorf("the CCU was never asked for the MASTER values of %q; the sweep compared nothing",
			staleChannelAddress)
	}
}

// awaitLogRecord waits for one JSON log record carrying msg and returns it
// decoded.
func awaitLogRecord(t *testing.T, logs *syncBuffer, msg string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		for line := range strings.SplitSeq(logs.String(), "\n") {
			if !strings.Contains(line, msg) {
				continue
			}
			rec := map[string]any{}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec["msg"] == msg {
				return rec
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q log record within the deadline — the stale MASTER descriptor was not "+
				"reported, so the sweep either never ran or resolved the device's channels under an "+
				"identifier the registries are not keyed by", msg)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// hostPortOf splits a test server URL into the pieces CentralConfig wants.
func hostPortOf(t *testing.T, raw string) (host string, port int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	h, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}
	return h, p
}
