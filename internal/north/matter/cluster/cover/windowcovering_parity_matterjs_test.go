// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/cover"
	clusterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// matterClusterEntry is a minimal schema entry used by the parity tests.
type matterClusterEntry struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
}

type matterSchemaEnvelope struct {
	Clusters []matterClusterEntry `json:"clusters"`
}

func loadSchema(t *testing.T) *matterSchemaEnvelope {
	t.Helper()

	var s matterSchemaEnvelope
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal matter-schema-snapshot.json: %v", err)
	}

	return &s
}

func clusterEntry(s *matterSchemaEnvelope, id uint32) (matterClusterEntry, bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c, true
		}
	}

	return matterClusterEntry{}, false
}

// TestParityMatterJS_WindowCoveringClusterRevision pins the WindowCovering
// cluster revision against matter.js HEAD.
func TestParityMatterJS_WindowCoveringClusterRevision(t *testing.T) {
	t.Parallel()

	schema := loadSchema(t)

	js, ok := clusterEntry(schema, clusterwire.WindowCoveringClusterID)
	if !ok {
		t.Fatalf("matter.js schema has no cluster 0x%04X (WindowCovering)", clusterwire.WindowCoveringClusterID)
	}

	if cover.ClusterRevision != js.Revision {
		t.Errorf("ClusterRevision = %d, want %d (matter.js HEAD for %s 0x%04X)",
			cover.ClusterRevision, js.Revision, js.Name, js.ID)
	}
}

// TestParityMatterJS_WindowCoveringAttrConstants verifies that the wire
// attribute-ID constants match the expected IDs from the Matter spec.
func TestParityMatterJS_WindowCoveringAttrConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		got      uint32
		expected uint32
	}{
		{"Type", clusterwire.WindowCoveringAttrType, 0x0000},
		{"ConfigStatus", clusterwire.WindowCoveringAttrConfigStatus, 0x0007},
		{"CurrentPositionLiftPercentage", clusterwire.WindowCoveringAttrCurrentPositionLiftPercentage, 0x0008},
		{"OperationalStatus", clusterwire.WindowCoveringAttrOperationalStatus, 0x000A},
		{"TargetPositionLiftPercent100ths", clusterwire.WindowCoveringAttrTargetPositionLiftPercent100ths, 0x000B},
		{"EndProductType", clusterwire.WindowCoveringAttrEndProductType, 0x000D},
		{"CurrentPositionLiftPercent100ths", clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths, 0x000E},
		{"Mode", clusterwire.WindowCoveringAttrMode, 0x0017},
		{"SafetyStatus", clusterwire.WindowCoveringAttrSafetyStatus, 0x001A},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if c.got != c.expected {
				t.Errorf("wire.WindowCoveringAttr%s = 0x%04X, want 0x%04X", c.name, c.got, c.expected)
			}
		})
	}
}

// TestParityMatterJS_WindowCoveringCmdConstants verifies that the wire
// command-ID constants are correct.
func TestParityMatterJS_WindowCoveringCmdConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		got      uint32
		expected uint32
	}{
		{"UpOrOpen", clusterwire.WindowCoveringCmdUpOrOpen, 0x00},
		{"DownOrClose", clusterwire.WindowCoveringCmdDownOrClose, 0x01},
		{"StopMotion", clusterwire.WindowCoveringCmdStopMotion, 0x02},
		{"GoToLiftPercentage", clusterwire.WindowCoveringCmdGoToLiftPercentage, 0x05},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if c.got != c.expected {
				t.Errorf("wire.WindowCoveringCmd%s = 0x%02X, want 0x%02X", c.name, c.got, c.expected)
			}
		})
	}
}

// TestWindowCoveringServer_ClusterID verifies MatterClusterID returns 0x0102.
func TestWindowCoveringServer_ClusterID(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	if got := srv.MatterClusterID(); got != clusterwire.WindowCoveringClusterID {
		t.Errorf("MatterClusterID = 0x%04X, want 0x%04X", got, clusterwire.WindowCoveringClusterID)
	}
}

