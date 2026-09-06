// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/cluster/modeselect"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// select_matter.go — the ModeSelect (0x0050) projection of a writable ENUM
// ---------------------------------------------------------------------------

// modeSelectValueList is a CCU VALUE_LIST of the shape this projection has to
// carry: device-specific labels whose position is the wire value. These four
// are the leading OPTICAL_ALARM_SELECTION entries of an HmIP-ASIR, the same
// list internal/model/custom/siren/optical_pattern_test.go declares.
var modeSelectValueList = []string{
	"DISABLE_OPTICAL_SIGNAL",
	"BLINKING_ALTERNATELY_REPEATING",
	"BLINKING_BOTH_REPEATING",
	"DOUBLE_FLASHING_REPEATING",
}

// newModeSelect builds a Select over an ENUM parameter with the given
// operations and VALUE_LIST, plus the writer its southbound sends land in.
func newModeSelect(t *testing.T, ops hmenum.Operations, values []string) (*Select, *stubWriter) {
	t.Helper()
	cfg := baseCfg(hmenum.ParameterOpticalAlarmSelection, hmenum.ParameterTypeEnum, ops)
	cfg.Descriptor.ValueList = values
	w := &stubWriter{}
	cfg.Writer = w
	return NewSelect(cfg), w
}

// modeSelectServer returns the single cluster server a Select projects,
// failing when the projection produced anything else.
func modeSelectServer(t *testing.T, s *Select) interfaces.MatterClusterServer {
	t.Helper()
	servers := s.MatterClusterServers()
	if len(servers) != 1 {
		t.Fatalf("MatterClusterServers() returned %d servers, want exactly 1", len(servers))
	}
	return servers[0]
}

// TestSelectMatter_WritableEnumMountsOneModeSelectServer pins the cluster set
// of the ModeSelect device type: its only mandatory server cluster is
// ModeSelect itself (matter.js mode-select-device.element.ts:12-19), so a
// second server would be a cluster the device type never asked for and a
// missing one leaves the endpoint without the cluster it advertises.
func TestSelectMatter_WritableEnumMountsOneModeSelectServer(t *testing.T) {
	t.Parallel()
	s, _ := newModeSelect(t, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent,
		modeSelectValueList)
	srv := modeSelectServer(t, s)
	if got := srv.MatterClusterID(); got != 0x0050 {
		t.Errorf("MatterClusterID() = 0x%04X, want 0x0050 (ModeSelect)", got)
	}
}

// TestSelectMatter_SupportedModesMirrorTheValueListInOrder pins that each
// VALUE_LIST entry becomes exactly one mode whose Label is the entry and whose
// Mode is its index — the index is the value this data point's type already is,
// and it is what ChangeToMode carries back.
func TestSelectMatter_SupportedModesMirrorTheValueListInOrder(t *testing.T) {
	t.Parallel()
	s, _ := newModeSelect(t, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent,
		modeSelectValueList)
	srv := modeSelectServer(t, s)

	raw, ok := srv.MatterRead(modeselect.AttrSupportedModes)
	if !ok {
		t.Fatal("SupportedModes must answer; it is conformance M")
	}
	modes, isList := raw.([]modeselect.ModeOptionStruct)
	if !isList {
		t.Fatalf("SupportedModes read as %T, want []modeselect.ModeOptionStruct", raw)
	}
	if len(modes) != len(modeSelectValueList) {
		t.Fatalf("SupportedModes has %d entries, want %d (one per VALUE_LIST entry)",
			len(modes), len(modeSelectValueList))
	}
	for i, want := range modeSelectValueList {
		if modes[i].Label != want {
			t.Errorf("mode %d label = %q, want %q", i, modes[i].Label, want)
		}
		if int(modes[i].Mode) != i {
			t.Errorf("mode %d value = %d, want %d (the VALUE_LIST index)", i, modes[i].Mode, i)
		}
	}
}

// decodedModeOption is one ModeOptionStruct read back off the wire.
//
// tagsPresent is what separates "SemanticTags rode as an empty list" from "the
// field was never written": the decoded slice is length 0 either way, so an
// assertion on length alone stays green against an encoding that drops the
// conformance-M field entirely (matter.js mode-select-cluster.element.ts:66).
type decodedModeOption struct {
	label       string
	mode        uint8
	tagsPresent bool
	tagCount    int
}

