// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

// Tests for the two operator-facing views of the Matter session table:
// the occupancy the REST diagnostics surface reports, and the debug
// record the CASE resume path leaves behind.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/go-fabric/secure/operational"
	"github.com/SukramJ/go-fabric/secure/sigma"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// TestMatterSessionLister_ReportsOccupancyFromTheLiveManager pins that
// the occupancy the diagnostics endpoint serves is read from the same
// manager that hands out session ids — not recomputed from the session
// list, which cannot see a staked id at all.
//
// A handshake that announced a session id in Sigma2 and never completed
// holds its slot for twenty minutes. That is the shape that drains the
// 16-bit space, and it is invisible in every other view.
func TestMatterSessionLister_ReportsOccupancyFromTheLiveManager(t *testing.T) {
	mgr := buildTestOperationalManager(t)
	lister := matterSessionLister{op: mgr}

	staked, err := mgr.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID: %v", err)
	}
	var keys sigma.SessionKeys
	for i := range keys.I2RKey {
		keys.I2RKey[i] = byte(i + 1)
		keys.R2IKey[i] = byte(i + 17)
	}
	if _, err := mgr.OpenFromSigmaWithID(staked, 1, 0xB0B, 0xA11CE, 0x1111, nil, keys); err != nil {
		t.Fatalf("OpenFromSigmaWithID: %v", err)
	}
	if _, err := mgr.AllocateID(); err != nil {
		t.Fatalf("AllocateID (abandoned handshake): %v", err)
	}

	occ := lister.MatterSessionOccupancy()
	if occ.Live != 1 {
		t.Errorf("live = %d, want 1", occ.Live)
	}
	if occ.Reserved != 1 {
		t.Errorf("reserved = %d, want 1 — the abandoned handshake still owns its id", occ.Reserved)
	}
	if occ.Capacity != operational.SessionIDSpace {
		t.Errorf("capacity = %d, want %d", occ.Capacity, operational.SessionIDSpace)
	}
	if occ.Free != operational.SessionIDSpace-2 {
		t.Errorf("free = %d, want %d", occ.Free, operational.SessionIDSpace-2)
	}
	if len(lister.MatterSessions()) != 1 {
		t.Errorf("the session list must still report only the established session")
	}
}

// TestMatterSessionLister_WithoutManagerReportsNothing pins the
// bridge-disabled path: the adapter is built before the bridge exists in
// some configurations, and a zero occupancy must not read as a full
// table.
func TestMatterSessionLister_WithoutManagerReportsNothing(t *testing.T) {
	t.Parallel()
	occ := matterSessionLister{}.MatterSessionOccupancy()
	if occ != (handlers.MatterSessionOccupancy{}) {
		t.Errorf("occupancy without a manager = %+v, want the zero value", occ)
	}
}

// TestLogCaseResume_RecordsTheIDsTheResumeReusedOrRenewed pins the
// content of the resume record.
//
// Whether a resumed session must carry a new session id cannot be
// settled without a live controller, so what ships is the ability to
// answer it from an operator report: the resumption id the controller
// presented, the session id before and after, and the sessions the
// install displaced. Dropping any of them turns the report back into a
// guess.
func TestLogCaseResume_RecordsTheIDsTheResumeReusedOrRenewed(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logCaseResume(logger, sigma.ResumeInfo{
		Resumed:               true,
		PresentedResumptionID: []byte{0xAA, 0xBB},
		IssuedResumptionID:    []byte{0xCC, 0xDD},
		SessionIDBefore:       0x2001,
		SessionIDAfter:        0x2001,
	}, 1, 0xA11CE, []uint16{0x2001}, operational.SessionTableOccupancy{Live: 2, Reserved: 1, Capacity: 65534})

	out := buf.String()
	for _, want := range []string{
		"matter.bridge.case.session_resumed",
		"presented_resumption_id=aabb",
		"issued_resumption_id=ccdd",
		"session_id_before=8193",
		"session_id_after=8193",
		"session_id_renewed=false",
		"displaced_session_ids=8193",
		"displaced_session_count=1",
		"peer_node_id=659918",
		"sessions_live=2",
		"sessions_reserved=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("resume record is missing %q\ngot: %s", want, out)
		}
	}
	if !strings.Contains(out, "level=DEBUG") {
		t.Errorf("the resume record must stay at debug level — it fires once per resume on the wire path\ngot: %s", out)
	}
}

// TestLogCaseResume_SilentForAFullHandshake pins that the record
// describes resumes only. A full Sigma3 handshake already logs its own
// establishment; emitting a resume line there would tell an operator the
// controller resumed a cached session when it ran the whole handshake.
func TestLogCaseResume_SilentForAFullHandshake(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logCaseResume(logger, sigma.ResumeInfo{}, 1, 0xA11CE, nil, operational.SessionTableOccupancy{})

	if buf.Len() != 0 {
		t.Errorf("a full handshake emitted a resume record: %s", buf.String())
	}
}
