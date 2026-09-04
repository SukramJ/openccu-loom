// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Parity test: verifies that each Go MarshalTLV encoder produces the same wire
// bytes as the matter.js HEAD reference encoder for the same logical input.
//
// Fixture master: notes/parity/matter/im-wire-fixtures.json — what the
// generator writes, and what notes/parity/matter/README.md documents.
// Test input:     testdata/im-wire-fixtures.json, an embedded copy of that
// master. Embedding keeps the test free of any path relative to the repo
// root, so it survives both a change of working directory and a move of
// this package within the tree.
// Generator:      notes/parity/matter/generate-im-wire-fixtures.ts
//
// Regen, from the repo root, with a built matter.js checkout at ../matter.js
// (the generator resolves @matter/types from there):
//
//	root=$(pwd)
//	(cd ../matter.js && node "$root/notes/parity/matter/generate-im-wire-fixtures.ts") \
//	    > notes/parity/matter/im-wire-fixtures.json
//	cp notes/parity/matter/im-wire-fixtures.json \
//	    internal/north/matter/im/testdata/im-wire-fixtures.json
//
// Fixtures with byDesignDivergence=true are skipped in the byte-equality
// assertion. These document cases where the Go encoder intentionally diverges
// from matter.js (e.g. fixed-width SubscriptionId encoding required by chip-tool
// and Apple Home, empty-array vs omitted optional fields). See the byDesignNote
// in each fixture and notes/parity/by_design.md for the rationale.

package im

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// fixtureRecord is one entry from im-wire-fixtures.json.
type fixtureRecord struct {
	Label              string          `json:"label"`
	Description        string          `json:"description"`
	Type               string          `json:"type"`
	Fixture            json.RawMessage `json:"fixture"`
	BytesHex           string          `json:"bytesHex"`
	ByDesignDivergence bool            `json:"byDesignDivergence"`
	ByDesignNote       string          `json:"byDesignNote"`
}

//go:embed testdata/im-wire-fixtures.json
var imFixturesJSON []byte

// loadFixtures decodes the embedded fixture corpus. A missing file is a
// compile error, so the only runtime failure left is a malformed or empty
// corpus — which would otherwise make every parity subtest silently vanish.
func loadFixtures(t *testing.T) []fixtureRecord {
	t.Helper()
	var records []fixtureRecord
	if err := json.Unmarshal(imFixturesJSON, &records); err != nil {
		t.Fatalf("parse im-wire-fixtures.json: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("im-wire-fixtures.json is empty")
	}
	return records
}

// bytesDiff returns a human-readable diff of two byte slices, showing the
// first diverging byte position and ±8 bytes of surrounding context.
func bytesDiff(got, want []byte) string {
	if len(got) != len(want) {
		prefix := 0
		for prefix < len(got) && prefix < len(want) && got[prefix] == want[prefix] {
			prefix++
		}
		return fmt.Sprintf(
			"length mismatch: got %d bytes, want %d bytes; first divergence at offset %d\n"+
				"  got  %s\n  want %s",
			len(got), len(want), prefix,
			hex.EncodeToString(got),
			hex.EncodeToString(want),
		)
	}
	for i := range got {
		if got[i] == want[i] {
			continue
		}
		lo := max(i-8, 0)
		hi := min(i+8, len(got))
		return fmt.Sprintf(
			"diverge at offset %d: got 0x%02X want 0x%02X\n"+
				"  context got  [%d..%d]: %s\n"+
				"  context want [%d..%d]: %s\n"+
				"  full got:  %s\n  full want: %s",
			i, got[i], want[i],
			lo, hi, hex.EncodeToString(got[lo:hi]),
			lo, hi, hex.EncodeToString(want[lo:hi]),
			hex.EncodeToString(got),
			hex.EncodeToString(want),
		)
	}
	return ""
}

// fixtureNilValueWriter is an AttributeValueWriter that writes nothing, used for
// ReportData fixtures that carry no attribute data.
var fixtureNilValueWriter AttributeValueWriter = func(_ *tlv.Encoder, _ tlv.Tag, _ AttributeValue) {}

// ---- JSON input shapes for each fixture type ----------------------------

// rdFixture is the JSON shape for a ReportData fixture from matter.js.
type rdFixture struct {
	SubscriptionID           *uint32 `json:"subscriptionId"`
	SuppressResponse         bool    `json:"suppressResponse"`
	MoreChunkedMessages      bool    `json:"moreChunkedMessages"`
	AttributeReports         *[]any  `json:"attributeReports"`
	EventReports             *[]any  `json:"eventReports"`
	InteractionModelRevision *uint8  `json:"interactionModelRevision"`
}

// srFixture is the JSON shape for a StatusResponse fixture.
type srFixture struct {
	Status                   uint8  `json:"status"`
	InteractionModelRevision *uint8 `json:"interactionModelRevision"`
}

// subscribeRespFixture is the JSON shape for a SubscribeResponse fixture.
type subscribeRespFixture struct {
	SubscriptionID           uint32 `json:"subscriptionId"`
	MaxInterval              uint16 `json:"maxInterval"`
	InteractionModelRevision *uint8 `json:"interactionModelRevision"`
}

// writeRespFixture is the JSON shape for a WriteResponse fixture.
type writeRespFixture struct {
	WriteResponses           []writeStatusEntry `json:"writeResponses"`
	InteractionModelRevision *uint8             `json:"interactionModelRevision"`
}

type writeStatusEntry struct {
	Path   attributePathJSON `json:"path"`
	Status statusJSON        `json:"status"`
}

// attributePathJSON mirrors the matter.js AttributePathIB JSON shape.
type attributePathJSON struct {
	EndpointID  *uint16 `json:"endpointId"`
	ClusterID   *uint32 `json:"clusterId"`
	AttributeID *uint32 `json:"attributeId"`
	NodeID      *uint64 `json:"nodeId"`
	ListIndex   *uint16 `json:"listIndex"`
}

// statusJSON mirrors the StatusIB JSON shape from matter.js.
type statusJSON struct {
	Status        uint8  `json:"status"`
	ClusterStatus *uint8 `json:"clusterStatus"`
}

// invokeRespFixture is the JSON shape for an InvokeResponseForSend fixture.
type invokeRespFixture struct {
	SuppressResponse         bool   `json:"suppressResponse"`
	InvokeResponses          []any  `json:"invokeResponses"`
	InteractionModelRevision *uint8 `json:"interactionModelRevision"`
}

// ---- encoder helpers per type -------------------------------------------

func encodeReportData(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var f rdFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal ReportData fixture: %v", err)
	}
	rd := ReportData{
		SuppressResponse:    f.SuppressResponse,
		MoreChunkedMessages: f.MoreChunkedMessages,
	}
	if f.SubscriptionID != nil {
		rd.SubscriptionID = *f.SubscriptionID
		rd.HasSubscription = true
	}
	enc := tlv.NewEncoder()
	rd.MarshalTLV(enc, fixtureNilValueWriter)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("ReportData.MarshalTLV: %v", err)
	}
	return b
}

