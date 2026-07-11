// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for commandFieldsReader, the individual decode* functions,
// rewriteInvokeResponseCommand, and drainContainer.
// Lives in package bridge to access unexported functions.

import (
	"testing"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildDecoderAfterStructOpen encodes the given builder inside a struct,
// returns a Decoder positioned AFTER the struct opener (i.e. ready for
// a function that reads fields until EndContainer).
func buildDecoderAfterStructOpen(build func(enc *tlv.Encoder)) *tlv.Decoder {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	build(enc)
	_ = enc.EndContainer()
	raw, err := enc.Bytes()
	if err != nil {
		panic("buildDecoderAfterStructOpen: " + err.Error())
	}
	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // consume struct opener
		panic("buildDecoderAfterStructOpen: consume opener: " + err.Error())
	}
	return dec
}

// ─── drainContainer ───────────────────────────────────────────────────────────

func TestDrainContainer_Empty(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(_ *tlv.Encoder) {}) // empty struct
	if err := drainContainer(dec); err != nil {
		t.Fatalf("drainContainer empty: %v", err)
	}
}

func TestDrainContainer_WithFields(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 99)
		enc.PutBool(tlv.ContextTag(1), true)
	})
	if err := drainContainer(dec); err != nil {
		t.Fatalf("drainContainer with fields: %v", err)
	}
}

func TestDrainContainer_Nested(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.StartArray(tlv.ContextTag(0))
		enc.PutUint(tlv.AnonymousTag(), 1)
		enc.PutUint(tlv.AnonymousTag(), 2)
		_ = enc.EndContainer()
	})
	if err := drainContainer(dec); err != nil {
		t.Fatalf("drainContainer nested: %v", err)
	}
}

// ─── decodeArmFailSafeRequest ─────────────────────────────────────────────────

func TestDecodeArmFailSafeRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 60)   // ExpiryLengthSeconds
		enc.PutUint(tlv.ContextTag(1), 1234) // Breadcrumb
	})
	got, err := decodeArmFailSafeRequest(dec)
	if err != nil {
		t.Fatalf("decodeArmFailSafeRequest: %v", err)
	}
	if got.ExpiryLengthSeconds != 60 {
		t.Errorf("ExpiryLengthSeconds: want 60, got %d", got.ExpiryLengthSeconds)
	}
	if got.Breadcrumb != 1234 {
		t.Errorf("Breadcrumb: want 1234, got %d", got.Breadcrumb)
	}
}

// ─── decodeSetRegulatoryConfigRequest ────────────────────────────────────────

func TestDecodeSetRegulatoryConfigRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 2)    // NewRegulatoryConfig: 2
		enc.PutUTF8(tlv.ContextTag(1), "DE") // CountryCode
		enc.PutUint(tlv.ContextTag(2), 9999) // Breadcrumb
	})
	got, err := decodeSetRegulatoryConfigRequest(dec)
	if err != nil {
		t.Fatalf("decodeSetRegulatoryConfigRequest: %v", err)
	}
	if got.NewRegulatoryConfig != 2 {
		t.Errorf("NewRegulatoryConfig: want 2, got %d", got.NewRegulatoryConfig)
	}
	if got.Breadcrumb != 9999 {
		t.Errorf("Breadcrumb: want 9999, got %d", got.Breadcrumb)
	}
}

// ─── decodeCommissioningCompleteRequest ─────────────────────────────────────

func TestDecodeCommissioningCompleteRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(_ *tlv.Encoder) {}) // empty struct
	v, err := decodeCommissioningCompleteRequest(dec)
	if err != nil {
		t.Fatalf("decodeCommissioningCompleteRequest: %v", err)
	}
	if v != nil {
		t.Errorf("expected nil fields, got %v", v)
	}
}

// ─── decodeAttestationRequest ────────────────────────────────────────────────

func TestDecodeAttestationRequest(t *testing.T) {
	t.Parallel()
	nonce := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutOctets(tlv.ContextTag(0), nonce)
	})
	got, err := decodeAttestationRequest(dec)
	if err != nil {
		t.Fatalf("decodeAttestationRequest: %v", err)
	}
	if len(got.AttestationNonce) != 4 || got.AttestationNonce[0] != 0xDE {
		t.Errorf("AttestationNonce: want %v, got %v", nonce, got.AttestationNonce)
	}
}

// ─── decodeCertificateChainRequest ──────────────────────────────────────────

func TestDecodeCertificateChainRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 1) // CertificateType: 1
	})
	got, err := decodeCertificateChainRequest(dec)
	if err != nil {
		t.Fatalf("decodeCertificateChainRequest: %v", err)
	}
	if got.CertificateType != 1 {
		t.Errorf("CertificateType: want 1, got %d", got.CertificateType)
	}
}

// ─── decodeCSRRequest ────────────────────────────────────────────────────────

