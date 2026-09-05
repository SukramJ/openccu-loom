// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"
)

// TestSubscribeInitial_WireTruth_StrictTLV asserts that every
// Subscribe-Initial ReportData chunk the bridge encoder produces:
//
//  1. Passes [tlv.Validate] strict checks (no context tag at top level,
//     no anonymous tag inside Structure, no non-anonymous tag inside
//     Array). chip's TLVReader rejects whole IM messages on these
//     patterns; without this gate Apple's MTRDevice silent-drops the
//     entire stream with a "could not find cached attribute values"
//     breadcrumb.
//
//  2. Decodes back to the [im.ReportData] schema matter.js's
//     TlvDataReportForSend.ts:27 declares: top-level anonymous Struct
//     with context-tagged fields 0 (SubscriptionID, uint32), 1
//     (AttributeReports, Array of Structs), and 4 (SuppressResponse,
//     bool).
//
// The test is a wire-truth hook for the initial-Subscribe ReportData
// shape. It exercises the encoder + decoder pair against a
// representative ReportData shape; a future iteration can boot a full
// bridge endpoint topology and re-run the same checks against the live
// chunk stream.
func TestSubscribeInitial_WireTruth_StrictTLV(t *testing.T) {
	t.Parallel()

	// Representative Subscribe-Initial chunk: subscriptionID + 3
	// attribute reports across two endpoints. The values are scalar
	// uint16/uint32 so the test does not depend on a cluster-native
	// value writer beyond the IM defaults.
	rd := im.ReportData{
		SubscriptionID:      0x12345678,
		HasSubscription:     true,
		MoreChunkedMessages: false,
		SuppressResponse:    false,
		Reports: []im.AttributeReport{
			{
				Path: im.ConcreteAttributePath{
					Endpoint: 0, Cluster: 0x0028, Attribute: 0x0000,
					HasEndpoint: true, HasCluster: true, HasAttribute: true,
				},
				Value:       im.AttributeValue{Value: uint16(19)},
				DataVersion: 1,
			},
			{
				Path: im.ConcreteAttributePath{
					Endpoint: 1, Cluster: 0x001D, Attribute: 0xFFFD,
					HasEndpoint: true, HasCluster: true, HasAttribute: true,
				},
				Value:       im.AttributeValue{Value: uint16(3)},
				DataVersion: 1,
			},
			{
				Path: im.ConcreteAttributePath{
					Endpoint: 2, Cluster: 0x0006, Attribute: 0x0000,
					HasEndpoint: true, HasCluster: true, HasAttribute: true,
				},
				Value:       im.AttributeValue{Value: true},
				DataVersion: 4,
			},
		},
	}

	wire, err := matterbridge.EncodeReportData(rd)
	if err != nil {
		t.Fatalf("EncodeReportData: %v", err)
	}

	// Strict TLV check.
	if vErr := tlv.Validate(wire); vErr != nil {
		t.Fatalf("tlv.Validate rejected encoded ReportData: %v\nwire=% X", vErr, wire)
	}

	// Walk the TLV structure and verify the matter.js
	// TlvDataReportForSend.ts:27 shape. Tags present:
	//   0 (uint32 SubscriptionID), 1 (Array AttributeReports),
	//   4 (bool SuppressResponse), 0xFF (uint InteractionModelRevision).
	saw := walkTopLevelTags(t, wire)
	if !saw[0] {
		t.Errorf("ReportData missing SubscriptionID (context-tag 0)")
	}
	if !saw[1] {
		t.Errorf("ReportData missing AttributeReports (context-tag 1)")
	}
	if !saw[4] {
		t.Errorf("ReportData missing SuppressResponse (context-tag 4)")
	}
	if !saw[0xFF] {
		t.Errorf("ReportData missing InteractionModelRevision (context-tag 0xFF)")
	}
}

// TestSubscribeInitial_WireTruth_EmptyReports asserts the keepalive
// shape — an empty AttributeReports Array with SuppressResponse=true
// — still satisfies the strict TLV and matter.js TlvDataReport shape.
// matter.js's ServerSubscription emits this on every heartbeat tick
// when no attribute has changed; Apple's MTRDevice MUST accept it.
func TestSubscribeInitial_WireTruth_EmptyReports(t *testing.T) {
	t.Parallel()
	rd := im.ReportData{
		SubscriptionID:   0xAABBCCDD,
		HasSubscription:  true,
		SuppressResponse: true,
		Reports:          nil,
	}
	wire, err := matterbridge.EncodeReportData(rd)
	if err != nil {
		t.Fatalf("EncodeReportData: %v", err)
	}
	if vErr := tlv.Validate(wire); vErr != nil {
		t.Fatalf("tlv.Validate keepalive rejected: %v\nwire=% X", vErr, wire)
	}
	saw := walkTopLevelTags(t, wire)
	if !saw[1] {
		t.Errorf("keepalive ReportData missing AttributeReports array (must be present even when empty)")
	}
}