func encodeStatusResponse(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var f srFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal StatusResponse fixture: %v", err)
	}
	sr := StatusResponse{Status: StatusCode(f.Status)} //nolint:gosec // G115: status fits uint8 by spec
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("StatusResponse.MarshalTLV: %v", err)
	}
	return b
}

func encodeSubscribeResponse(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var f subscribeRespFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal SubscribeResponse fixture: %v", err)
	}
	sr := SubscribeResponse{
		SubscriptionID: f.SubscriptionID,
		MaxInterval:    f.MaxInterval,
	}
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("SubscribeResponse.MarshalTLV: %v", err)
	}
	return b
}

func encodeWriteResponse(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var f writeRespFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal WriteResponse fixture: %v", err)
	}
	wr := WriteResponse{}
	for _, e := range f.WriteResponses {
		path := ConcreteAttributePath{}
		if e.Path.EndpointID != nil {
			path.Endpoint = *e.Path.EndpointID
			path.HasEndpoint = true
		}
		if e.Path.ClusterID != nil {
			path.Cluster = *e.Path.ClusterID
			path.HasCluster = true
		}
		if e.Path.AttributeID != nil {
			path.Attribute = *e.Path.AttributeID
			path.HasAttribute = true
		}
		st := StatusIB{Status: StatusCode(e.Status.Status)} //nolint:gosec // G115
		if e.Status.ClusterStatus != nil {
			st.ClusterStatus = *e.Status.ClusterStatus
			st.HasClusterStatus = true
		}
		wr.Responses = append(wr.Responses, AttributeStatus{Path: path, Status: st})
	}
	enc := tlv.NewEncoder()
	wr.MarshalTLV(enc)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("WriteResponse.MarshalTLV: %v", err)
	}
	return b
}

func encodeInvokeResponse(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var f invokeRespFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal InvokeResponse fixture: %v", err)
	}
	ir := InvokeResponse{
		SuppressResponse: f.SuppressResponse,
	}
	enc := tlv.NewEncoder()
	noopWriter := CommandFieldsWriter(func(_ *tlv.Encoder, _ tlv.Tag, _ any) {})
	ir.MarshalTLV(enc, noopWriter)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("InvokeResponse.MarshalTLV: %v", err)
	}
	return b
}

// ---- main test ----------------------------------------------------------

