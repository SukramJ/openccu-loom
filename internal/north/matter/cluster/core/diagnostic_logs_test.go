// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestDiagLogs_ClusterID(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	if got := d.MatterClusterID(); got != 0x0032 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0032", got)
	}
}

func TestDiagLogs_ClusterRevision(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	v, ok := d.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 1 {
		t.Fatalf("ClusterRevision = %v, want 1", v)
	}
}

func TestDiagLogs_ReadFeatureMapZero(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	v, ok := d.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) != 0 {
		t.Fatalf("FeatureMap = %v, want 0", v)
	}
}

func TestDiagLogs_ReadUnknownAttrReturnsFalse(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	for _, attrID := range []uint32{0x0000, 0x0001, 0xBEEF} {
		v, ok := d.MatterRead(attrID)
		if ok || v != nil {
			t.Errorf("MatterRead(0x%04X) = (%v, %v), want (nil, false)", attrID, v, ok)
		}
	}
}

func TestDiagLogs_InvokeRetrieveLogsRequest(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	ctx := context.Background()
	resp, err := d.MatterInvoke(ctx, 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke(RetrieveLogsRequest) error: %v", err)
	}
	if resp == nil {
		t.Fatal("MatterInvoke(RetrieveLogsRequest) resp=nil, want RetrieveLogsResponse")
	}
	r := resp.(core.RetrieveLogsResponse)
	if r.Status != core.LogStatusNoLogs {
		t.Fatalf("Status = %d, want LogStatusNoLogs (%d)", r.Status, core.LogStatusNoLogs)
	}
	if len(r.LogContent) != 0 {
		t.Fatalf("LogContent len=%d, want 0", len(r.LogContent))
	}
}

func TestDiagLogs_InvokeUnknownCmdReturnsError(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	ctx := context.Background()
	for _, cmdID := range []uint32{0x01, 0x02, 0xFF} {
		_, err := d.MatterInvoke(ctx, cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestDiagLogs_WriteReturnsError(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	ctx := context.Background()
	for _, attrID := range []uint32{0x0000, 0xFFFD, 0xBEEF} {
		err := d.MatterWrite(ctx, attrID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterWrite(0x%04X) expected error, got nil", attrID)
		}
	}
}

type fakeLogProvider struct {
	payload []byte
	err     error
}

func (f *fakeLogProvider) Logs(_ context.Context, _ uint8) ([]byte, error) {
	return f.payload, f.err
}

func TestDiagLogs_AttachProvider_NilProvider(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	// Attaching nil must not panic.
	d.AttachProvider(nil)
	resp, err := d.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke after nil provider: %v", err)
	}
	r := resp.(core.RetrieveLogsResponse)
	if r.Status != core.LogStatusNoLogs {
		t.Fatalf("Status = %d, want LogStatusNoLogs", r.Status)
	}
}

func TestDiagLogs_AttachProvider_SuccessSmallPayload(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	want := []byte("hello logs")
	d.AttachProvider(&fakeLogProvider{payload: want})
	resp, err := d.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke: %v", err)
	}
	r := resp.(core.RetrieveLogsResponse)
	if r.Status != core.LogStatusSuccess {
		t.Fatalf("Status = %d, want LogStatusSuccess", r.Status)
	}
	if !bytes.Equal(r.LogContent, want) {
		t.Fatalf("LogContent = %q, want %q", r.LogContent, want)
	}
}

func TestDiagLogs_AttachProvider_ExhaustedLargePayload(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	// Payload larger than MatterDiagnosticLogsInlineCap (1024).
	big := bytes.Repeat([]byte("X"), core.MatterDiagnosticLogsInlineCap+1)
	d.AttachProvider(&fakeLogProvider{payload: big})
	resp, err := d.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke exhausted: %v", err)
	}
	r := resp.(core.RetrieveLogsResponse)
	if r.Status != core.LogStatusExhausted {
		t.Fatalf("Status = %d, want LogStatusExhausted", r.Status)
	}
	if len(r.LogContent) != core.MatterDiagnosticLogsInlineCap {
		t.Fatalf("LogContent len = %d, want %d", len(r.LogContent), core.MatterDiagnosticLogsInlineCap)
	}
}

