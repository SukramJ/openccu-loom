// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"thingfabric.dev/go-fabric-ui/fabricui"
	mattercore "thingfabric.dev/go-fabric/cluster/core"
	"thingfabric.dev/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// pagesNode is the node the node pages reach now: the runtime hands them a
// fabricui.NodeSwitch, whose target an erase replaces.
func pagesNode(opts fabricui.Options) (matterNodeSource, bool) {
	sw, ok := opts.Node.(*fabricui.NodeSwitch)
	if !ok {
		return matterNodeSource{}, false
	}
	n, ok := sw.Current().(matterNodeSource)
	return n, ok && n.Node != nil
}

// basicInformationAttrNodeLabel is the BasicInformation NodeLabel attribute
// (Matter Core §11.1.6.3).
const basicInformationAttrNodeLabel uint32 = 0x0005

// eraseRuntime boots the Matter runtime through wireMatterRuntime with one
// exposable switch on the central, and returns its wiring, the node API
// over it, the hub and the database.
func eraseRuntime(t *testing.T, central string) (matterWiring, http.Handler, *ws.Hub, *gosql.DB) {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, central)
	unit, ok := reg.Get(central)
	if !ok {
		t.Fatal("the test registry did not hold the central it was built with")
	}
	unit.ModelRegistry.Put(switchDeviceWithUnnamedChannel("0001ERS01", "Leselampe", 3))

	hub := ws.NewHub()
	db := openTestLoomDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	mw, closers, teardown := wireMatterRuntime(
		ctx, cfg, reg, db, health.NewTracker(), nil, nil, nil,
		slog.New(slog.DiscardHandler), hub, nil,
	)
	// One cleanup, cancel first: see TestMatterBootEventsAreReadableAfterWiring.
	t.Cleanup(func() {
		cancel()
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})
	if _, ok := pagesNode(mw.nodeUI); !ok {
		t.Fatalf("the Matter runtime handed the node pages no node switch (%T); without it this test asserts nothing", mw.nodeUI.Node)
	}
	return mw, newNodeAPI(t, mw.nodeUI), hub, db
}

// setExposure enables or disables the first exposable source through the
// selection route, as the operator does on the page.
func setExposure(t *testing.T, api http.Handler, enabled bool) {
	t.Helper()
	rec := serveNodeAPIOn(t, api, http.MethodGet, "/api/v1/node/exposure", "")
	var list struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) == 0 {
		t.Fatalf("GET /node/exposure listed no candidate (err %v): %s", err, rec.Body.String())
	}
	body := fmt.Sprintf(`{"items":[{"key":%q,"enabled":%t}]}`, list.Items[0].Key, enabled)
	if rec := serveNodeAPIOn(t, api, http.MethodPut, "/api/v1/node/exposure", body); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("PUT /node/exposure = %d %s", rec.Code, rec.Body.String())
	}
}

// hubFrames counts the frames of topic the hub carried so far.
func hubFrames(hub *ws.Hub, topic string) int {
	return len(hub.Replay(0, func(tp string) bool { return tp == topic }).Events)
}

// eraseNode serves the page's erase and requires it to succeed.
func eraseNode(t *testing.T, api http.Handler) {
	t.Helper()
	if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/maintenance/erase", `{"confirm":"erase-node"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("POST /node/maintenance/erase = %d %s, want 204", rec.Code, rec.Body.String())
	}
}