func TestDecodeCSRRequest(t *testing.T) {
	t.Parallel()
	csrNonce := []byte{0xAA, 0xBB, 0xCC}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutOctets(tlv.ContextTag(0), csrNonce)
		enc.PutBool(tlv.ContextTag(1), true) // IsForUpdateNOC
	})
	got, err := decodeCSRRequest(dec)
	if err != nil {
		t.Fatalf("decodeCSRRequest: %v", err)
	}
	if len(got.CSRNonce) != 3 || got.CSRNonce[0] != 0xAA {
		t.Errorf("CSRNonce: want %v, got %v", csrNonce, got.CSRNonce)
	}
	if !got.IsForUpdateNOC {
		t.Error("IsForUpdateNOC: want true, got false")
	}
}

// ─── decodeAddNOCRequest ─────────────────────────────────────────────────────

func TestDecodeAddNOCRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutOctets(tlv.ContextTag(0), []byte{0x01}) // NOCValue
		enc.PutOctets(tlv.ContextTag(1), []byte{0x02}) // ICACValue
		enc.PutOctets(tlv.ContextTag(2), []byte{0x03}) // IPKValue
		enc.PutUint(tlv.ContextTag(3), 555)            // CaseAdminSubject
		enc.PutUint(tlv.ContextTag(4), 0xFFF1)         // AdminVendorID
	})
	got, err := decodeAddNOCRequest(dec)
	if err != nil {
		t.Fatalf("decodeAddNOCRequest: %v", err)
	}
	if len(got.NOCValue) != 1 || got.NOCValue[0] != 0x01 {
		t.Errorf("NOCValue unexpected: %v", got.NOCValue)
	}
	if got.CaseAdminSubject != 555 {
		t.Errorf("CaseAdminSubject: want 555, got %d", got.CaseAdminSubject)
	}
	if got.AdminVendorID != 0xFFF1 {
		t.Errorf("AdminVendorID: want 0xFFF1, got 0x%04X", got.AdminVendorID)
	}
}

// ─── decodeUpdateNOCRequest ──────────────────────────────────────────────────

func TestDecodeUpdateNOCRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutOctets(tlv.ContextTag(0), []byte{0x0A}) // NOCValue
		enc.PutOctets(tlv.ContextTag(1), []byte{0x0B}) // ICACValue
	})
	got, err := decodeUpdateNOCRequest(dec)
	if err != nil {
		t.Fatalf("decodeUpdateNOCRequest: %v", err)
	}
	if len(got.NOCValue) != 1 || got.NOCValue[0] != 0x0A {
		t.Errorf("NOCValue: got %v", got.NOCValue)
	}
	if len(got.ICACValue) != 1 || got.ICACValue[0] != 0x0B {
		t.Errorf("ICACValue: got %v", got.ICACValue)
	}
}

// ─── decodeUpdateFabricLabelRequest ──────────────────────────────────────────

func TestDecodeUpdateFabricLabelRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUTF8(tlv.ContextTag(0), "Mein Zuhause")
	})
	got, err := decodeUpdateFabricLabelRequest(dec)
	if err != nil {
		t.Fatalf("decodeUpdateFabricLabelRequest: %v", err)
	}
	if got.Label != "Mein Zuhause" {
		t.Errorf("Label: want %q, got %q", "Mein Zuhause", got.Label)
	}
}

// ─── decodeRemoveFabricRequest ───────────────────────────────────────────────

func TestDecodeRemoveFabricRequest(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 3) // FabricIndex
	})
	got, err := decodeRemoveFabricRequest(dec)
	if err != nil {
		t.Fatalf("decodeRemoveFabricRequest: %v", err)
	}
	if got.FabricIndex != 3 {
		t.Errorf("FabricIndex: want 3, got %d", got.FabricIndex)
	}
}

// ─── decodeAddTrustedRootCertificateRequest ───────────────────────────────────

func TestDecodeAddTrustedRootCertificateRequest(t *testing.T) {
	t.Parallel()
	cert := []byte{0xDE, 0xCA, 0xFB, 0xAD}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutOctets(tlv.ContextTag(0), cert)
	})
	got, err := decodeAddTrustedRootCertificateRequest(dec)
	if err != nil {
		t.Fatalf("decodeAddTrustedRootCertificateRequest: %v", err)
	}
	if len(got.RootCACertificate) != 4 || got.RootCACertificate[0] != 0xDE {
		t.Errorf("RootCACertificate: got %v", got.RootCACertificate)
	}
}

// ─── rewriteInvokeResponseCommand ────────────────────────────────────────────

