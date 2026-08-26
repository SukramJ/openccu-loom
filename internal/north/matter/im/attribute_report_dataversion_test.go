// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

// White-box test for AttributeReport.marshal — lives in package im (not im_test)
// to access unexported tag constants (tagAttributeDataDataVersion,
// tagAttributeReportData) so the assertion can be precise about which TLV
// context tag is being checked.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestAttributeReport_DataVersionPresent is an ADR-0013 "earlier-catch" test
// for Decision #3.
//
// Bug-pattern: AttributeReport.marshal previously emitted AttributeDataIB
// without a DataVersion field (context tag 0). chip-tool's ClusterStateCache
// surfaces CHIP_ERROR_KEY_NOT_FOUND on every read because it expects tag 0 to
// exist unconditionally (Matter §10.6.1.4 makes DataVersion mandatory inside
// AttributeDataIB).
//
// The test encodes a non-status AttributeReport and walks the raw TLV to verify
// that at least one context-tag-0 element exists inside the AttributeDataIB
// struct (i.e. the DataVersion field is actually emitted on the wire).
//
// Regression: if the DataVersion field is removed from marshal(), this test
// fails with "AttributeDataIB missing DataVersion (context tag 0)".
func TestAttributeReport_DataVersionPresent(t *testing.T) {
	t.Parallel()

	// Build a single data-bearing AttributeReport (not a status report).
	// DataVersion=0 is passed deliberately — marshal must default it to 1
	// per the comment in AttributeReport.marshal. The test asserts presence
	// only; the concrete value (1) is secondary.
	rep := AttributeReport{
		Path: ConcreteAttributePath{
			Endpoint:     1,
			HasEndpoint:  true,
			Cluster:      0x0006,
			HasCluster:   true,
			Attribute:    0x0000,
			HasAttribute: true,
		},
		Value:       AttributeValue{Value: true},
		IsStatus:    false,
		DataVersion: 0, // triggers the default-to-1 path in marshal
	}

	// Encode via ReportData.MarshalTLV using a trivial valueWriter.
	rd := ReportData{Reports: []AttributeReport{rep}}
	enc := tlv.NewEncoder()
	rd.MarshalTLV(enc, func(e *tlv.Encoder, tag tlv.Tag, v AttributeValue) {
		e.PutBool(tag, true) // value does not matter for this test
	})
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("ReportData.MarshalTLV: %v", err)
	}

	// Walk the raw TLV stream and locate the DataVersion field.
	//
	// Expected structure (Matter §10.6.6 / §10.6.1.4):
	//   Structure (anonymous)                ← ReportDataMessage
	//     [1] Array AttributeReports
	//       Structure (anonymous)            ← AttributeReportIB
	//         [1] Structure AttributeDataIB  ← tagAttributeReportData == 1
	//           [0] uint DataVersion         ← tagAttributeDataDataVersion == 0  ← MUST exist
	//           [1] List AttributePathIB
	//           [2] value
	//         EndContainer
	//       EndContainer
	//     EndContainer
	//   EndContainer
	dec := tlv.NewDecoder(wire)
	foundDataVersion := false

	// State machine: we track when we're inside an AttributeDataIB struct
	// (opened by a context-tag-1 Structure inside an AttributeReportIB).
	type frame struct {
		inAttrDataIB bool
	}
	var stack []frame

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
		// Check for DataVersion field: context tag 0, uint, inside an
		// AttributeDataIB container.
		if len(stack) > 0 && stack[len(stack)-1].inAttrDataIB {
			if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == uint32(tagAttributeDataDataVersion) {
				foundDataVersion = true
			}
		}
		if el.IsContainer {
			// Determine whether this is an AttributeDataIB opener:
			// context tag == tagAttributeReportData (1) and TypeStructure.
			isAttrDataIB := el.Tag.Kind == tlv.TagKindContext &&
				el.Tag.Number == uint32(tagAttributeReportData) &&
				el.Type == tlv.TypeStructure
			stack = append(stack, frame{inAttrDataIB: isAttrDataIB})
		}
	}

	if !foundDataVersion {
		t.Error("ADR-0013 D#3: AttributeDataIB missing DataVersion (context tag 0); " +
			"chip-tool ClusterStateCache will surface CHIP_ERROR_KEY_NOT_FOUND on every read")
	}
}