// TestMatterEraseBringsAFreshNodeBackUnderThePages drives the node page's
// erase through wireMatterRuntime. go-fabric's node is single-use, so the
// erase has to bring a new node up — and everything the daemon hangs on the
// node has to move to it: the pages, the status reader, the endpoint hook
// and the node observer. The old node is the negative control throughout:
// every check would read it had the daemon kept it.
func TestMatterEraseBringsAFreshNodeBackUnderThePages(t *testing.T) {
	t.Parallel()
	mw, api, hub, _ := eraseRuntime(t, "ccu-erase")
	ctx := context.Background()

	var status struct {
		Erase      bool   `json:"erase"`
		BootReason *uint8 `json:"boot_reason"`
	}
	readStatus := func() {
		t.Helper()
		rec := serveNodeAPIOn(t, api, http.MethodGet, "/api/v1/node/status", "")
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatalf("GET /node/status: %v: %s", err, rec.Body.String())
		}
	}
	readStatus()
	if !status.Erase {
		t.Fatal("GET /node/status reports no erase: the runtime wires none, and the page offers none")
	}

	setExposure(t, api, true)
	old, _ := pagesNode(mw.nodeUI)
	devices := len(old.Topology().Devices)
	if devices == 0 {
		t.Fatal("the selected switch is not on the bridge before the erase; the endpoint check below would compare nothing")
	}
	if _, err := mw.nodeLabel.Apply(ctx, "Erase Probe"); err != nil {
		t.Fatalf("apply node label: %v", err)
	}
	oldID := nodeRootString(t, old, basicInformationAttrUniqueID)

	eraseNode(t, api)

	n, _ := pagesNode(mw.nodeUI)
	if n.Node == old.Node {
		t.Fatal("the pages still read the erased node: the erase brought no new node, or did not set the switch")
	}
	if got := mw.status.node(); got != n.Node {
		t.Fatal("the status reader (MCP, health probe) still reads the erased node")
	}
	if n.LocalAddr() == "" {
		t.Fatal("the new node does not listen")
	}
	if got := n.BootReason(); got != mattercore.BootReasonSoftwareReset {
		t.Fatalf("new node BootReason = %d, want SoftwareReset (%d)", got, mattercore.BootReasonSoftwareReset)
	}
	if id := nodeRootString(t, n, basicInformationAttrUniqueID); id == oldID {
		t.Fatalf("the new node serves the erased node's UniqueID %q; the erase did not reach the store", id)
	}
	if got := len(n.Topology().Devices); got != devices {
		t.Fatalf("the new node holds %d bridged devices, want the %d the operator selected before the erase", got, devices)
	}
	if label := nodeRootString(t, n, basicInformationAttrNodeLabel); label != "Erase Probe" {
		t.Fatalf("new node NodeLabel = %q, want the label the operator applied last", label)
	}
	if !n.Window().Open {
		t.Fatal("the new, uncommissioned node opened no window")
	}
	readStatus()
	if status.BootReason == nil || *status.BootReason != mattercore.BootReasonSoftwareReset {
		t.Fatalf("GET /node/status boot_reason = %v after the erase, want %d: the pages do not read the new node", status.BootReason, mattercore.BootReasonSoftwareReset)
	}

	// The node observer moved: the new node's window close reaches the hub.
	closed := hubFrames(hub, nodeTopicWindowClosed)
	if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window/close", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close the new node's window = %d %s", rec.Code, rec.Body.String())
	}
	if got := hubFrames(hub, nodeTopicWindowClosed); got <= closed {
		t.Fatalf("the new node's window close put no %s frame on the hub: the node observer stayed on the erased node", nodeTopicWindowClosed)
	}
	// The reassembly hook moved: the new bridge's reassembly reaches the hub.
	changed := hubFrames(hub, nodeTopicEndpointsChanged)
	setExposure(t, api, false)
	if got := hubFrames(hub, nodeTopicEndpointsChanged); got <= changed {
		t.Fatalf("a reassembly of the new node put no %s frame on the hub: the hook stayed on the erased bridge", nodeTopicEndpointsChanged)
	}
	if got := len(n.Topology().Devices); got != 0 {
		t.Fatalf("after the deselection the new node holds %d bridged devices, want 0: the sync does not reach its reconciler", got)
	}
}

// servedRootString reads a string attribute the node's root
// BasicInformation serves.
func nodeRootString(t *testing.T, n matterNodeSource, attr uint32) string {
	t.Helper()
	v, ok := n.Root().BasicInformation.MatterRead(attr)
	if !ok {
		t.Fatalf("root BasicInformation does not serve attribute 0x%04X", attr)
	}
	switch s := v.(type) {
	case tlv.BoundedString:
		return s.Value
	case string:
		return s
	}
	t.Fatalf("root BasicInformation attribute 0x%04X = %#v, want a string", attr, v)
	return ""
}

