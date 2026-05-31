// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

// White-box tests for DataVersion stamping + DataVersionFilter evaluation
// in HandleReadRequest. Lives in package im (not im_test) to access the
// unexported matchDataVersionFilter helper and the tag constants used for
// TLV verification.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// --- dataVersionDispatcher: test helper ---

// dataVersionDispatcher is a fakeDispatcher variant that carries a
// per-read DataVersion to test that HandleReadRequest stamps it on
// AttributeReport.DataVersion.
type dataVersionDispatcher struct {
	readVal     AttributeValue
	readStat    StatusCode
	dataVersion uint32
}

func (d *dataVersionDispatcher) Read(_ context.Context, p ConcreteAttributePath) []ReadResult {
	return []ReadResult{{
		Path:        p,
		Value:       d.readVal,
		Status:      d.readStat,
		DataVersion: d.dataVersion,
	}}
}

func (d *dataVersionDispatcher) Write(_ context.Context, p ConcreteAttributePath, _ AttributeValue) []WriteResult {
	return []WriteResult{{Path: p, Status: d.readStat}}
}

func (d *dataVersionDispatcher) Invoke(_ context.Context, p ConcreteCommandPath, _ any) InvokeResult {
	return InvokeResult{Path: p, Status: d.readStat}
}

// --- Tests ---

// TestRead_DataVersionStampedFromCluster verifies that
// HandleReadRequest propagates ReadResult.DataVersion to
// AttributeReport.DataVersion.
//
// The dispatcher returns DataVersion=5; we assert the emitted report
// carries the same value and that it encodes as TlvUInt32 at context
// tag 0 inside the AttributeDataIB.
func TestRead_DataVersionStampedFromCluster(t *testing.T) {
	t.Parallel()

	const wantDV uint32 = 5

	d := &dataVersionDispatcher{
		readVal:     AttributeValue{Value: true},
		readStat:    StatusSuccess,
		dataVersion: wantDV,
	}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 0, HasEndpoint: true, Cluster: 0x001F, HasCluster: true, Attribute: 0x0000, HasAttribute: true},
		},
	}

	rd := HandleReadRequest(context.Background(), d, req)
	if len(rd.Reports) != 1 {
		t.Fatalf("reports=%d, want 1", len(rd.Reports))
	}
	got := rd.Reports[0].DataVersion
	if got != wantDV {
		t.Errorf("AttributeReport.DataVersion = %d, want %d", got, wantDV)
	}

	// Also verify the TLV wire encodes the value at DataVersion field.
	enc := tlv.NewEncoder()
	rd.MarshalTLV(enc, func(e *tlv.Encoder, tag tlv.Tag, _ AttributeValue) {
		e.PutBool(tag, true)
	})
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("MarshalTLV: %v", err)
	}
	verifyAttributeDataIBDataVersion(t, wire, wantDV)
}

// TestRead_DataVersionFilterMatching_SkipsCluster verifies that when
// the controller passes DataVersionFilter[{ep=0, cluster=0x001F, v=5}]
// and the cluster returns DataVersion=5, HandleReadRequest emits zero
// AttributeDataIBs for that cluster (controller cache is fresh).
func TestRead_DataVersionFilterMatching_SkipsCluster(t *testing.T) {
	t.Parallel()

	const dv uint32 = 5

	d := &dataVersionDispatcher{
		readVal:     AttributeValue{Value: true},
		readStat:    StatusSuccess,
		dataVersion: dv,
	}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 0, HasEndpoint: true, Cluster: 0x001F, HasCluster: true, Attribute: 0x0000, HasAttribute: true},
		},
		DataVersionFilters: []DataVersionFilter{
			{Endpoint: 0, Cluster: 0x001F, DataVersion: dv},
		},
	}

	rd := HandleReadRequest(context.Background(), d, req)
	if len(rd.Reports) != 0 {
		t.Errorf("reports=%d, want 0 (cluster filtered)", len(rd.Reports))
	}
}