func TestRewriteInvokeResponseCommand_StatusOnly(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{IsStatus: true}
	ent.Path.Command = 0x00
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x00 {
		t.Errorf("status-only: command should stay 0x00, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_NilResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{IsStatus: false, Response: nil}
	ent.Path.Command = 0x07
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x07 {
		t.Errorf("nil response: command should stay 0x07, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_ArmFailSafeResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.ArmFailSafeResponse{},
	}
	ent.Path.Command = 0x00 // request command ID
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x01 {
		t.Errorf("ArmFailSafeResponse: want 0x01, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_SetRegulatoryConfigResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.SetRegulatoryConfigResponse{},
	}
	ent.Path.Command = 0x02
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x03 {
		t.Errorf("SetRegulatoryConfigResponse: want 0x03, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_CommissioningCompleteResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.CommissioningCompleteResponse{},
	}
	ent.Path.Command = 0x04
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x05 {
		t.Errorf("CommissioningCompleteResponse: want 0x05, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_AttestationResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.AttestationResponse{},
	}
	ent.Path.Command = 0x00
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x01 {
		t.Errorf("AttestationResponse: want 0x01, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_CertificateChainResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.CertificateChainResponse{},
	}
	ent.Path.Command = 0x02
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x03 {
		t.Errorf("CertificateChainResponse: want 0x03, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_CSRResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.CSRResponse{},
	}
	ent.Path.Command = 0x04
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x05 {
		t.Errorf("CSRResponse: want 0x05, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_NOCResponse(t *testing.T) {
	t.Parallel()
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: mattercore.NOCResponse{},
	}
	ent.Path.Command = 0x06
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0x08 {
		t.Errorf("NOCResponse: want 0x08, got 0x%02X", ent.Path.Command)
	}
}

func TestRewriteInvokeResponseCommand_UnknownResponseNoChange(t *testing.T) {
	t.Parallel()
	// An unrecognized response type should leave the command ID unchanged.
	ent := &im.InvokeResponseEntry{
		IsStatus: false,
		Response: struct{ X int }{X: 42},
	}
	ent.Path.Command = 0xFF
	rewriteInvokeResponseCommand(ent)
	if ent.Path.Command != 0xFF {
		t.Errorf("unknown response: want 0xFF, got 0x%02X", ent.Path.Command)
	}
}

// ─── commandFieldsReader ─────────────────────────────────────────────────────

func cmdPath(cluster, cmd uint32) im.ConcreteCommandPath {
	return im.ConcreteCommandPath{Cluster: cluster, Command: cmd}
}

func TestCommandFieldsReader_ArmFailSafe(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 90)
	enc.PutUint(tlv.ContextTag(1), 7)
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x0030, 0x00), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader ArmFailSafe: %v", err)
	}
	req, ok := v.(mattercore.ArmFailSafeRequest)
	if !ok {
		t.Fatalf("expected ArmFailSafeRequest, got %T", v)
	}
	if req.ExpiryLengthSeconds != 90 {
		t.Errorf("ExpiryLengthSeconds: want 90, got %d", req.ExpiryLengthSeconds)
	}
}

func TestCommandFieldsReader_RemoveFabric(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 2) // FabricIndex: 2
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x0A), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader RemoveFabric: %v", err)
	}
	req, ok := v.(mattercore.RemoveFabricRequest)
	if !ok {
		t.Fatalf("expected RemoveFabricRequest, got %T", v)
	}
	if req.FabricIndex != 2 {
		t.Errorf("FabricIndex: want 2, got %d", req.FabricIndex)
	}
}

func TestCommandFieldsReader_UpdateFabricLabel(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUTF8(tlv.ContextTag(0), "Küche")
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x09), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader UpdateFabricLabel: %v", err)
	}
	req, ok := v.(mattercore.UpdateFabricLabelRequest)
	if !ok {
		t.Fatalf("expected UpdateFabricLabelRequest, got %T", v)
	}
	if req.Label != "Küche" {
		t.Errorf("Label: want %q, got %q", "Küche", req.Label)
	}
}

func TestCommandFieldsReader_CommissioningComplete(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x0030, 0x04), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader CommissioningComplete: %v", err)
	}
	if v != nil {
		t.Errorf("expected nil for empty-fields command, got %v", v)
	}
}