// TestWindowCoveringServer_MandatoryAttributeRead verifies that all
// mandatory WindowCovering attributes return (value, true).
func TestWindowCoveringServer_MandatoryAttributeRead(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{
		Type:                         0,
		EndProductType:               0,
		FeatureMap:                   0x05, // LF | PA_LF
		InitialPositionPercent100ths: 5000,
	})

	mandatory := []uint32{
		clusterwire.WindowCoveringAttrType,
		clusterwire.WindowCoveringAttrConfigStatus,
		clusterwire.WindowCoveringAttrOperationalStatus,
		clusterwire.WindowCoveringAttrTargetPositionLiftPercent100ths,
		clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths,
		clusterwire.WindowCoveringAttrEndProductType,
		clusterwire.WindowCoveringAttrMode,
	}

	for _, id := range mandatory {
		v, ok := srv.MatterRead(id)
		if !ok {
			t.Errorf("MatterRead(0x%04X) ok = false, want true", id)
		}

		if v == nil {
			t.Errorf("MatterRead(0x%04X) value = nil, want non-nil", id)
		}
	}
}

// TestWindowCoveringServer_InitialPosition verifies that the configured
// initial position is returned for both current and target attrs.
func TestWindowCoveringServer_InitialPosition(t *testing.T) {
	t.Parallel()

	const initial uint16 = 7500
	srv := cover.NewWindowCoveringServer(cover.Config{InitialPositionPercent100ths: initial})

	v, ok := srv.MatterRead(clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths)
	if !ok {
		t.Fatal("CurrentPositionLiftPercent100ths: ok = false")
	}

	if got, _ := v.(uint16); got != initial {
		t.Errorf("CurrentPositionLiftPercent100ths = %d, want %d", got, initial)
	}

	v2, ok := srv.MatterRead(clusterwire.WindowCoveringAttrTargetPositionLiftPercent100ths)
	if !ok {
		t.Fatal("TargetPositionLiftPercent100ths: ok = false")
	}

	if got, _ := v2.(uint16); got != initial {
		t.Errorf("TargetPositionLiftPercent100ths = %d, want %d", got, initial)
	}
}

// TestWindowCoveringServer_UpOrOpen drives the cover to fully open.
func TestWindowCoveringServer_UpOrOpen(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{InitialPositionPercent100ths: 10000})
	_, err := srv.MatterInvoke(context.Background(), clusterwire.WindowCoveringCmdUpOrOpen, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("UpOrOpen: %v", err)
	}

	v, _ := srv.MatterRead(clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths)
	if got, _ := v.(uint16); got != 0 {
		t.Errorf("after UpOrOpen: CurrentPosition = %d, want 0", got)
	}
}

// TestWindowCoveringServer_DownOrClose drives the cover to fully closed.
func TestWindowCoveringServer_DownOrClose(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{InitialPositionPercent100ths: 0})
	_, err := srv.MatterInvoke(context.Background(), clusterwire.WindowCoveringCmdDownOrClose, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("DownOrClose: %v", err)
	}

	v, _ := srv.MatterRead(clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths)
	if got, _ := v.(uint16); got != 10000 {
		t.Errorf("after DownOrClose: CurrentPosition = %d, want 10000", got)
	}
}

// TestWindowCoveringServer_StopMotion verifies StopMotion clears the
// operational status.
func TestWindowCoveringServer_StopMotion(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	_, err := srv.MatterInvoke(context.Background(), clusterwire.WindowCoveringCmdStopMotion, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("StopMotion: %v", err)
	}

	v, _ := srv.MatterRead(clusterwire.WindowCoveringAttrOperationalStatus)
	if got, _ := v.(uint8); got != 0 {
		t.Errorf("after StopMotion: OperationalStatus = %d, want 0", got)
	}
}

// TestWindowCoveringServer_GoToLiftPercentage moves the cover to a
// specific position via bare uint16 payload.
func TestWindowCoveringServer_GoToLiftPercentage(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	const target uint16 = 3500
	_, err := srv.MatterInvoke(context.Background(), clusterwire.WindowCoveringCmdGoToLiftPercentage, target, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}

	v, _ := srv.MatterRead(clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths)
	if got, _ := v.(uint16); got != target {
		t.Errorf("after GoToLiftPercentage(%d): CurrentPosition = %d, want %d", target, got, target)
	}

	v2, _ := srv.MatterRead(clusterwire.WindowCoveringAttrTargetPositionLiftPercent100ths)
	if got, _ := v2.(uint16); got != target {
		t.Errorf("after GoToLiftPercentage(%d): TargetPosition = %d, want %d", target, got, target)
	}
}

