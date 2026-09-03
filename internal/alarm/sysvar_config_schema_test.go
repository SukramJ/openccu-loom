// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"testing"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The sysvar mirror and the output layer read one stored document,
// alarm_outputs.config_json, through one schema. This drives the
// mirror's own reader over a verbatim document — no Go struct in
// between — so a key that outputs.OutputConfig does not declare is
// measured as lost rather than assumed present.
func TestSysvarMirrorReadsDisarmAndExistingFlagsFromTheStoredDocument(t *testing.T) {
	h := newSysvarHarness(t)
	h.wireCentral("ccu1")
	h.seedZone("eg", "Erdgeschoss")

	const raw = `{"sysvar_name":"AlarmZoneEG","sysvar_allow_disarm":true,"sysvar_existing":true}`
	now := h.clk.Now().UnixMilli()
	if err := h.svc.Stores().Outputs.Upsert(h.ctx, sqlitestore.AlarmOutputRow{
		ID: "mirror1", ZoneID: "eg", Class: hmenum.AlarmOutputClassSysvarMirror,
		CentralName: "ccu1", Name: "mirror1", ConfigJSON: raw,
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed output: %v", err)
	}
	h.start()

	targets := h.svc.sysvarMirror.mirrorTargets(h.ctx, "eg")
	if len(targets) != 1 {
		t.Fatalf("mirrorTargets = %+v, want exactly one target", targets)
	}
	got := targets[0]
	if got.name != "AlarmZoneEG" {
		t.Errorf("target name = %q, want %q", got.name, "AlarmZoneEG")
	}
	if !got.allowDisarm {
		t.Errorf("target allowDisarm = false, want true (sysvar_allow_disarm was set in the stored document)")
	}
	if !got.existing {
		t.Errorf("target existing = false, want true (sysvar_existing was set in the stored document)")
	}
}