// supportedModesOnTheWire encodes modes through the bridge's ReportData
// encoder — the path a controller's read actually travels — and decodes the
// SupportedModes array back out of the AttributeDataIB.
func supportedModesOnTheWire(t *testing.T, modes any) []decodedModeOption {
	t.Helper()
	wire, err := matterbridge.EncodeReportData(im.ReportData{
		Reports: []im.AttributeReport{{
			Path: im.ConcreteAttributePath{
				Endpoint: 1, Cluster: modeselect.ClusterID, Attribute: modeselect.AttrSupportedModes,
				HasEndpoint: true, HasCluster: true, HasAttribute: true,
			},
			Value:       im.AttributeValue{Value: modes},
			DataVersion: 1,
		}},
	})
	if err != nil {
		t.Fatalf("EncodeReportData: %v", err)
	}
	if vErr := tlv.Validate(wire); vErr != nil {
		t.Fatalf("tlv.Validate rejected the encoded report: %v\nwire=% X", vErr, wire)
	}
	d := tlv.NewDecoder(wire)
	seekAttributeDataArray(t, d)

	out := []decodedModeOption{}
	for {
		el, nerr := d.Next()
		if nerr != nil {
			t.Fatalf("decoder.Next: %v", nerr)
		}
		if el.IsEndContainer {
			return out
		}
		if el.Type != tlv.TypeStructure {
			t.Fatalf("SupportedModes entry has type 0x%02X, want a structure", el.Type)
		}
		out = append(out, decodeOneModeOption(t, d))
	}
}

// attributeDataValueTag is the AttributeDataIB Data field
// (Matter §10.6.1.4 AttributeDataIB, context tag 2).
const attributeDataValueTag uint32 = 2

// seekAttributeDataArray advances d to just past the AttributeDataIB Data
// element, failing when the value did not encode as an array — the encoder's
// default branch writes TLV null for a shape it does not recognise, which a
// controller reads as a ModeSelect cluster with no modes.
func seekAttributeDataArray(t *testing.T, d *tlv.Decoder) {
	t.Helper()
	for {
		el, err := d.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.Fatal("no AttributeDataIB Data array in the encoded report: SupportedModes did not " +
					"encode as an array")
			}
			t.Fatalf("decoder.Next: %v", err)
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == attributeDataValueTag &&
			el.Type == tlv.TypeArray {
			return
		}
	}
}

// decodeOneModeOption reads the fields of one ModeOptionStruct, the opening
// structure element having already been consumed. tagsPresent stays false
// until the SemanticTags field is actually seen.
func decodeOneModeOption(t *testing.T, d *tlv.Decoder) decodedModeOption {
	t.Helper()
	var opt decodedModeOption
	for {
		el, err := d.Next()
		if err != nil {
			t.Fatalf("decoder.Next: %v", err)
		}
		if el.IsEndContainer {
			return opt
		}
		if el.Tag.Kind != tlv.TagKindContext {
			t.Fatalf("ModeOptionStruct field carries tag kind %v, want a context tag", el.Tag.Kind)
		}
		switch uint8(el.Tag.Number) {
		case modeselect.ModeOptionFieldLabel:
			opt.label = el.String
		case modeselect.ModeOptionFieldMode:
			opt.mode = uint8(el.Uint)
		case modeselect.ModeOptionFieldSemanticTags:
			if el.Type != tlv.TypeArray {
				t.Fatalf("SemanticTags has type 0x%02X, want an array", el.Type)
			}
			opt.tagsPresent = true
			opt.tagCount = countContainerEntries(t, d)
		default:
			t.Fatalf("unexpected ModeOptionStruct field tag %d", el.Tag.Number)
		}
	}
}

// countContainerEntries consumes an already-opened container and reports how
// many top-level elements it held.
func countContainerEntries(t *testing.T, d *tlv.Decoder) int {
	t.Helper()
	n, depth := 0, 0
	for {
		el, err := d.Next()
		if err != nil {
			t.Fatalf("decoder.Next: %v", err)
		}
		if el.IsEndContainer {
			if depth == 0 {
				return n
			}
			depth--
			continue
		}
		if depth == 0 {
			n++
		}
		if el.Type == tlv.TypeStructure || el.Type == tlv.TypeArray || el.Type == tlv.TypeList {
			depth++
		}
	}
}