func TestDiagLogs_AttachProvider_ProviderError(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	d.AttachProvider(&fakeLogProvider{err: errors.New("transient I/O failure")})
	resp, err := d.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		// Provider errors must NOT surface as IM errors per §11.11.6.1.
		t.Fatalf("MatterInvoke must not return IM error on provider failure: %v", err)
	}
	r := resp.(core.RetrieveLogsResponse)
	if r.Status != core.LogStatusBusy {
		t.Fatalf("Status = %d, want LogStatusBusy (transient provider failure)", r.Status)
	}
}

func TestDiagLogs_AttachProvider_EmptyPayloadIsNoLogs(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	d.AttachProvider(&fakeLogProvider{payload: []byte{}})
	resp, err := d.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke: %v", err)
	}
	r := resp.(core.RetrieveLogsResponse)
	if r.Status != core.LogStatusNoLogs {
		t.Fatalf("Status = %d, want LogStatusNoLogs for empty payload", r.Status)
	}
}

func TestDiagLogs_SetBootEpoch(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	past := time.Now().Add(-5 * time.Second)
	d.SetBootEpoch(past)
	resp, err := d.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke: %v", err)
	}
	r := resp.(core.RetrieveLogsResponse)
	// TimeSinceBoot should reflect the ~5 s offset (at least 4 s in practice).
	if r.TimeSinceBoot < uint64(4*time.Second) {
		t.Fatalf("TimeSinceBoot = %d ns, want >= 4s (boot epoch shifted)", r.TimeSinceBoot)
	}
}

func TestDiagLogs_InvokeIntentDecoding_MapUint8(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	d.AttachProvider(&fakeLogProvider{payload: []byte("x")})
	// intent field 0 → NetworkDiag=1 encoded as uint8.
	fields := map[uint8]any{0: uint8(1)}
	resp, err := d.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke with map intent: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
}

func TestDiagLogs_InvokeIntentDecoding_MapUint64(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	d.AttachProvider(&fakeLogProvider{payload: []byte("x")})
	// intent field 0 → CrashLogs=2 encoded as uint64 (raw TLV decoded form).
	fields := map[uint8]any{0: uint64(2)}
	resp, err := d.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke with map uint64 intent: %v", err)
	}
	_ = resp
}

func TestDiagLogs_InvokeIntentDecoding_UnknownFieldsDefaultToEndUser(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	d.AttachProvider(&fakeLogProvider{payload: []byte("x")})
	// A non-map type falls through to the default EndUserSupport branch.
	fields := "unexpected-type"
	resp, err := d.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke with unknown fields type: %v", err)
	}
	_ = resp
}

func TestDiagLogs_MatterAcceptedCommands(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	list := d.MatterAcceptedCommands()
	if len(list) == 0 {
		t.Fatal("MatterAcceptedCommands() is empty")
	}
	// RetrieveLogsRequest = 0x00 must be listed.
	if slices.Contains(list, 0x00) {
		return
	}
	t.Fatalf("MatterAcceptedCommands() = %v — missing RetrieveLogsRequest (0x00)", list)
}

func TestDiagLogs_MatterGeneratedCommands(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	list := d.MatterGeneratedCommands()
	if len(list) == 0 {
		t.Fatal("MatterGeneratedCommands() is empty")
	}
	// RetrieveLogsResponse = 0x01 must be listed.
	if slices.Contains(list, 0x01) {
		return
	}
	t.Fatalf("MatterGeneratedCommands() = %v — missing RetrieveLogsResponse (0x01)", list)
}

func TestDiagLogs_MatterReportableIsEmpty(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	list := d.MatterReportable()
	if len(list) != 0 {
		t.Fatalf("MatterReportable() = %v, want nil/empty (no push-able attrs)", list)
	}
}

func TestDiagLogs_MatterAttributesIsEmpty(t *testing.T) {
	t.Parallel()
	d := core.NewDiagnosticLogs()
	list := d.MatterAttributes()
	if len(list) != 0 {
		t.Fatalf("MatterAttributes() = %v, want nil/empty (globals only)", list)
	}
}