// TestCommandFieldsReader_MoveToLevelVariants_AbsentTransitionTime
// verifies that both LevelControl command IDs that dispatch to the
// typed MoveToLevel decoder — MoveToLevel (0x00) and
// MoveToLevelWithOnOff (0x04) — decode a payload whose TransitionTime
// context tag 1 is entirely ABSENT (no TLV Null placeholder) into a
// [wire.MoveToLevelRequest] with TransitionTime nil. Google Home's
// brightness slider sends exactly this shape, so the absent-tag wire
// form is a controller-interop contract for the dispatch path, not a
// decoder corner case. Mirrors matter.js
// packages/types/src/tlv/TlvObject.ts:205 decodeTlvInternalValue and
// packages/node/src/behaviors/level-control/LevelControlServer.ts:297
// moveToLevelLogic (unset transition time falls back to the device
// default).
func TestCommandFieldsReader_MoveToLevelVariants_AbsentTransitionTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command uint32
	}{
		{name: "MoveToLevel", command: 0x00},
		{name: "MoveToLevelWithOnOff", command: 0x04},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enc := tlv.NewEncoder()
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUint(tlv.ContextTag(0), 128) // Level
			// Context tag 1 (TransitionTime) intentionally absent.
			enc.PutUint(tlv.ContextTag(2), 0) // OptionsMask
			enc.PutUint(tlv.ContextTag(3), 0) // OptionsOverride
			_ = enc.EndContainer()
			raw, _ := enc.Bytes()
			dec := tlv.NewDecoder(raw)
			opener, _ := dec.Next()

			v, err := commandFieldsReader(cmdPath(0x0008, tc.command), dec, opener)
			if err != nil {
				t.Fatalf("commandFieldsReader %s: %v", tc.name, err)
			}
			req, ok := v.(wire.MoveToLevelRequest)
			if !ok {
				t.Fatalf("expected wire.MoveToLevelRequest, got %T", v)
			}
			if req.Level != 128 {
				t.Errorf("Level: want 128, got %d", req.Level)
			}
			if req.TransitionTime != nil {
				t.Errorf("TransitionTime: want nil for absent tag, got %v", *req.TransitionTime)
			}
		})
	}
}

func TestCommandFieldsReader_UnknownCommand_GenericTagMap(t *testing.T) {
	t.Parallel()
	// An unknown cluster/command must NOT silently drop its fields —
	// that was the production bug behind HmIP-BDT-style dimmers
	// rejecting Apple's MoveToLevel because commandFieldsReader
	// returned nil for every non-commissioning cluster, including
	// LevelControl. The generic fallback now surfaces fields as a
	// tag-keyed map so the dispatching cluster server has the
	// payload available even before a typed decoder lands.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 123)
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x9999, 0x00), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader unknown: %v", err)
	}
	m, ok := v.(map[uint8]any)
	if !ok {
		t.Fatalf("expected map[uint8]any for unknown command, got %T", v)
	}
	got, ok := m[0].(uint64)
	if !ok || got != 123 {
		t.Errorf("map[0] = %v (%T), want uint64(123)", m[0], m[0])
	}
}

func TestCommandFieldsReader_AddNOC(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(0), []byte{0xAA}) // NOCValue
	enc.PutUint(tlv.ContextTag(4), 0xFFF1)         // AdminVendorID
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x06), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader AddNOC: %v", err)
	}
	req, ok := v.(mattercore.AddNOCRequest)
	if !ok {
		t.Fatalf("expected AddNOCRequest, got %T", v)
	}
	if req.AdminVendorID != 0xFFF1 {
		t.Errorf("AdminVendorID: want 0xFFF1, got 0x%04X", req.AdminVendorID)
	}
}

func TestCommandFieldsReader_UpdateNOC(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(0), []byte{0xBB}) // NOCValue
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x07), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader UpdateNOC: %v", err)
	}
	if _, ok := v.(mattercore.UpdateNOCRequest); !ok {
		t.Fatalf("expected UpdateNOCRequest, got %T", v)
	}
}

func TestCommandFieldsReader_AddTrustedRootCertificate(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(0), []byte{0xCC}) // RootCACertificate
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x0B), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader AddTrustedRootCertificate: %v", err)
	}
	if _, ok := v.(mattercore.AddTrustedRootCertificateRequest); !ok {
		t.Fatalf("expected AddTrustedRootCertificateRequest, got %T", v)
	}
}

func TestCommandFieldsReader_CSR(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(0), []byte{0xDD}) // CSRNonce
	enc.PutBool(tlv.ContextTag(1), false)          // IsForUpdateNOC
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x04), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader CSR: %v", err)
	}
	if _, ok := v.(mattercore.CSRRequest); !ok {
		t.Fatalf("expected CSRRequest, got %T", v)
	}
}

func TestCommandFieldsReader_Attestation(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(0), []byte{0xEE}) // AttestationNonce
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x00), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader Attestation: %v", err)
	}
	if _, ok := v.(mattercore.AttestationRequest); !ok {
		t.Fatalf("expected AttestationRequest, got %T", v)
	}
}

func TestCommandFieldsReader_CertificateChain(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 1) // CertificateType
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x003E, 0x02), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader CertificateChain: %v", err)
	}
	if _, ok := v.(mattercore.CertificateChainRequest); !ok {
		t.Fatalf("expected CertificateChainRequest, got %T", v)
	}
}

func TestCommandFieldsReader_SetRegulatoryConfig(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 1)    // NewRegulatoryConfig
	enc.PutUTF8(tlv.ContextTag(1), "US") // CountryCode
	enc.PutUint(tlv.ContextTag(2), 0)    // Breadcrumb
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	opener, _ := dec.Next()

	v, err := commandFieldsReader(cmdPath(0x0030, 0x02), dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader SetRegulatoryConfig: %v", err)
	}
	if _, ok := v.(mattercore.SetRegulatoryConfigRequest); !ok {
		t.Fatalf("expected SetRegulatoryConfigRequest, got %T", v)
	}
}