// TestSelectMatter_SemanticTagsRideAsAnEmptyListNotAnAbsentField pins the
// distinction the CCU forces on us: a VALUE_LIST entry is a label with no
// classification behind it, which is the empty SemanticTags list, never a
// missing field. SemanticTags is conformance M
// (matter.js mode-select-cluster.element.ts:66), so an absent field is a wire
// violation while an empty list is the encoding for "this mode is anonymous".
// The assertion reads the encoded bytes rather than the Go slice, because a
// nil slice and a written-but-empty list are indistinguishable in Go and only
// the wire says which one a controller sees.
func TestSelectMatter_SemanticTagsRideAsAnEmptyListNotAnAbsentField(t *testing.T) {
	t.Parallel()
	s, _ := newModeSelect(t, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent,
		modeSelectValueList)
	srv := modeSelectServer(t, s)
	raw, ok := srv.MatterRead(modeselect.AttrSupportedModes)
	if !ok {
		t.Fatal("SupportedModes must answer; it is conformance M")
	}

	got := supportedModesOnTheWire(t, raw)
	if len(got) != len(modeSelectValueList) {
		t.Fatalf("decoded %d modes, want %d", len(got), len(modeSelectValueList))
	}
	for i, opt := range got {
		if !opt.tagsPresent {
			t.Errorf("mode %d (%q) carries no SemanticTags field; conformance M means it always "+
				"rides, empty when the mode is anonymous", i, opt.label)
			continue
		}
		if opt.tagCount != 0 {
			t.Errorf("mode %d (%q) carries %d semantic tags; a CCU VALUE_LIST entry has no CSA "+
				"classification behind it", i, opt.label, opt.tagCount)
		}
	}
}

// TestSelectMatter_CurrentModeReportsObservedIndexElseDescriptorDefault pins
// the answer given before the first CCU push. CurrentMode is conformance M
// with no X quality (matter.js mode-select-cluster.element.ts:41-42), so there
// is no null to report: the descriptor's own DEFAULT answers when it indexes
// the VALUE_LIST, and index 0 otherwise.
func TestSelectMatter_CurrentModeReportsObservedIndexElseDescriptorDefault(t *testing.T) {
	t.Parallel()
	ops := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent

	t.Run("observed value wins", func(t *testing.T) {
		t.Parallel()
		s, _ := newModeSelect(t, ops, modeSelectValueList)
		s.Descriptor.Default = json.RawMessage("1")
		s.OnEvent(int32(3))
		if got := currentMode(t, s); got != 3 {
			t.Errorf("CurrentMode = %d, want 3 (the observed index)", got)
		}
	})

	t.Run("unobserved falls back to an in-range DEFAULT", func(t *testing.T) {
		t.Parallel()
		cfg := baseCfg(hmenum.ParameterOpticalAlarmSelection, hmenum.ParameterTypeEnum, ops)
		cfg.Descriptor.ValueList = modeSelectValueList
		cfg.Descriptor.Default = json.RawMessage("2")
		cfg.Writer = &stubWriter{}
		s := NewSelect(cfg)
		if got := currentMode(t, s); got != 2 {
			t.Errorf("CurrentMode = %d, want 2 (the descriptor DEFAULT)", got)
		}
	})

	t.Run("out-of-range DEFAULT falls back to 0", func(t *testing.T) {
		t.Parallel()
		cfg := baseCfg(hmenum.ParameterOpticalAlarmSelection, hmenum.ParameterTypeEnum, ops)
		cfg.Descriptor.ValueList = modeSelectValueList
		cfg.Descriptor.Default = json.RawMessage("9")
		cfg.Writer = &stubWriter{}
		s := NewSelect(cfg)
		if got := currentMode(t, s); got != 0 {
			t.Errorf("CurrentMode = %d, want 0: DEFAULT 9 does not index a 4-entry VALUE_LIST", got)
		}
	})

	t.Run("no DEFAULT falls back to 0", func(t *testing.T) {
		t.Parallel()
		s, _ := newModeSelect(t, ops, modeSelectValueList)
		if got := currentMode(t, s); got != 0 {
			t.Errorf("CurrentMode = %d, want 0 (the first declared entry)", got)
		}
	})
}

// currentMode reads CurrentMode through the mounted cluster server.
func currentMode(t *testing.T, s *Select) uint8 {
	t.Helper()
	raw, ok := modeSelectServer(t, s).MatterRead(modeselect.AttrCurrentMode)
	if !ok {
		t.Fatal("CurrentMode must answer; it is conformance M")
	}
	mode, isU8 := raw.(uint8)
	if !isU8 {
		t.Fatalf("CurrentMode read as %T, want uint8", raw)
	}
	return mode
}

// TestSelectMatter_StandardNamespaceIsNull pins that the projection claims no
// CSA namespace. A CCU VALUE_LIST is a list of device-specific strings and
// carries no classification a controller could act on without reading the
// label; the attribute is quality X, default null
// (matter.js mode-select-cluster.element.ts:29-32) for exactly that case, so
// naming a namespace would assert a meaning the CCU never supplied.
func TestSelectMatter_StandardNamespaceIsNull(t *testing.T) {
	t.Parallel()
	s, _ := newModeSelect(t, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent,
		modeSelectValueList)
	raw, ok := modeSelectServer(t, s).MatterRead(modeselect.AttrStandardNamespace)
	if !ok {
		t.Fatal("StandardNamespace must answer: null is a value, not a missing attribute")
	}
	if raw != nil {
		t.Errorf("StandardNamespace = %v (%T), want nil (TLV null)", raw, raw)
	}
}