// TestMatterEraseLeavesAnUnfinishedScrubToTheBoot: go-fabric's erase ends
// with a scrub of the whole database file (VACUUM, then a WAL checkpoint),
// which a read transaction of the running daemon makes fail. The erase then
// still brings the new node up,
// the pages warn, and the next boot runs the scrub. The case without a
// reader is the negative control: it leaves neither flag nor warning.
func TestMatterEraseLeavesAnUnfinishedScrubToTheBoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		holdReader  bool
		wantPending bool
	}{
		{name: "scrub finishes", holdReader: false, wantPending: false},
		{name: "a read transaction blocks the scrub", holdReader: true, wantPending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mw, api, _, db := eraseRuntime(t, "ccu-scrub")
			ctx := context.Background()
			old, _ := pagesNode(mw.nodeUI)

			var release func()
			if tc.holdReader {
				conn, err := db.Conn(ctx)
				if err != nil {
					t.Fatalf("reader connection: %v", err)
				}
				tx, err := conn.BeginTx(ctx, &gosql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatalf("reader transaction: %v", err)
				}
				var one int
				if err := tx.QueryRowContext(ctx, `SELECT 1 FROM boot_record`).Scan(&one); err != nil {
					t.Fatalf("reader snapshot: %v", err)
				}
				release = func() { _ = tx.Rollback(); _ = conn.Close() }
				t.Cleanup(release)
			}

			eraseNode(t, api)

			if n, _ := pagesNode(mw.nodeUI); n.Node == old.Node {
				t.Fatal("the erase brought no new node")
			}
			rec, err := sqlite.LoadBootRecord(ctx, db)
			if err != nil {
				t.Fatalf("load boot record: %v", err)
			}
			if rec.ScrubPending != tc.wantPending {
				t.Fatalf("scrub pending = %v, want %v", rec.ScrubPending, tc.wantPending)
			}
			warned := false
			for _, w := range mw.nodeUI.Warnings(ctx) {
				warned = warned || w.Code == "scrub_pending"
			}
			if warned != tc.wantPending {
				t.Fatalf("scrub_pending warning on the pages = %v, want %v", warned, tc.wantPending)
			}
			if !tc.wantPending {
				return
			}
			release()
			runPendingScrub(db, slog.New(slog.DiscardHandler))
			if rec, _ := sqlite.LoadBootRecord(ctx, db); rec.ScrubPending {
				t.Fatal("the boot's scrub did not clear the flag")
			}
		})
	}
}

// TestBootRunsTheScrubAnEraseLeftPending pins the boot half through the
// boot path itself (wireAuditOverlay), not through runPendingScrub: a flag
// an erase left is gone after the next boot opened the database. The first
// boot, which reads the flag before it is set, is the negative control.
func TestBootRunsTheScrubAnEraseLeftPending(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	ctx := context.Background()
	boot := func() *auditOverlay {
		t.Helper()
		gooseMigrateMu.Lock()
		ov, teardown := wireAuditOverlay(ctx, wiring.NewManifest(), cfg, slog.New(slog.DiscardHandler))
		gooseMigrateMu.Unlock()
		t.Cleanup(teardown)
		if ov.db == nil {
			t.Fatal("the boot opened no database")
		}
		return ov
	}

	first := boot()
	if err := sqlite.MarkScrubPending(ctx, first.db); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if rec, _ := sqlite.LoadBootRecord(ctx, first.db); !rec.ScrubPending {
		t.Fatal("the flag does not read back before the next boot")
	}
	_ = first.db.Close()

	second := boot()
	if rec, err := sqlite.LoadBootRecord(ctx, second.db); err != nil || rec.ScrubPending {
		t.Fatalf("after the next boot: scrub pending = %v (err %v), want the boot to have run the scrub", rec.ScrubPending, err)
	}
}
