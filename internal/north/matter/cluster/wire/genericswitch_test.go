// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ---- fakeGenericSwitchSource ----

type fakeGenericSwitchSource struct {
	positions    uint8
	supportsLong bool
}

func (f *fakeGenericSwitchSource) MatterSwitchPositions() uint8        { return f.positions }
func (f *fakeGenericSwitchSource) MatterSwitchSupportsLongPress() bool { return f.supportsLong }

// ---- fakeEventEmitter ----

type emittedEvent struct {
	endpoint uint16
	cluster  uint32
	event    uint32
	data     any
	priority interfaces.MatterEventPriority
}

type fakeEventEmitter struct {
	calls []emittedEvent
}

func (f *fakeEventEmitter) MatterEmitEvent(endpoint uint16, cluster, event uint32, data any, priority interfaces.MatterEventPriority) {
	f.calls = append(f.calls, emittedEvent{
		endpoint: endpoint,
		cluster:  cluster,
		event:    event,
		data:     data,
		priority: priority,
	})
}

// ---- helpers ----

const (
	genericSwitchClusterID uint32 = 0x003B
	switchAttrPositions    uint32 = 0x0000
	switchAttrCurrentPos   uint32 = 0x0001
	switchAttrMultiPress   uint32 = 0x0002
	switchAttrFeatureMap   uint32 = 0xFFFC
	switchAttrRevision     uint32 = 0xFFFD

	switchEventInitialPress uint32 = 0x01
	switchEventLongPress    uint32 = 0x02
	switchEventShortRelease uint32 = 0x03
	switchEventLongRelease  uint32 = 0x04
)

func newSwitch(endpoint uint16, positions uint8, supportsLong bool) *wire.GenericSwitch {
	src := &fakeGenericSwitchSource{positions: positions, supportsLong: supportsLong}
	return wire.NewGenericSwitch(endpoint, src)
}

// ---- Tests ----

func TestGenericSwitch_ClusterID(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	if got := gs.MatterClusterID(); got != genericSwitchClusterID {
		t.Errorf("MatterClusterID() = 0x%04X, want 0x%04X", got, genericSwitchClusterID)
	}
}

func TestGenericSwitch_Read_NumberOfPositions_FromSource(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 5, false)
	v, ok := gs.MatterRead(switchAttrPositions)
	if !ok {
		t.Fatal("MatterRead(0x0000) returned ok=false")
	}
	if n, _ := v.(uint8); n != 5 {
		t.Errorf("NumberOfPositions = %v, want 5", v)
	}
}

func TestGenericSwitch_Read_NumberOfPositions_Default2WhenZero(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 0, false) // source returns 0 → cluster falls back to 2
	v, ok := gs.MatterRead(switchAttrPositions)
	if !ok {
		t.Fatal("MatterRead(0x0000) returned ok=false")
	}
	if n, _ := v.(uint8); n != 2 {
		t.Errorf("NumberOfPositions with source=0 = %v, want 2 (default)", v)
	}
}

func TestGenericSwitch_Read_CurrentPosition_AlwaysZero(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	v, ok := gs.MatterRead(switchAttrCurrentPos)
	if !ok {
		t.Fatal("MatterRead(0x0001) returned ok=false")
	}
	if n, _ := v.(uint8); n != 0 {
		t.Errorf("CurrentPosition = %v, want 0 (always idle)", v)
	}
}

func TestGenericSwitch_Read_ClusterRevision_IsTwo(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	v, ok := gs.MatterRead(switchAttrRevision)
	if !ok {
		t.Fatal("MatterRead(0xFFFD) returned ok=false")
	}
	if n, _ := v.(uint16); n != 2 {
		t.Errorf("ClusterRevision = %v, want 2", v)
	}
}

func TestGenericSwitch_Read_FeatureMap_WithoutLongPress(t *testing.T) {
	t.Parallel()
	// MS (bit 1) + MSR (bit 2) = 0x06
	const wantFeatureMap uint32 = 0x06
	gs := newSwitch(1, 2, false)
	v, ok := gs.MatterRead(switchAttrFeatureMap)
	if !ok {
		t.Fatal("MatterRead(0xFFFC) returned ok=false")
	}
	if fm, _ := v.(uint32); fm != wantFeatureMap {
		t.Errorf("FeatureMap (no long press) = 0x%04X, want 0x%04X (MS|MSR)", fm, wantFeatureMap)
	}
}