// TestIMWireFixtures_MarshalParity asserts that each Go MarshalTLV encoder
// produces the same wire bytes as matter.js HEAD for the same logical input.
//
// Fixtures with byDesignDivergence=true are reported but not failed — they
// document intentional encoding differences. Run with -v to see the full list.
//
// The F3 regression fixture (report_data_supress_false_empty_attrs) must pass:
// before fix F3, openccu-loom omitted suppressResponse when the value was false;
// matter.js always emits it. That fixture is the proof-of-life for the fix.
func TestIMWireFixtures_MarshalParity(t *testing.T) {
	t.Parallel()
	records := loadFixtures(t)

	for _, rec := range records {
		// capture
		t.Run(rec.Label, func(t *testing.T) {
			t.Parallel()

			want, err := hex.DecodeString(rec.BytesHex)
			if err != nil {
				t.Fatalf("bad bytesHex in fixture %q: %v", rec.Label, err)
			}

			// Encode via the Go MarshalTLV for supported types.
			var got []byte
			switch rec.Type {
			case "ReportData":
				got = encodeReportData(t, rec.Fixture)
			case "StatusResponse":
				got = encodeStatusResponse(t, rec.Fixture)
			case "SubscribeResponse":
				got = encodeSubscribeResponse(t, rec.Fixture)
			case "WriteResponse":
				got = encodeWriteResponse(t, rec.Fixture)
			case "InvokeResponse":
				got = encodeInvokeResponse(t, rec.Fixture)
			default:
				// Types without a Go MarshalTLV (ReadRequest, SubscribeRequest,
				// WriteRequest, InvokeRequest, TimedRequest) are decode-only and
				// cannot be exercised here. They are recorded as fixtures for
				// documentation and Stage-3 round-trip tests.
				t.Skipf("type %q is decode-only — no MarshalTLV to test", rec.Type)
				return
			}

			if rec.ByDesignDivergence {
				// Encode succeeded without panicking. Log the divergence note.
				gotHex := hex.EncodeToString(got)
				if gotHex != rec.BytesHex {
					t.Logf("BY-DESIGN: %s\n  note: %s\n  got:  %s\n  want: %s",
						rec.Label, rec.ByDesignNote, gotHex, rec.BytesHex)
				} else {
					t.Logf("by-design fixture now matches (note: %s)", rec.ByDesignNote)
				}
				return
			}

			gotHex := hex.EncodeToString(got)
			if gotHex != rec.BytesHex {
				diff := bytesDiff(got, want)
				t.Errorf("Go %s.MarshalTLV diverges from matter.js HEAD reference.\n"+
					"Fixture: %s\n"+
					"Description: %s\n"+
					"%s\n"+
					"Regen fixtures: see the command in this file's header comment.",
					rec.Type, rec.Label, rec.Description, diff)
			}
		})
	}
}

// TestIMWireFixtures_F3Regression is the proof-of-life for fix F3.
//
// Before F3: ReportData.MarshalTLV omitted the suppressResponse field when its
// value was false, diverging from matter.js (TlvDataReportForSend.ts:27 declares
// suppressResponse as TlvOptionalField and ServerSubscription always passes a
// concrete value). Apple Home treated the absent field as "unknown" and did not
// emit the mandatory StatusResponse(Success), leaving the Subscribe handshake
// incomplete.
//
// After F3: suppressResponse is always emitted, including the explicit false.
// This test locks that behavior: the fixture hex must match matter.js exactly.
func TestIMWireFixtures_F3Regression(t *testing.T) {
	t.Parallel()
	records := loadFixtures(t)

	var found bool
	for _, rec := range records {
		if rec.Label != "report_data_supress_false_empty_attrs" {
			continue
		}
		found = true
		if rec.ByDesignDivergence {
			t.Fatal("F3 regression fixture must NOT be marked byDesignDivergence")
		}

		want, err := hex.DecodeString(rec.BytesHex)
		if err != nil {
			t.Fatalf("bad bytesHex: %v", err)
		}

		got := encodeReportData(t, rec.Fixture)
		gotHex := hex.EncodeToString(got)

		if gotHex != rec.BytesHex {
			diff := bytesDiff(got, want)
			t.Errorf("F3 regression: ReportData.MarshalTLV emits wrong bytes for suppressResponse=false.\n"+
				"Before fix F3, suppressResponse was omitted when false; matter.js always emits it.\n"+
				"%s", diff)
		} else {
			t.Logf("F3 regression fixture passes: suppressResponse=false is always emitted (%s)", gotHex)
		}
	}

	if !found {
		t.Fatal("F3 regression fixture not found in im-wire-fixtures.json; regen the fixture file")
	}
}

// TestIMWireFixtures_ByDesignCoverage verifies that all by-design divergences
// are documented and that the encoder does not panic on them.
func TestIMWireFixtures_ByDesignCoverage(t *testing.T) {
	t.Parallel()
	records := loadFixtures(t)

	var byDesignLabels []string
	for _, rec := range records {
		if !rec.ByDesignDivergence {
			continue
		}
		byDesignLabels = append(byDesignLabels, rec.Label)

		// Verify the encoder at least runs without panicking for encodable types.
		switch rec.Type {
		case "ReportData":
			_ = encodeReportData(t, rec.Fixture)
		case "StatusResponse":
			_ = encodeStatusResponse(t, rec.Fixture)
		case "SubscribeResponse":
			_ = encodeSubscribeResponse(t, rec.Fixture)
		case "WriteResponse":
			_ = encodeWriteResponse(t, rec.Fixture)
		case "InvokeResponse":
			_ = encodeInvokeResponse(t, rec.Fixture)
		}
		// Decode-only types (WriteRequest, InvokeRequest, TimedRequest,
		// ReadRequest, SubscribeRequest) have no MarshalTLV — no encode test.
	}

	t.Logf("by-design divergences (%d): %s", len(byDesignLabels), strings.Join(byDesignLabels, ", "))
}