// TestDecodeSetRegulatoryConfigRequest_NonContextTagSkipped verifies that a
// non-context-tag element inside SetRegulatoryConfig request is silently skipped.
func TestDecodeSetRegulatoryConfigRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUint(tlv.ContextTag(0), 1)     // NewRegulatoryConfig
	})
	got, err := decodeSetRegulatoryConfigRequest(dec)
	if err != nil {
		t.Fatalf("decodeSetRegulatoryConfigRequest: %v", err)
	}
	if got.NewRegulatoryConfig != 1 {
		t.Errorf("NewRegulatoryConfig: want 1, got %d", got.NewRegulatoryConfig)
	}
}

// TestDecodeAddNOCRequest_NonContextTagSkipped verifies that a non-context-tag
// element inside an AddNOC request is silently skipped.
func TestDecodeAddNOCRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	noc := []byte{0x01}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutOctets(tlv.ContextTag(0), noc)
	})
	got, err := decodeAddNOCRequest(dec)
	if err != nil {
		t.Fatalf("decodeAddNOCRequest: %v", err)
	}
	if len(got.NOCValue) != 1 || got.NOCValue[0] != 0x01 {
		t.Errorf("NOCValue: want [0x01], got %v", got.NOCValue)
	}
}

// ─── non-context-tag continue paths ──────────────────────────────────────────
// Each decode* function has an `if el.Tag.Kind != TagKindContext { continue }`
// guard. The tests below pass an anonymous-tag element first so that guard is
// exercised, then the end-container so the function returns cleanly.

func TestDecodeArmFailSafeRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUint(tlv.ContextTag(0), 30)    // ExpiryLengthSeconds
	})
	got, err := decodeArmFailSafeRequest(dec)
	if err != nil {
		t.Fatalf("decodeArmFailSafeRequest: %v", err)
	}
	if got.ExpiryLengthSeconds != 30 {
		t.Errorf("ExpiryLengthSeconds: want 30, got %d", got.ExpiryLengthSeconds)
	}
}

func TestDecodeAttestationRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	nonce := []byte{0xAA, 0xBB}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutOctets(tlv.ContextTag(0), nonce)
	})
	got, err := decodeAttestationRequest(dec)
	if err != nil {
		t.Fatalf("decodeAttestationRequest: %v", err)
	}
	if len(got.AttestationNonce) != 2 || got.AttestationNonce[0] != 0xAA {
		t.Errorf("AttestationNonce: want %v, got %v", nonce, got.AttestationNonce)
	}
}

func TestDecodeCertificateChainRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUint(tlv.ContextTag(0), 2)     // CertificateType
	})
	got, err := decodeCertificateChainRequest(dec)
	if err != nil {
		t.Fatalf("decodeCertificateChainRequest: %v", err)
	}
	if got.CertificateType != 2 {
		t.Errorf("CertificateType: want 2, got %d", got.CertificateType)
	}
}

func TestDecodeCSRRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	nonce := []byte{0xCC, 0xDD}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutOctets(tlv.ContextTag(0), nonce)
	})
	got, err := decodeCSRRequest(dec)
	if err != nil {
		t.Fatalf("decodeCSRRequest: %v", err)
	}
	if len(got.CSRNonce) != 2 || got.CSRNonce[0] != 0xCC {
		t.Errorf("CSRNonce: want %v, got %v", nonce, got.CSRNonce)
	}
}

func TestDecodeUpdateNOCRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	noc := []byte{0x01, 0x02}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutOctets(tlv.ContextTag(0), noc)
	})
	got, err := decodeUpdateNOCRequest(dec)
	if err != nil {
		t.Fatalf("decodeUpdateNOCRequest: %v", err)
	}
	if len(got.NOCValue) != 2 || got.NOCValue[0] != 0x01 {
		t.Errorf("NOCValue: want %v, got %v", noc, got.NOCValue)
	}
}

func TestDecodeUpdateFabricLabelRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUTF8(tlv.ContextTag(0), "MyFabric")
	})
	got, err := decodeUpdateFabricLabelRequest(dec)
	if err != nil {
		t.Fatalf("decodeUpdateFabricLabelRequest: %v", err)
	}
	if got.Label != "MyFabric" {
		t.Errorf("Label: want MyFabric, got %q", got.Label)
	}
}

func TestDecodeRemoveFabricRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUint(tlv.ContextTag(0), 3)     // FabricIndex
	})
	got, err := decodeRemoveFabricRequest(dec)
	if err != nil {
		t.Fatalf("decodeRemoveFabricRequest: %v", err)
	}
	if got.FabricIndex != 3 {
		t.Errorf("FabricIndex: want 3, got %d", got.FabricIndex)
	}
}

func TestDecodeAddTrustedRootCertificateRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	cert := []byte{0xEE, 0xFF}
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutOctets(tlv.ContextTag(0), cert)
	})
	got, err := decodeAddTrustedRootCertificateRequest(dec)
	if err != nil {
		t.Fatalf("decodeAddTrustedRootCertificateRequest: %v", err)
	}
	if len(got.RootCACertificate) != 2 || got.RootCACertificate[0] != 0xEE {
		t.Errorf("RootCACertificate: want %v, got %v", cert, got.RootCACertificate)
	}
}

// ─── decodeMoveToLevelRequest additional branches ─────────────────────────────

// TestDecodeMoveToLevelRequest_NonContextTagSkipped verifies that an
// anonymous-tag element is skipped and the level is still read correctly.
func TestDecodeMoveToLevelRequest_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUint(tlv.ContextTag(0), 100)   // Level
	})
	req, err := decodeMoveToLevelRequest(dec)
	if err != nil {
		t.Fatalf("decodeMoveToLevelRequest: %v", err)
	}
	if req.Level != 100 {
		t.Errorf("level: want 100, got %d", req.Level)
	}
}

// TestDecodeMoveToLevelRequest_OverflowReturnsError verifies that a Level
// value larger than 0xFF returns an error.
func TestDecodeMoveToLevelRequest_OverflowReturnsError(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 0x100) // Level > uint8 max
	})
	_, err := decodeMoveToLevelRequest(dec)
	if err == nil {
		t.Error("decodeMoveToLevelRequest with Level > 0xFF: want error, got nil")
	}
}

// TestDecodeMoveToLevelRequest_AllFields verifies that every
// MoveToLevelRequest field (Level, TransitionTime, OptionsMask,
// OptionsOverride) is decoded from its context tag. The Options bitmaps
// must reach lightLevelServer.MatterInvoke intact for the ExecuteIfOff
// gate (matter.js LevelControlServer.ts:596), so the decoder needs to
// carry the full struct, not just the bare Level byte.
func TestDecodeMoveToLevelRequest_AllFields(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 200) // Level
		enc.PutUint(tlv.ContextTag(1), 10)  // TransitionTime
		enc.PutUint(tlv.ContextTag(2), 1)   // OptionsMask
		enc.PutUint(tlv.ContextTag(3), 1)   // OptionsOverride
	})
	req, err := decodeMoveToLevelRequest(dec)
	if err != nil {
		t.Fatalf("decodeMoveToLevelRequest: %v", err)
	}
	if req.Level != 200 {
		t.Errorf("Level: want 200, got %d", req.Level)
	}
	if req.TransitionTime == nil || *req.TransitionTime != 10 {
		t.Errorf("TransitionTime: want pointer to 10, got %v", req.TransitionTime)
	}
	if req.OptionsMask != 1 {
		t.Errorf("OptionsMask: want 1, got %d", req.OptionsMask)
	}
	if req.OptionsOverride != 1 {
		t.Errorf("OptionsOverride: want 1, got %d", req.OptionsOverride)
	}
}

// TestDecodeMoveToLevelRequest_NullTransitionTime verifies that a TLV-null
// TransitionTime (the nullable §1.6.7.1 encoding for "use the default
// transition time") decodes to a nil pointer rather than a spurious zero
// value.
func TestDecodeMoveToLevelRequest_NullTransitionTime(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 50) // Level
		enc.PutNull(tlv.ContextTag(1))     // null TransitionTime
	})
	req, err := decodeMoveToLevelRequest(dec)
	if err != nil {
		t.Fatalf("decodeMoveToLevelRequest: %v", err)
	}
	if req.Level != 50 {
		t.Errorf("Level: want 50, got %d", req.Level)
	}
	if req.TransitionTime != nil {
		t.Errorf("TransitionTime: want nil, got %v", *req.TransitionTime)
	}
}

// TestDecodeMoveToLevelRequest_AbsentTransitionTime verifies that a
// payload with the TransitionTime context tag 1 entirely ABSENT (not
// TLV Null) decodes successfully and leaves TransitionTime nil. Google
// Home's brightness slider omits the transitionTime field altogether,
// so the tag-walking decoder's tolerance for a missing tag is
// load-bearing controller interop — a strict "all spec tags present"
// refactor would silently break Google Home dimming. Mirrors matter.js
// packages/types/src/tlv/TlvObject.ts:205 decodeTlvInternalValue
// (structure decode leaves a missing member unset instead of erroring)
// and packages/node/src/behaviors/level-control/LevelControlServer.ts:297
// moveToLevelLogic (`transitionTime ?? onOffTransitionTime ?? null` —
// an unset transition time means "use the device default").
func TestDecodeMoveToLevelRequest_AbsentTransitionTime(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.ContextTag(0), 77) // Level
		// Context tag 1 (TransitionTime) intentionally absent.
		enc.PutUint(tlv.ContextTag(2), 1) // OptionsMask
		enc.PutUint(tlv.ContextTag(3), 0) // OptionsOverride
	})
	req, err := decodeMoveToLevelRequest(dec)
	if err != nil {
		t.Fatalf("decodeMoveToLevelRequest: %v", err)
	}
	if req.Level != 77 {
		t.Errorf("Level: want 77, got %d", req.Level)
	}
	if req.TransitionTime != nil {
		t.Errorf("TransitionTime: want nil for absent tag, got %v", *req.TransitionTime)
	}
	if req.OptionsMask != 1 {
		t.Errorf("OptionsMask: want 1, got %d", req.OptionsMask)
	}
	if req.OptionsOverride != 0 {
		t.Errorf("OptionsOverride: want 0, got %d", req.OptionsOverride)
	}
}

