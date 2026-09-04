// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
)

func TestOTARequestor_ClusterID(t *testing.T) {
	t.Parallel()
	// matter.js HEAD ota-software-update-requestor.element.ts:20 → id 0x002A.
	// 0x0029 is OtaSoftwareUpdateProvider, a different cluster.
	o := core.NewOTASoftwareUpdateRequestor()
	if got := o.MatterClusterID(); got != 0x002A {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x002A", got)
	}
}

func TestOTARequestor_ClusterRevision(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	v, ok := o.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 1 {
		t.Fatalf("ClusterRevision = %v, want 1", v)
	}
}

func TestOTARequestor_ReadDefaultOTAProvidersEmpty(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	v, ok := o.MatterRead(0x0000)
	if !ok {
		t.Fatal("DefaultOTAProviders: ok=false")
	}
	list := v.([]any)
	if len(list) != 0 {
		t.Fatalf("DefaultOTAProviders len=%d, want 0", len(list))
	}
}

func TestOTARequestor_ReadUpdatePossibleFalse(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	v, ok := o.MatterRead(0x0001)
	if !ok {
		t.Fatal("UpdatePossible: ok=false")
	}
	if v.(bool) != false {
		t.Fatal("UpdatePossible = true, want false")
	}
}

func TestOTARequestor_ReadUpdateStateIdle(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	v, ok := o.MatterRead(0x0002)
	if !ok {
		t.Fatal("UpdateState: ok=false")
	}
	if v.(uint8) != core.OTAUpdateStateIdle {
		t.Fatalf("UpdateState = %d, want OTAUpdateStateIdle (%d)", v.(uint8), core.OTAUpdateStateIdle)
	}
}

func TestOTARequestor_ReadUpdateStateProgressNil(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	v, ok := o.MatterRead(0x0003)
	// The attribute exists (ok=true) but is nullable (nil value).
	if !ok {
		t.Fatal("UpdateStateProgress: ok=false")
	}
	if v != nil {
		t.Fatalf("UpdateStateProgress = %v, want nil", v)
	}
}

func TestOTARequestor_WriteDefaultOTAProvidersNoOp(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	err := o.MatterWrite(context.Background(), 0x0000, []any{})
	if err != nil {
		t.Fatalf("Write DefaultOTAProviders: %v", err)
	}
}

func TestOTARequestor_WriteOtherAttrReturnsError(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	ctx := context.Background()
	for _, attrID := range []uint32{0x0001, 0x0002, 0x0003, 0xFFFD} {
		err := o.MatterWrite(ctx, attrID, nil)
		if err == nil {
			t.Errorf("MatterWrite(0x%04X) expected error, got nil", attrID)
		}
	}
}

func TestOTARequestor_InvokeAnnounceOTAProvider(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	ctx := context.Background()
	resp, err := o.MatterInvoke(ctx, 0x00, nil)
	if err != nil {
		t.Fatalf("MatterInvoke(AnnounceOTAProvider) error: %v", err)
	}
	if resp != nil {
		t.Fatalf("MatterInvoke(AnnounceOTAProvider) resp = %v, want nil", resp)
	}
}

func TestOTARequestor_InvokeUnknownCmdReturnsError(t *testing.T) {
	t.Parallel()
	o := core.NewOTASoftwareUpdateRequestor()
	ctx := context.Background()
	for _, cmdID := range []uint32{0x01, 0x02, 0xFF} {
		_, err := o.MatterInvoke(ctx, cmdID, nil)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestOTARequestor_MatterReportable(t *testing.T) {
	t.Parallel()
	ota := core.NewOTASoftwareUpdateRequestor()
	list := ota.MatterReportable()
	if len(list) == 0 {
		t.Fatal("MatterReportable() is empty")
	}
}

func TestOTARequestor_MatterAttributes(t *testing.T) {
	t.Parallel()
	ota := core.NewOTASoftwareUpdateRequestor()
	list := ota.MatterAttributes()
	if len(list) == 0 {
		t.Fatal("MatterAttributes() is empty")
	}
}