// TestSubscribeInitial_WireTruth_AttributePathStructure asserts every
// AttributeReportIB inside the Reports array carries the matter.js
// TlvAttributeReport shape: anon Struct with context-tag 1
// (AttributeDataIB), which itself has context-tag 1 (AttributePath
// list) containing endpoint / cluster / attribute scalar tags.
func TestSubscribeInitial_WireTruth_AttributePathStructure(t *testing.T) {
	t.Parallel()
	rd := im.ReportData{
		SubscriptionID:  0x01,
		HasSubscription: true,
		Reports: []im.AttributeReport{
			{
				Path: im.ConcreteAttributePath{
					Endpoint: 1, Cluster: 0x001D, Attribute: 0xFFFB,
					HasEndpoint: true, HasCluster: true, HasAttribute: true,
				},
				Value:       im.AttributeValue{Value: uint16(0)},
				DataVersion: 1,
			},
		},
	}
	wire, err := matterbridge.EncodeReportData(rd)
	if err != nil {
		t.Fatalf("EncodeReportData: %v", err)
	}
	if vErr := tlv.Validate(wire); vErr != nil {
		t.Fatalf("tlv.Validate: %v", vErr)
	}

	// Extract the AttributeReports array body and walk each entry.
	if found, err := findAttributeReportPath(wire); err != nil {
		t.Fatalf("findAttributeReportPath: %v", err)
	} else if !found.endpointPresent || !found.clusterPresent || !found.attributePresent {
		t.Errorf("AttributePath missing one of the three IDs: endpoint=%v cluster=%v attribute=%v",
			found.endpointPresent, found.clusterPresent, found.attributePresent)
	}
}

// walkTopLevelTags decodes the top-level Structure of an encoded
// ReportData and returns a set of context-tag numbers seen at the
// first nesting level. The validator runs first, so the buffer is
// guaranteed well-formed.
func walkTopLevelTags(t *testing.T, wire []byte) map[uint8]bool {
	t.Helper()
	saw := make(map[uint8]bool)
	dec := tlv.NewDecoder(wire)
	first, err := dec.Next()
	if err != nil {
		t.Fatalf("Decode top: %v", err)
	}
	if first.Type != tlv.TypeStructure {
		t.Fatalf("top element type = 0x%02X, want Structure", byte(first.Type))
	}
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Decode: %v", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext {
			saw[uint8(el.Tag.Number)] = true
		}
		if el.Type == tlv.TypeStructure || el.Type == tlv.TypeArray || el.Type == tlv.TypeList {
			depth++
		}
	}
	return saw
}

// findAttributeReport carries the three boolean flags for the
// presence of the endpoint / cluster / attribute scalar tags inside
// the first AttributeDataIB's AttributePath list.
type findAttributeReport struct {
	endpointPresent  bool
	clusterPresent   bool
	attributePresent bool
}

// findAttributeReportPath walks the wire to locate the first
// AttributeReportIB → AttributeDataIB → AttributePath list and
// reports which of endpoint(2)/cluster(3)/attribute(4) tags are
// present. Returns an error when the structure does not match.
func findAttributeReportPath(wire []byte) (findAttributeReport, error) {
	dec := tlv.NewDecoder(wire)
	// Top-level Structure.
	if _, err := dec.Next(); err != nil {
		return findAttributeReport{}, fmt.Errorf("top struct: %w", err)
	}
	// Walk top-level elements until we find context-tag 1 (AttributeReports).
	if err := skipUntilContextTag(dec, 1); err != nil {
		return findAttributeReport{}, fmt.Errorf("attribute reports: %w", err)
	}
	// AttributeReports Array → walk its first element (anon Struct).
	if _, err := dec.Next(); err != nil {
		return findAttributeReport{}, fmt.Errorf("first report struct: %w", err)
	}
	// Inside: walk to context-tag 1 (AttributeDataIB).
	if err := skipUntilContextTag(dec, 1); err != nil {
		return findAttributeReport{}, fmt.Errorf("attribute data: %w", err)
	}
	// AttributeDataIB Struct → walk to context-tag 1 (AttributePath list).
	if err := skipUntilContextTag(dec, 1); err != nil {
		return findAttributeReport{}, fmt.Errorf("attribute path: %w", err)
	}
	// AttributePath list — collect the three scalar tags.
	var out findAttributeReport
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return out, fmt.Errorf("path walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext {
			switch el.Tag.Number {
			case 2:
				out.endpointPresent = true
			case 3:
				out.clusterPresent = true
			case 4:
				out.attributePresent = true
			}
		}
		if el.Type == tlv.TypeStructure || el.Type == tlv.TypeArray || el.Type == tlv.TypeList {
			depth++
		}
	}
	return out, nil
}

// skipUntilContextTag advances dec until it encounters the next
// element at the current depth whose context tag matches target.
// Container elements at the current level are skipped (their depth
// goes up + back down to the same starting depth).
func skipUntilContextTag(dec *tlv.Decoder, target uint32) error {
	for {
		el, err := dec.Next()
		if err != nil {
			return err
		}
		if el.IsEndContainer {
			return errors.New("end-of-container before target tag")
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == target {
			return nil
		}
		// Skip over nested containers at the same depth.
		if el.Type == tlv.TypeStructure || el.Type == tlv.TypeArray || el.Type == tlv.TypeList {
			depth := 1
			for depth > 0 {
				inner, err := dec.Next()
				if err != nil {
					return err
				}
				if inner.IsEndContainer {
					depth--
					continue
				}
				if inner.Type == tlv.TypeStructure || inner.Type == tlv.TypeArray || inner.Type == tlv.TypeList {
					depth++
				}
			}
		}
	}
}

// _ keeps bytes used so the package builds without "imported and not used".
var _ = bytes.Compare