// ─── decodeGenericTagMap type branches ───────────────────────────────────────

// TestDecodeGenericTagMap_AllTypes verifies that all supported TLV types
// (signed int, bool, float, UTF-8 string, null, octet string) are decoded
// into the map with the expected Go type.
func TestDecodeGenericTagMap_AllTypes(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutInt(tlv.ContextTag(0), -7)                    // signed int → int64
		enc.PutBool(tlv.ContextTag(1), true)                 // bool → bool
		enc.PutFloat32(tlv.ContextTag(2), 1.5)               // float → float64
		enc.PutUTF8(tlv.ContextTag(3), "hello")              // UTF-8 → string
		enc.PutNull(tlv.ContextTag(4))                       // null → nil
		enc.PutOctets(tlv.ContextTag(5), []byte{0xAB, 0xCD}) // octets → []byte
	})
	m, err := decodeGenericTagMap(dec)
	if err != nil {
		t.Fatalf("decodeGenericTagMap: %v", err)
	}
	if m == nil {
		t.Fatal("decodeGenericTagMap: returned nil map")
	}

	if v, ok := m[0].(int64); !ok || v != -7 {
		t.Errorf("tag 0 (signed): want int64(-7), got %T(%v)", m[0], m[0])
	}
	if v, ok := m[1].(bool); !ok || !v {
		t.Errorf("tag 1 (bool): want bool(true), got %T(%v)", m[1], m[1])
	}
	if _, ok := m[2].(float64); !ok {
		t.Errorf("tag 2 (float): want float64, got %T", m[2])
	}
	if v, ok := m[3].(string); !ok || v != "hello" {
		t.Errorf("tag 3 (string): want \"hello\", got %T(%v)", m[3], m[3])
	}
	if _, exists := m[4]; !exists {
		t.Error("tag 4 (null): key not present in map")
	}
	if m[4] != nil {
		t.Errorf("tag 4 (null): want nil, got %v", m[4])
	}
	if b, ok := m[5].([]byte); !ok || len(b) != 2 || b[0] != 0xAB {
		t.Errorf("tag 5 (octets): want []byte{0xAB,0xCD}, got %T(%v)", m[5], m[5])
	}
}

// TestDecodeGenericTagMap_NestedContainerDrained verifies that a nested
// container (e.g. an array sub-field) is drained and does not break the
// outer decode loop.
func TestDecodeGenericTagMap_NestedContainerDrained(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		// Nested array that should be drained.
		enc.StartArray(tlv.ContextTag(0))
		enc.PutUint(tlv.AnonymousTag(), 1)
		enc.PutUint(tlv.AnonymousTag(), 2)
		_ = enc.EndContainer()
		// A real field after the nested container.
		enc.PutUint(tlv.ContextTag(1), 42)
	})
	m, err := decodeGenericTagMap(dec)
	if err != nil {
		t.Fatalf("decodeGenericTagMap with nested container: %v", err)
	}
	// The nested container at tag 0 must have been drained (key absent).
	if _, ok := m[0]; ok {
		t.Error("tag 0 (nested container): should not appear in map")
	}
	// The field after the container must be present.
	if v, ok := m[1].(uint64); !ok || v != 42 {
		t.Errorf("tag 1 after nested container: want uint64(42), got %T(%v)", m[1], m[1])
	}
}

// TestDecodeGenericTagMap_NonContextTagSkipped verifies that anonymous-tag
// elements are silently skipped.
func TestDecodeGenericTagMap_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(enc *tlv.Encoder) {
		enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
		enc.PutUint(tlv.ContextTag(0), 7)
	})
	m, err := decodeGenericTagMap(dec)
	if err != nil {
		t.Fatalf("decodeGenericTagMap: %v", err)
	}
	if v, ok := m[0].(uint64); !ok || v != 7 {
		t.Errorf("tag 0: want uint64(7), got %T(%v)", m[0], m[0])
	}
}

// TestDecodeGenericTagMap_Empty verifies that an empty container returns a
// nil map (no allocation for empty command payloads).
func TestDecodeGenericTagMap_Empty(t *testing.T) {
	t.Parallel()
	dec := buildDecoderAfterStructOpen(func(_ *tlv.Encoder) {})
	m, err := decodeGenericTagMap(dec)
	if err != nil {
		t.Fatalf("decodeGenericTagMap empty: %v", err)
	}
	if m != nil {
		t.Errorf("empty container: want nil map, got %v", m)
	}
}