// TestSelectMatter_ChangeToModeWritesTheIndexAndRefusesAnUnknownMode pins both
// halves of the command: a mode that addresses a VALUE_LIST entry reaches the
// data point with the index itself on the wire — a Matter mode IS an index
// (matter.js mode-select-cluster.element.ts:63-68 separates Mode from Label) —
// and a mode outside the list is refused before anything is sent, so a
// controller's bad index never becomes a CCU write.
func TestSelectMatter_ChangeToModeWritesTheIndexAndRefusesAnUnknownMode(t *testing.T) {
	t.Parallel()
	ops := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent

	t.Run("valid mode reaches the data point", func(t *testing.T) {
		t.Parallel()
		s, w := newModeSelect(t, ops, modeSelectValueList)
		srv := modeSelectServer(t, s)
		if _, err := srv.MatterInvoke(t.Context(), modeselect.CmdChangeToMode,
			modeselect.ChangeToModeRequest{NewMode: 2}); err != nil {
			t.Fatalf("ChangeToMode(2): %v", err)
		}
		call, sent := w.last()
		if !sent {
			t.Fatal("ChangeToMode(2) sent nothing southbound")
		}
		if call.param != hmenum.ParameterOpticalAlarmSelection {
			t.Errorf("wrote parameter %q, want %q", call.param, hmenum.ParameterOpticalAlarmSelection)
		}
		if call.value != int32(2) {
			t.Errorf("wrote %v (%T), want int32(2) — the VALUE_LIST index", call.value, call.value)
		}
	})

	t.Run("out-of-range mode writes nothing", func(t *testing.T) {
		t.Parallel()
		s, w := newModeSelect(t, ops, modeSelectValueList)
		if err := s.ChangeToMode(t.Context(), 9); !errors.Is(err, ErrIndexOutOfBounds) {
			t.Errorf("ChangeToMode(9) = %v, want ErrIndexOutOfBounds", err)
		}
		if call, sent := w.last(); sent {
			t.Errorf("ChangeToMode(9) sent %v southbound; an unknown mode must never reach the CCU",
				call.value)
		}
	})

	t.Run("out-of-range mode is refused at the cluster too", func(t *testing.T) {
		t.Parallel()
		s, w := newModeSelect(t, ops, modeSelectValueList)
		srv := modeSelectServer(t, s)
		if _, err := srv.MatterInvoke(t.Context(), modeselect.CmdChangeToMode,
			modeselect.ChangeToModeRequest{NewMode: 9}); err == nil {
			t.Error("ChangeToMode(9) through the cluster: want an error, got nil")
		}
		if call, sent := w.last(); sent {
			t.Errorf("ChangeToMode(9) sent %v southbound; an unknown mode must never reach the CCU",
				call.value)
		}
	})
}

// TestSelectMatter_IneligibleDataPointsMountNoServer is the negative control
// for every assertion above: without the eligibility gate the projection would
// attach a mode chooser to parameters that cannot back it — a read-only ENUM,
// where ChangeToMode (conformance M) would always fail, and a parameter with no
// VALUE_LIST, which reaches a controller as a chooser with nothing to choose.
func TestSelectMatter_IneligibleDataPointsMountNoServer(t *testing.T) {
	t.Parallel()

	t.Run("not writable", func(t *testing.T) {
		t.Parallel()
		s, _ := newModeSelect(t, hmenum.OperationsRead|hmenum.OperationsEvent, modeSelectValueList)
		if servers := s.MatterClusterServers(); servers != nil {
			t.Errorf("a read-only ENUM mounted %d servers, want none", len(servers))
		}
	})

	t.Run("empty value list", func(t *testing.T) {
		t.Parallel()
		s, _ := newModeSelect(t, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent, nil)
		if servers := s.MatterClusterServers(); servers != nil {
			t.Errorf("an ENUM with no VALUE_LIST mounted %d servers, want none", len(servers))
		}
	})

	t.Run("neither readable nor an event source", func(t *testing.T) {
		t.Parallel()
		s, _ := newModeSelect(t, hmenum.OperationsWrite, modeSelectValueList)
		if servers := s.MatterClusterServers(); servers != nil {
			t.Errorf("a write-only ENUM mounted %d servers, want none", len(servers))
		}
	})

	t.Run("nil select", func(t *testing.T) {
		t.Parallel()
		var s *Select
		if servers := s.MatterClusterServers(); servers != nil {
			t.Errorf("a nil Select mounted %d servers, want none", len(servers))
		}
	})
}