// TestWindowCoveringServer_GoToLiftPercentageMapPayload moves the cover
// using the map-with-percent-key payload variant.
func TestWindowCoveringServer_GoToLiftPercentageMapPayload(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	const target uint16 = 2000
	fields := map[string]any{"percent": target}
	_, err := srv.MatterInvoke(context.Background(), clusterwire.WindowCoveringCmdGoToLiftPercentage, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("GoToLiftPercentage(map): %v", err)
	}

	v, _ := srv.MatterRead(clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths)
	if got, _ := v.(uint16); got != target {
		t.Errorf("CurrentPosition = %d, want %d", got, target)
	}
}

// TestWindowCoveringServer_UnknownCommandReturnsError verifies that an
// unrecognised command ID returns a non-nil error.
func TestWindowCoveringServer_UnknownCommandReturnsError(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	_, err := srv.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error for unknown command, got nil")
	}
}

// TestWindowCoveringServer_WriteReturnsError verifies that MatterWrite
// returns an error for read-only attributes (e.g. Type 0x0000).
func TestWindowCoveringServer_WriteReturnsError(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	// Type (0x0000) is read-only; writes must be rejected.
	err := srv.MatterWrite(context.Background(), clusterwire.WindowCoveringAttrType, uint8(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("expected error from MatterWrite on read-only attribute Type, got nil")
	}
}

// TestWindowCoveringServer_WriteModeAccepted verifies that Mode (0x0017)
// writes are accepted and the new value is reflected by MatterRead.
// Mode is RW VM with constraint max 15 per matter.js
// window-covering-cluster.element.ts:79.
func TestWindowCoveringServer_WriteModeAccepted(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	if err := srv.MatterWrite(context.Background(), clusterwire.WindowCoveringAttrMode, uint8(3), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(Mode, 3): unexpected error: %v", err)
	}
	v, ok := srv.MatterRead(clusterwire.WindowCoveringAttrMode)
	if !ok {
		t.Fatal("MatterRead(Mode): ok=false after write")
	}
	if got, _ := v.(uint8); got != 3 {
		t.Errorf("Mode after write = %d, want 3", got)
	}
}

// TestWindowCoveringServer_WriteModeConstraint verifies that Mode > 15
// returns ConstraintError per matter.js window-covering-cluster.element.ts:79.
func TestWindowCoveringServer_WriteModeConstraint(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	err := srv.MatterWrite(context.Background(), clusterwire.WindowCoveringAttrMode, uint8(16), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite(Mode, 16) expected ConstraintError, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	sc, ok := err.(statusCoder)
	if !ok {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if sc.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusConstraintError (0x87)", sc.MatterStatusCode())
	}
}

// TestWindowCoveringServer_GoToLiftPercent_ConstraintMax10000 verifies that
// GoToLiftPercentage with pct > 10000 returns ConstraintError per
// matter.js window-covering-cluster.element.ts:72 constraint "max 10000".
func TestWindowCoveringServer_GoToLiftPercent_ConstraintMax10000(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	_, err := srv.MatterInvoke(context.Background(), clusterwire.WindowCoveringCmdGoToLiftPercentage, uint16(10001), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("GoToLiftPercentage(10001) expected ConstraintError, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	sc, ok := err.(statusCoder)
	if !ok {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if sc.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusConstraintError (0x87)", sc.MatterStatusCode())
	}
}

// TestWindowCoveringServer_MatterAttributes lists all attribute IDs and
// verifies no duplicates are present.
func TestWindowCoveringServer_MatterAttributes(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	attrs := srv.MatterAttributes()
	if len(attrs) == 0 {
		t.Fatal("MatterAttributes returned empty slice")
	}

	seen := make(map[uint32]int, len(attrs))
	for _, id := range attrs {
		seen[id]++
	}

	for id, count := range seen {
		if count > 1 {
			t.Errorf("attribute 0x%04X appears %d times in MatterAttributes", id, count)
		}
	}
}

// TestWindowCoveringServer_MatterReportable verifies the reportable list
// contains at least CurrentPositionLiftPercent100ths and OperationalStatus.
func TestWindowCoveringServer_MatterReportable(t *testing.T) {
	t.Parallel()

	srv := cover.NewWindowCoveringServer(cover.Config{})
	reportable := srv.MatterReportable()

	has := func(id uint32) bool {
		for _, v := range reportable {
			if v == id {
				return true
			}
		}

		return false
	}

	if !has(clusterwire.WindowCoveringAttrCurrentPositionLiftPercent100ths) {
		t.Error("MatterReportable missing CurrentPositionLiftPercent100ths (0x000E)")
	}

	if !has(clusterwire.WindowCoveringAttrOperationalStatus) {
		t.Error("MatterReportable missing OperationalStatus (0x000A)")
	}
}