// ─── decode* error paths (dec.Next returns error on truncated TLV) ────────────

// truncatedContextTagDec returns a Decoder positioned at a context-tag
// uint1 element whose value byte is absent, causing dec.Next() to return
// an error. Used to exercise the error branches in every decode* function.
//
// Byte layout: 0x24 (context-tag kind, TypeUnsignedInt1) | 0x00 (tag number)
// — no following value byte, so readValue returns io.EOF.
func truncatedContextTagDec() *tlv.Decoder {
	return tlv.NewDecoder([]byte{0x24, 0x00})
}

func TestDecodeMoveToLevelRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeMoveToLevelRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeMoveToLevelRequest truncated: want error, got nil")
	}
}

func TestDecodeGenericTagMap_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeGenericTagMap(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeGenericTagMap truncated: want error, got nil")
	}
}

func TestDecodeArmFailSafeRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeArmFailSafeRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeArmFailSafeRequest truncated: want error, got nil")
	}
}

func TestDecodeSetRegulatoryConfigRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeSetRegulatoryConfigRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeSetRegulatoryConfigRequest truncated: want error, got nil")
	}
}

func TestDecodeAttestationRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeAttestationRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeAttestationRequest truncated: want error, got nil")
	}
}

func TestDecodeCertificateChainRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeCertificateChainRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeCertificateChainRequest truncated: want error, got nil")
	}
}

func TestDecodeCSRRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeCSRRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeCSRRequest truncated: want error, got nil")
	}
}

func TestDecodeAddNOCRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeAddNOCRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeAddNOCRequest truncated: want error, got nil")
	}
}

func TestDecodeUpdateNOCRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeUpdateNOCRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeUpdateNOCRequest truncated: want error, got nil")
	}
}

func TestDecodeUpdateFabricLabelRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeUpdateFabricLabelRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeUpdateFabricLabelRequest truncated: want error, got nil")
	}
}

func TestDecodeRemoveFabricRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeRemoveFabricRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeRemoveFabricRequest truncated: want error, got nil")
	}
}

func TestDecodeAddTrustedRootCertificateRequest_DecNextError(t *testing.T) {
	t.Parallel()
	_, err := decodeAddTrustedRootCertificateRequest(truncatedContextTagDec())
	if err == nil {
		t.Error("decodeAddTrustedRootCertificateRequest truncated: want error, got nil")
	}
}

// TestDrainContainer_DecNextError verifies that drainContainer propagates a
// decoder error encountered mid-drain.
func TestDrainContainer_DecNextError(t *testing.T) {
	t.Parallel()
	err := drainContainer(truncatedContextTagDec())
	if err == nil {
		t.Error("drainContainer truncated: want error, got nil")
	}
}

// TestDecodeGenericTagMap_NestedContainerDrainError verifies that an error
// from drainContainer (nested container with truncated bytes) is propagated.
func TestDecodeGenericTagMap_NestedContainerDrainError(t *testing.T) {
	t.Parallel()
	// Craft raw TLV bytes that look like:
	//   0x36 0x00  — outer struct opener, context-tag 0 (IsContainer=true)
	//   0x17       — struct opener, anonymous tag, IsContainer=true
	//   (EOF)      — drainContainer will call dec.Next() and get io.EOF
	//
	// TLV control byte breakdown:
	//   0x36 = bits[7:5]=001(Context), bits[4:0]=10110(TypeStruct) → context-tag struct
	//   0x00 = tag number 0
	//   0x17 = bits[7:5]=000(Anonymous), bits[4:0]=10111(TypeStruct) → anon struct open
	//
	// The decoder is already positioned at the outer struct's first element
	// by skipping one element (the outer struct open itself is not present
	// since we give the decoder the content). So the raw bytes below are
	// the content bytes only — what decodeGenericTagMap reads.
	//
	// Simpler encoding: a context-tagged array opener (0x36, 0x00) followed
	// by a truncated interior (no EndContainer, no data) — io.EOF inside drain.
	//   0x36 = TagKindContext(001) | TypeStruct(10110) — struct as IsContainer
	//   0x00 = context tag number 0
	//   (EOF after the opener tag bytes — drainContainer immediately gets EOF)
	raw := []byte{0x36, 0x00}
	dec := tlv.NewDecoder(raw)
	// decodeGenericTagMap calls dec.Next() → reads 0x36 and 0x00 (opens a
	// context-tagged struct, IsContainer=true) → calls drainContainer(dec)
	// → dec.Next() returns io.EOF (no more bytes) → drainContainer returns
	// the error → decodeGenericTagMap wraps and returns it.
	_, err := decodeGenericTagMap(dec)
	if err == nil {
		t.Error("decodeGenericTagMap nested container drain error: want error, got nil")
	}
}