// TestRead_DataVersionFilterMismatch_EmitsAttributes verifies that when
// the controller passes DataVersionFilter[{ep=0, cluster=0x001F, v=4}]
// but the cluster reports DataVersion=5 (changed), HandleReadRequest
// emits the attribute data with DataVersion=5.
func TestRead_DataVersionFilterMismatch_EmitsAttributes(t *testing.T) {
	t.Parallel()

	const clusterDV uint32 = 5
	const controllerCachedDV uint32 = 4 // controller has an older version

	d := &dataVersionDispatcher{
		readVal:     AttributeValue{Value: true},
		readStat:    StatusSuccess,
		dataVersion: clusterDV,
	}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 0, HasEndpoint: true, Cluster: 0x001F, HasCluster: true, Attribute: 0x0000, HasAttribute: true},
		},
		DataVersionFilters: []DataVersionFilter{
			{Endpoint: 0, Cluster: 0x001F, DataVersion: controllerCachedDV},
		},
	}

	rd := HandleReadRequest(context.Background(), d, req)
	if len(rd.Reports) != 1 {
		t.Fatalf("reports=%d, want 1 (version mismatch → emit data)", len(rd.Reports))
	}
	if rd.Reports[0].DataVersion != clusterDV {
		t.Errorf("AttributeReport.DataVersion = %d, want %d", rd.Reports[0].DataVersion, clusterDV)
	}
}

// TestMatchDataVersionFilter covers the exported helper directly.
func TestMatchDataVersionFilter(t *testing.T) {
	t.Parallel()

	filters := []DataVersionFilter{
		{Endpoint: 0, Cluster: 0x001F, DataVersion: 7},
		{Endpoint: 1, Cluster: 0x0006, DataVersion: 12},
	}

	cases := []struct {
		ep      uint16
		cluster uint32
		wantV   uint32
		wantOK  bool
	}{
		{0, 0x001F, 7, true},
		{1, 0x0006, 12, true},
		{2, 0x0006, 0, false}, // endpoint mismatch
		{1, 0x001F, 0, false}, // cluster mismatch
	}

	for _, c := range cases {
		v, ok := MatchDataVersionFilter(filters, c.ep, c.cluster)
		if ok != c.wantOK {
			t.Errorf("MatchDataVersionFilter(ep=%d cl=0x%04X): ok=%v, want %v", c.ep, c.cluster, ok, c.wantOK)
		}
		if v != c.wantV {
			t.Errorf("MatchDataVersionFilter(ep=%d cl=0x%04X): v=%d, want %d", c.ep, c.cluster, v, c.wantV)
		}
	}
}

// verifyAttributeDataIBDataVersion walks a TLV-encoded ReportDataMessage
// and asserts that every AttributeDataIB carries a context-tag-0 uint
// with the expected DataVersion value.
func verifyAttributeDataIBDataVersion(t *testing.T, wire []byte, wantDV uint32) {
	t.Helper()
	dec := tlv.NewDecoder(wire)
	type frame struct{ inAttrDataIB bool }
	var stack []frame
	found := false

	for {
		el, err := dec.Next()
		if err != nil {
			break
		}
		if el.IsEndContainer {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if len(stack) > 0 && stack[len(stack)-1].inAttrDataIB {
			if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == uint32(tagAttributeDataDataVersion) {
				found = true
				gotDV := uint32(el.Uint) //nolint:gosec // G115: spec-bound DataVersion is uint32
				if gotDV != wantDV {
					t.Errorf("AttributeDataIB DataVersion on wire = %d, want %d", gotDV, wantDV)
				}
			}
		}
		if el.IsContainer {
			isAttrDataIB := el.Tag.Kind == tlv.TagKindContext &&
				el.Tag.Number == uint32(tagAttributeReportData) &&
				el.Type == tlv.TypeStructure
			stack = append(stack, frame{inAttrDataIB: isAttrDataIB})
		}
	}
	if !found {
		t.Error("AttributeDataIB missing DataVersion (context tag 0) in wire encoding")
	}
}
