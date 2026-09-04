// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

func TestGenDiag_ClusterID(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	if got := g.MatterClusterID(); got != 0x0033 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0033", got)
	}
}

func TestGenDiag_ClusterRevision(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 3 {
		t.Fatalf("ClusterRevision = %v, want 3", v)
	}
}

// TestGenDiag_ReadNetworkInterfacesNonEmpty asserts that
// `NetworkInterfaces` (Matter §11.12.4.1) carries at least one
// NetworkInterfaceStruct entry. Apple Home's HMMTRAccessoryServerBrowser
// reads this list to build its "topology dictionary"; an empty list
// makes Apple log "No enumeration/topology dictionary found" + "Nil
// supported link layer types" and tear the fabric down via
// RemoveFabric ~5 s after Subscribe-Initial.
func TestGenDiag_ReadNetworkInterfacesNonEmpty(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(0x0000)
	if !ok {
		t.Fatal("NetworkInterfaces: ok=false")
	}
	list, ok := v.([]core.NetworkInterfaceStruct)
	if !ok {
		t.Fatalf("NetworkInterfaces type = %T, want []core.NetworkInterfaceStruct", v)
	}
	if len(list) == 0 {
		t.Fatal("NetworkInterfaces is empty — Apple HAP-Mapper rejects pair on empty list")
	}
	// Every entry must carry a non-empty Name.
	for i, entry := range list {
		if entry.Name == "" {
			t.Errorf("entry[%d] has empty Name", i)
		}
	}
}

func TestGenDiag_ReadRebootCount(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(0x0001)
	if !ok {
		t.Fatal("RebootCount: ok=false")
	}
	if v.(uint16) != 1 {
		t.Fatalf("RebootCount = %v, want 1", v)
	}
}

func TestGenDiag_ReadUpTimeNonNegative(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(0x0002)
	if !ok {
		t.Fatal("UpTime: ok=false")
	}
	up := v.(uint64)
	if up > 1<<62 {
		t.Fatalf("UpTime suspiciously large: %d", up)
	}
}

func TestGenDiag_BootReasonAttributeUnimplemented(t *testing.T) {
	t.Parallel()
	// BootReason (0x0004) is intentionally NOT exposed as an attribute —
	// matter.js's bridge sample emits it only via the §11.12.8.1
	// BootReason *event* on Subscribe-Initial, and Apple Home parses
	// that event into the `estimated start time forward` log line.
	// The attribute on the wire causes Apple's MTRDevice-cache filter
	// to refuse the GeneralDiagnostics cluster (verified empirically
	// via byte-diff). EmitBootReason() remains the supported path.
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	if _, ok := g.MatterRead(0x0004); ok {
		t.Fatal("BootReason: attribute should be unimplemented (matter.js parity)")
	}
}

func TestGenDiag_FaultListsAttributesUnimplemented(t *testing.T) {
	t.Parallel()
	// ActiveHardwareFaults / ActiveRadioFaults / ActiveNetworkFaults
	// are OPTIONAL per §11.12.6 and matter.js Sample does not emit them.
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	for _, attrID := range []uint32{0x0005, 0x0006, 0x0007} {
		if _, ok := g.MatterRead(attrID); ok {
			t.Fatalf("attr 0x%04X: should be unimplemented (matter.js parity)", attrID)
		}
	}
}

func TestGenDiag_ReadTestEventTriggersEnabled(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(0x0008)
	if !ok {
		t.Fatal("TestEventTriggersEnabled: ok=false")
	}
	if v.(bool) != false {
		t.Fatal("TestEventTriggersEnabled = true, want false")
	}
}

func TestGenDiag_UpTimeMonotonicallyNonDecreasing(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v1, _ := g.MatterRead(0x0002)
	up1 := v1.(uint64)
	time.Sleep(10 * time.Millisecond)
	v2, _ := g.MatterRead(0x0002)
	up2 := v2.(uint64)
	if up2 < up1 {
		t.Fatalf("UpTime decreased: %d → %d", up1, up2)
	}
}