func TestGenericSwitch_Read_FeatureMap_WithLongPress(t *testing.T) {
	t.Parallel()
	// MS (bit 1) + MSR (bit 2) + MSL (bit 3) = 0x0E
	const wantFeatureMap uint32 = 0x0E
	gs := newSwitch(1, 2, true)
	v, ok := gs.MatterRead(switchAttrFeatureMap)
	if !ok {
		t.Fatal("MatterRead(0xFFFC) returned ok=false")
	}
	if fm, _ := v.(uint32); fm != wantFeatureMap {
		t.Errorf("FeatureMap (with long press) = 0x%04X, want 0x%04X (MS|MSR|MSL)", fm, wantFeatureMap)
	}
}

func TestGenericSwitch_Read_UnknownAttr_ReturnsFalse(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	_, ok := gs.MatterRead(0xDEAD)
	if ok {
		t.Error("MatterRead(unknown) returned ok=true, want false")
	}
}

func TestGenericSwitch_Write_ReturnsError(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	err := gs.MatterWrite(context.Background(), switchAttrCurrentPos, uint8(1), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite returned nil error for read-only cluster")
	}
}

func TestGenericSwitch_Invoke_ReturnsError(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	_, err := gs.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke returned nil error for cluster with no commands")
	}
}

func TestGenericSwitch_Reportable_ContainsCurrentPosition(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	reportable := gs.MatterReportable()
	if len(reportable) == 0 {
		t.Fatal("MatterReportable() returned empty slice")
	}
	found := false
	for _, id := range reportable {
		if id == switchAttrCurrentPos {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("MatterReportable() = %v, want it to contain 0x%04X (CurrentPosition)", reportable, switchAttrCurrentPos)
	}
}

func TestGenericSwitch_FireInitialPress_WithoutEmitter_NoPanic(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	// No emitter set — must not panic.
	gs.FireInitialPress(1)
}

func TestGenericSwitch_FireInitialPress_WithEmitter_CorrectEvent(t *testing.T) {
	t.Parallel()
	const endpoint uint16 = 3
	gs := newSwitch(endpoint, 2, false)
	emitter := &fakeEventEmitter{}
	gs.SetMatterEventEmitter(emitter)

	gs.FireInitialPress(1)

	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	ev := emitter.calls[0]
	if ev.endpoint != endpoint {
		t.Errorf("endpoint = %d, want %d", ev.endpoint, endpoint)
	}
	if ev.cluster != genericSwitchClusterID {
		t.Errorf("cluster = 0x%04X, want 0x%04X", ev.cluster, genericSwitchClusterID)
	}
	if ev.event != switchEventInitialPress {
		t.Errorf("event = 0x%02X, want 0x%02X (InitialPress)", ev.event, switchEventInitialPress)
	}
	if ev.priority != interfaces.MatterEventPriorityCritical {
		t.Errorf("priority = %v, want Critical", ev.priority)
	}
}

func TestGenericSwitch_FireShortRelease_WithEmitter_InfoPriority(t *testing.T) {
	t.Parallel()
	const endpoint uint16 = 4
	gs := newSwitch(endpoint, 2, false)
	emitter := &fakeEventEmitter{}
	gs.SetMatterEventEmitter(emitter)

	gs.FireShortRelease(0)

	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	ev := emitter.calls[0]
	if ev.event != switchEventShortRelease {
		t.Errorf("event = 0x%02X, want 0x%02X (ShortRelease)", ev.event, switchEventShortRelease)
	}
	if ev.priority != interfaces.MatterEventPriorityInfo {
		t.Errorf("priority = %v, want Info", ev.priority)
	}
}

func TestGenericSwitch_FireLongPress_NoLongPressSupport_NoOp(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	emitter := &fakeEventEmitter{}
	gs.SetMatterEventEmitter(emitter)

	gs.FireLongPress(1)

	if len(emitter.calls) != 0 {
		t.Errorf("emitter received %d calls, want 0 (no long-press support)", len(emitter.calls))
	}
}

func TestGenericSwitch_FireLongPress_WithLongPressSupport_EmitsCritical(t *testing.T) {
	t.Parallel()
	const endpoint uint16 = 5
	gs := newSwitch(endpoint, 2, true)
	emitter := &fakeEventEmitter{}
	gs.SetMatterEventEmitter(emitter)

	gs.FireLongPress(1)

	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	ev := emitter.calls[0]
	if ev.event != switchEventLongPress {
		t.Errorf("event = 0x%02X, want 0x%02X (LongPress)", ev.event, switchEventLongPress)
	}
	if ev.priority != interfaces.MatterEventPriorityCritical {
		t.Errorf("priority = %v, want Critical", ev.priority)
	}
}

func TestGenericSwitch_FireLongRelease_NoLongPressSupport_NoOp(t *testing.T) {
	t.Parallel()
	gs := newSwitch(1, 2, false)
	emitter := &fakeEventEmitter{}
	gs.SetMatterEventEmitter(emitter)

	gs.FireLongRelease(0)

	if len(emitter.calls) != 0 {
		t.Errorf("emitter received %d calls, want 0 (no long-press support)", len(emitter.calls))
	}
}

func TestGenericSwitch_FireLongRelease_WithLongPressSupport_EmitsInfoPriority(t *testing.T) {
	t.Parallel()
	const endpoint uint16 = 6
	gs := newSwitch(endpoint, 2, true)
	emitter := &fakeEventEmitter{}
	gs.SetMatterEventEmitter(emitter)

	gs.FireLongRelease(0)

	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	ev := emitter.calls[0]
	if ev.event != switchEventLongRelease {
		t.Errorf("event = 0x%02X, want 0x%02X (LongRelease)", ev.event, switchEventLongRelease)
	}
	if ev.priority != interfaces.MatterEventPriorityInfo {
		t.Errorf("priority = %v, want Info", ev.priority)
	}
}

func TestGenericSwitch_MatterAttributes_ExcludesMultiPressMaxWhenMSMAbsent(t *testing.T) {
	t.Parallel()
	// MS+MSR only — MSM not set. MultiPressMax (0x0002) has conformance MSM
	// and must NOT appear in the advertised attribute list.
	gs := newSwitch(1, 2, false)
	for _, id := range gs.MatterAttributes() {
		if id == switchAttrMultiPress {
			t.Errorf("MatterAttributes() contains MultiPressMax (0x%04X) but MSM feature is not set", id)
		}
	}
}

func TestGenericSwitch_MatterEvents_FeatureGated(t *testing.T) {
	t.Parallel()
	// MS+MSR+MSL — no MSM. Events should contain InitialPress, ShortRelease,
	// LongPress, LongRelease but NOT MultiPressOngoing or MultiPressComplete.
	gs := newSwitch(1, 2, true) // supportsLong = true → MSL set
	evMap := make(map[uint32]bool)
	for _, e := range gs.MatterEvents() {
		evMap[e] = true
	}
	if !evMap[0x01] {
		t.Error("MatterEvents() missing InitialPress (0x01)")
	}
	if !evMap[0x03] {
		t.Error("MatterEvents() missing ShortRelease (0x03) — MSR feature set")
	}
	if !evMap[0x02] {
		t.Error("MatterEvents() missing LongPress (0x02) — MSL feature set")
	}
	if !evMap[0x04] {
		t.Error("MatterEvents() missing LongRelease (0x04) — MSL feature set")
	}
	if evMap[0x05] {
		t.Error("MatterEvents() contains MultiPressOngoing (0x05) but MSM feature is not set")
	}
	if evMap[0x06] {
		t.Error("MatterEvents() contains MultiPressComplete (0x06) but MSM feature is not set")
	}
}

func TestGenericSwitch_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	src := &fakeGenericSwitchSource{positions: 2, supportsLong: false}
	gs := wire.NewGenericSwitch(1, src)
	gs.SetMatterEventEmitter(&fakeEventEmitter{})
	if len(gs.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}