func TestGenDiag_WriteReturnsError(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	ctx := context.Background()
	err := g.MatterWrite(ctx, 0x0001, uint16(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for write, got nil")
	}
}

func TestGenDiag_InvokeReturnsError(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	ctx := context.Background()
	// 0x00 TestEventTrigger and 0xFF (unknown) must reject; 0x01
	// TimeSnapshot is implemented (Matter 1.5) — see
	// TestGenDiag_TimeSnapshot.
	for _, cmdID := range []uint32{0x00, 0xFF} {
		_, err := g.MatterInvoke(ctx, cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

// genDiagStatusCoder is a local alias for the MatterStatusCode() method
// [im.StatusCodeError] carries. Deliberately does not embed Error() so
// errorlint does not mistake the assertion below for an error-unwrap check —
// mirrors the statusCoder helper in matter_negative_write_parity_test.go.
type genDiagStatusCoder interface {
	MatterStatusCode() im.StatusCode
}

// TestGenDiag_TestEventTrigger_ReturnsConstraintError asserts that invoking
// TestEventTrigger (0x00, conformance M) fails with a typed
// [im.StatusCodeError] carrying [im.StatusConstraintError] — the bridge
// configures no test-event enable key, so every invocation is rejected the
// same way matter.js's #validateTestEnabledKey rejects an all-zero /
// non-matching key (GeneralDiagnosticsServer.ts:99,104).
func TestGenDiag_TestEventTrigger_ReturnsConstraintError(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	_, err := g.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("TestEventTrigger: expected error, got nil")
	}
	sc, ok := err.(genDiagStatusCoder)
	if !ok {
		t.Fatalf("TestEventTrigger error %v (%T) does not implement im.StatusCodeError", err, err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusConstraintError {
		t.Errorf("MatterStatusCode() = %v, want StatusConstraintError", got)
	}
}

// TestGenDiag_MatterAcceptedCommands_IncludesTestEventTrigger asserts that
// TestEventTrigger (0x00, conformance M) is enumerated in
// MatterAcceptedCommands alongside TimeSnapshot (0x01) — the mandatory
// command must be advertised even though the handler always rejects it.
func TestGenDiag_MatterAcceptedCommands_IncludesTestEventTrigger(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	list := g.MatterAcceptedCommands()
	for _, want := range []uint32{0x00, 0x01} {
		if !slices.Contains(list, want) {
			t.Errorf("MatterAcceptedCommands() = %v — missing 0x%02X", list, want)
		}
	}
}

func TestGenDiag_TimeSnapshot(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	resp, err := g.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("TimeSnapshot: %v", err)
	}
	r, ok := resp.(core.TimeSnapshotResponse)
	if !ok {
		t.Fatalf("response = %T, want core.TimeSnapshotResponse", resp)
	}
	if r.PosixTimeMs == nil {
		t.Fatal("PosixTimeMs is nil; bridge has a wall-clock so the value should be present")
	}
	if *r.PosixTimeMs == 0 {
		t.Errorf("PosixTimeMs = 0, want a wall-clock value")
	}
}

// TestGenDiag_MatterEventsContainsBootReason asserts that
// MatterEvents() returns event-id 0x0003 (BootReason), satisfying the
// MatterClusterEventLister contract for the EventList attribute.
func TestGenDiag_MatterEventsContainsBootReason(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	events := g.MatterEvents()
	if slices.Contains(events, 0x0003) {
		return
	}
	t.Fatalf("MatterEvents() = %v — missing BootReason (0x0003)", events)
}

// TestGenDiag_EmitBootReason_FiresEvent asserts that EmitBootReason
// fires the Matter §11.12.8.1 BootReason event (cluster=0x0033,
// event=0x0003, priority=Critical) with the configured BootReason
// value in the payload. Mirrors matter.js
// packages/node/src/behaviors/general-diagnostics/
// GeneralDiagnosticsServer.ts startup-event emission.
func TestGenDiag_EmitBootReason_FiresEvent(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonSoftwareReset)
	emitter := &fakeEmitter{}
	g.SetMatterEventEmitter(emitter)
	g.SetEndpoint(0)

	g.EmitBootReason()

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(got))
	}
	ev := got[0]
	if ev.endpoint != 0 {
		t.Errorf("endpoint = %d, want 0", ev.endpoint)
	}
	if ev.cluster != 0x0033 {
		t.Errorf("cluster = 0x%04X, want 0x0033 (GeneralDiagnostics)", ev.cluster)
	}
	if ev.event != 0x0003 {
		t.Errorf("event = 0x%04X, want 0x0003 (BootReason)", ev.event)
	}
	if ev.priority != matterport.EventPriorityCritical {
		t.Errorf("priority = %v, want Critical", ev.priority)
	}
	payload, ok := ev.data.(core.BootReasonEvent)
	if !ok {
		t.Fatalf("data = %T, want core.BootReasonEvent", ev.data)
	}
	if payload.BootReason != core.BootReasonSoftwareReset {
		t.Errorf("BootReason = %d, want %d (SoftwareReset)", payload.BootReason, core.BootReasonSoftwareReset)
	}
}

// TestGenDiag_EmitBootReason_NoOpWhenEmitterNil asserts that calling
// EmitBootReason before SetMatterEventEmitter does not panic and emits
// no events.
func TestGenDiag_EmitBootReason_NoOpWhenEmitterNil(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	// No emitter wired — must not panic.
	g.EmitBootReason()
}

func TestGenDiag_UpTimeSeconds(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	up := g.UpTimeSeconds()
	if up > 1<<32 {
		t.Fatalf("UpTimeSeconds suspiciously large: %d", up)
	}
}

func TestGenDiag_SetPersistedCounters(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	g.SetPersistedCounters(42, 1000)
	v, ok := g.MatterRead(0x0001)
	if !ok {
		t.Fatal("RebootCount: ok=false after SetPersistedCounters")
	}
	if v.(uint16) != 42 {
		t.Fatalf("RebootCount = %v, want 42", v)
	}
}

func TestGenDiag_MatterDataVersionNonZero(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	// DataVersion is seeded at construction with a non-zero sentinel so
	// DataVersionFilter=0 doesn't produce false-positive cache hits.
	if g.MatterDataVersion() == 0 {
		t.Fatal("MatterDataVersion() = 0 — expected non-zero sentinel")
	}
}

func TestGenDiag_SetPersistedCountersBumpsDataVersion(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v0 := g.MatterDataVersion()
	g.SetPersistedCounters(5, 100)
	v1 := g.MatterDataVersion()
	if v1 <= v0 {
		t.Fatalf("DataVersion did not increase after SetPersistedCounters: %d → %d", v0, v1)
	}
}

func TestGenDiag_MatterReportable(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	list := g.MatterReportable()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	// UpTime (0x0002) is always reportable.
	if !have[0x0002] {
		t.Errorf("MatterReportable() missing UpTime (0x0002)")
	}
}

func TestGenDiag_MatterAttributes(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	list := g.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001, 0x0002, 0x0008} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}

func TestGenDiag_MatterAcceptedCommands(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	list := g.MatterAcceptedCommands()
	if slices.Contains(list, 0x01) {
		return
	}
	t.Fatalf("MatterAcceptedCommands() = %v — missing TimeSnapshot (0x01)", list)
}

func TestGenDiag_MatterGeneratedCommands(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	list := g.MatterGeneratedCommands()
	if slices.Contains(list, 0x02) {
		return
	}
	t.Fatalf("MatterGeneratedCommands() = %v — missing TimeSnapshotResponse (0x02)", list)
}

func TestGenDiag_NetworkInterfacesHardwareAddressConstraint(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(0x0000)
	if !ok {
		t.Fatal("NetworkInterfaces: ok=false")
	}
	list := v.([]core.NetworkInterfaceStruct)
	// Every entry must have HardwareAddress exactly 6 or 8 bytes — the
	// matter.js `hwadr` constraint that Apple's IM-decoder enforces.
	for i, entry := range list {
		n := len(entry.HardwareAddress)
		if n != 0 && n != 6 && n != 8 {
			t.Errorf("entry[%d] HardwareAddress len=%d; must be 0, 6, or 8", i, n)
		}
	}
}

func TestGenDiag_TotalOperationalHoursReadable(t *testing.T) {
	t.Parallel()
	g := core.NewGeneralDiagnostics(core.BootReasonPowerOnReboot)
	v, ok := g.MatterRead(0x0003) // TotalOperationalHours
	if !ok {
		t.Fatal("TotalOperationalHours: ok=false")
	}
	_ = v.(uint32)
}
