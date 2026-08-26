// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"fmt"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// commandFieldsReader is the [im.CommandFieldsReader] the bridge plugs
// into [im.UnmarshalInvokeRequestTLV]. It dispatches on
// (path.Cluster, path.Command) and returns the cluster-native fields
// struct expected by the matching cluster server.
//
// Unrecognised commands return (nil, nil) — the IM layer threads nil
// through to the cluster server, and the server is responsible for
// rejecting per-command. Decode failures wrap with the cluster /
// command identifier so operator triage stays specific.
//
// Coverage matches what chip-tool sends during commissioning + the
// post-commissioning command set on the bridge's root endpoint.
// Add a switch case when wiring a new cluster command that carries
// fields (status-only commands need nothing here).
func commandFieldsReader(path im.ConcreteCommandPath, dec *tlv.Decoder, _ tlv.Element) (any, error) {
	switch path.Cluster {
	case 0x0030: // GeneralCommissioning
		switch path.Command {
		case 0x00:
			return decodeArmFailSafeRequest(dec)
		case 0x02:
			return decodeSetRegulatoryConfigRequest(dec)
		case 0x04:
			return decodeCommissioningCompleteRequest(dec)
		}
	case 0x003E: // OperationalCredentials
		switch path.Command {
		case 0x00:
			return decodeAttestationRequest(dec)
		case 0x02:
			return decodeCertificateChainRequest(dec)
		case 0x04:
			return decodeCSRRequest(dec)
		case 0x06:
			return decodeAddNOCRequest(dec)
		case 0x07:
			return decodeUpdateNOCRequest(dec)
		case 0x09:
			return decodeUpdateFabricLabelRequest(dec)
		case 0x0A:
			return decodeRemoveFabricRequest(dec)
		case 0x0B:
			return decodeAddTrustedRootCertificateRequest(dec)
		}
	case 0x0008: // LevelControl
		switch path.Command {
		case 0x00, 0x04: // MoveToLevel, MoveToLevelWithOnOff
			return decodeMoveToLevelRequest(dec)
		}
	case 0x0300: // ColorControl
		switch path.Command {
		case wire.ColorCtrlCmdMoveToHue:
			return decodeMoveToHueFields(dec)
		case wire.ColorCtrlCmdMoveToSaturation:
			return decodeMoveToSaturationFields(dec)
		case wire.ColorCtrlCmdMoveToHueAndSaturation:
			return decodeMoveToHueAndSaturationFields(dec)
		case wire.ColorCtrlCmdMoveToColorTemperature:
			return decodeMoveToColorTemperatureFields(dec)
		}
	}
	// Unknown command-path: salvage as a tag-keyed map[uint8]any so the
	// cluster server gets the field payload regardless of whether the
	// bridge has a typed decoder yet. Without this every Apple-driven
	// invoke on a non-commissioning cluster reaches MatterInvoke with
	// fields=nil — silently breaking dimming (LevelControl.MoveToLevel),
	// blinds (WindowCovering.GoToLiftPercentage), color (ColorControl.*),
	// thermostat setpoint, and lock-with-PIN commands.
	return decodeGenericTagMap(dec)
}

// decodeMoveToLevelRequest reads LevelControl.MoveToLevel /
// MoveToLevelWithOnOff fields (Matter §1.6.7.1). Tags: [0] uint8
// Level, [1] uint16 TransitionTime (nullable), [2] bitmap8
// OptionsMask, [3] bitmap8 OptionsOverride. The Options bitmaps must
// reach the cluster server: matter.js gates the non-WithOnOff variant
// on the effective ExecuteIfOff option (LevelControlServer.ts:596
// #optionsAllowExecution), so the server needs the full
// [wire.MoveToLevelRequest], not the bare Level byte.
func decodeMoveToLevelRequest(dec *tlv.Decoder) (wire.MoveToLevelRequest, error) {
	var req wire.MoveToLevelRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("MoveToLevel: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			if el.Uint > 0xFF {
				return req, fmt.Errorf("MoveToLevel: Level %d > uint8 max", el.Uint)
			}
			req.Level = uint8(el.Uint & 0xFF)
		case 1:
			if !el.IsNull {
				v := uint16(el.Uint & 0xFFFF)
				req.TransitionTime = &v
			}
		case 2:
			req.OptionsMask = uint8(el.Uint & 0xFF)
		case 3:
			req.OptionsOverride = uint8(el.Uint & 0xFF)
		}
	}
}

// decodeMoveToHueFields reads ColorControl.MoveToHue fields (Matter §3.2.7.4).
// Tags: [0] uint8 Hue, [1] enum8 Direction, [2] uint16 TransitionTime.
func decodeMoveToHueFields(dec *tlv.Decoder) (wire.MoveToHueRequest, error) {
	var req wire.MoveToHueRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("MoveToHue: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.Hue = uint8(el.Uint & 0xFF)
		case 1:
			req.Direction = uint8(el.Uint & 0xFF)
		case 2:
			req.TransitionTime = uint16(el.Uint & 0xFFFF)
		}
	}
}

// decodeMoveToSaturationFields reads ColorControl.MoveToSaturation fields
// (Matter §3.2.7.7). Tags: [0] uint8 Saturation, [1] uint16 TransitionTime.
func decodeMoveToSaturationFields(dec *tlv.Decoder) (wire.MoveToSaturationRequest, error) {
	var req wire.MoveToSaturationRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("MoveToSaturation: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.Saturation = uint8(el.Uint & 0xFF)
		case 1:
			req.TransitionTime = uint16(el.Uint & 0xFFFF)
		}
	}
}

// decodeMoveToHueAndSaturationFields reads ColorControl.MoveToHueAndSaturation
// fields (Matter §3.2.7.10). Tags: [0] uint8 Hue, [1] uint8 Saturation,
// [2] uint16 TransitionTime.
func decodeMoveToHueAndSaturationFields(dec *tlv.Decoder) (wire.MoveToHueAndSaturationRequest, error) {
	var req wire.MoveToHueAndSaturationRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("MoveToHueAndSaturation: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.Hue = uint8(el.Uint & 0xFF)
		case 1:
			req.Saturation = uint8(el.Uint & 0xFF)
		case 2:
			req.TransitionTime = uint16(el.Uint & 0xFFFF)
		}
	}
}

// decodeMoveToColorTemperatureFields reads ColorControl.MoveToColorTemperature
// fields (Matter §3.2.7.21). Tags: [0] uint16 ColorTemperatureMireds,
// [1] uint16 TransitionTime.
func decodeMoveToColorTemperatureFields(dec *tlv.Decoder) (wire.MoveToColorTemperatureRequest, error) {
	var req wire.MoveToColorTemperatureRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("MoveToColorTemperature: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.ColorTemperatureMireds = uint16(el.Uint & 0xFFFF)
		case 1:
			req.TransitionTime = uint16(el.Uint & 0xFFFF)
		}
	}
}

// decodeGenericTagMap drains the current container into a
// map[uint8]any keyed by context-tag number, preserving the field
// payload for cluster servers that don't yet have a typed decoder.
// Skips nested containers (drains them) — typed decoders cover the
// nested-struct cases. Returns nil-map when the container is empty.
func decodeGenericTagMap(dec *tlv.Decoder) (map[uint8]any, error) {
	var out map[uint8]any
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, fmt.Errorf("generic fields: %w", err)
		}
		if el.IsEndContainer {
			return out, nil
		}
		if el.IsContainer {
			if err := drainContainer(dec); err != nil {
				return nil, fmt.Errorf("drain nested container: %w", err)
			}
			continue
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if out == nil {
			out = make(map[uint8]any, 4)
		}
		tag := uint8(el.Tag.Number & 0xFF)
		switch {
		case el.Type >= tlv.TypeUnsignedInt1 && el.Type <= tlv.TypeUnsignedInt8:
			out[tag] = el.Uint
		case el.Type >= tlv.TypeSignedInt1 && el.Type <= tlv.TypeSignedInt8:
			out[tag] = el.Int
		case el.Type == tlv.TypeBoolFalse || el.Type == tlv.TypeBoolTrue:
			out[tag] = el.Bool
		case el.Type == tlv.TypeFloat4 || el.Type == tlv.TypeFloat8:
			out[tag] = el.Float
		case el.Type >= tlv.TypeUTF8Str1 && el.Type <= tlv.TypeUTF8Str8:
			out[tag] = el.String
		case el.IsNull:
			out[tag] = nil
		}
		if el.Type >= tlv.TypeOctetStr1 && el.Type <= tlv.TypeOctetStr8 {
			out[tag] = append([]byte(nil), el.Octets...)
		}
	}
}

// decodeArmFailSafeRequest reads the GeneralCommissioning ArmFailSafe
// command fields per Matter §11.10.6.2. Tags: [0] uint16
// ExpiryLengthSeconds, [1] uint64 Breadcrumb.
func decodeArmFailSafeRequest(dec *tlv.Decoder) (mattercore.ArmFailSafeRequest, error) {
	var req mattercore.ArmFailSafeRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("ArmFailSafe: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.ExpiryLengthSeconds = uint16(el.Uint & 0xFFFF)
		case 1:
			req.Breadcrumb = el.Uint
		}
	}
}

// decodeSetRegulatoryConfigRequest reads the SetRegulatoryConfig
// command fields per Matter §11.10.6.4. Tags: [0] enum8
// NewRegulatoryConfig, [1] string CountryCode, [2] uint64 Breadcrumb.
func decodeSetRegulatoryConfigRequest(dec *tlv.Decoder) (mattercore.SetRegulatoryConfigRequest, error) {
	var req mattercore.SetRegulatoryConfigRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("SetRegulatoryConfig: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.NewRegulatoryConfig = uint8(el.Uint & 0xFF)
		case 1:
			// CountryCode is a `TlvUTF8String` (char-string[2] per
			// §11.10.7.4). TLV decoder lands UTF8 in `el.String`,
			// not `el.Octets` — but the old `el.Octets` read had
			// silently dropped every value to "" for an unknown
			// duration. Run 24 with the el.String
			// fix in place showed Apple sending RemoveFabric ~80 s
			// after Subscribe-Initial — pre-fix Run 23 reached
			// `Completed rebuilding HAP services`. The CountryCode
			// fix is therefore reverted until the SetRegulatoryConfig
			// handler is verified to accept non-empty values without
			// regressing the pair flow. Reactivate after handler audit.
			req.CountryCode = string(el.Octets)
			_ = el.String // explicit unused — see comment above
		case 2:
			req.Breadcrumb = el.Uint
		}
	}
}

// decodeCommissioningCompleteRequest reads the CommissioningComplete
// fields. v1.5.1 spec defines no fields for the command — the
// container is empty. This decoder still consumes the EndContainer
// so the surrounding loop continues correctly. Returns nil for the
// fields struct (path-only Invoke is the contract for empty-fields
// commands).
func decodeCommissioningCompleteRequest(dec *tlv.Decoder) (any, error) {
	return nil, drainContainer(dec)
}

// decodeAttestationRequest reads OperationalCredentials AttestationRequest
// (Matter §11.18.7.1). Tag [0] = AttestationNonce (32-byte octets).
func decodeAttestationRequest(dec *tlv.Decoder) (mattercore.AttestationRequest, error) {
	var req mattercore.AttestationRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("AttestationRequest: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if uint8(el.Tag.Number&0xFF) == 0 {
			req.AttestationNonce = append([]byte(nil), el.Octets...)
		}
	}
}

// decodeCertificateChainRequest reads OperationalCredentials
// CertificateChainRequest (Matter §11.18.7.3). Tag [0] = CertificateType (uint8).
func decodeCertificateChainRequest(dec *tlv.Decoder) (mattercore.CertificateChainRequest, error) {
	var req mattercore.CertificateChainRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("CertificateChainRequest: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if uint8(el.Tag.Number&0xFF) == 0 {
			req.CertificateType = uint8(el.Uint & 0xFF)
		}
	}
}

// decodeCSRRequest reads OperationalCredentials CSRRequest (§11.18.7.5).
// Tags: [0] CSRNonce (32 bytes), [1] IsForUpdateNOC (bool, optional).
func decodeCSRRequest(dec *tlv.Decoder) (mattercore.CSRRequest, error) {
	var req mattercore.CSRRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("CSRRequest: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.CSRNonce = append([]byte(nil), el.Octets...)
		case 1:
			req.IsForUpdateNOC = el.Bool
		}
	}
}

// decodeAddNOCRequest reads OperationalCredentials AddNOC (§11.18.7.7).
// Tags: [0] NOCValue, [1] ICACValue, [2] IPKValue (16B),
// [3] CaseAdminSubject (uint64), [4] AdminVendorID (uint16).
func decodeAddNOCRequest(dec *tlv.Decoder) (mattercore.AddNOCRequest, error) {
	var req mattercore.AddNOCRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("AddNOC: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.NOCValue = append([]byte(nil), el.Octets...)
		case 1:
			req.ICACValue = append([]byte(nil), el.Octets...)
		case 2:
			req.IPKValue = append([]byte(nil), el.Octets...)
		case 3:
			req.CaseAdminSubject = el.Uint
		case 4:
			req.AdminVendorID = uint16(el.Uint & 0xFFFF)
		}
	}
}

// decodeUpdateNOCRequest reads OperationalCredentials UpdateNOC (§11.18.7.8).
// Tags: [0] NOCValue, [1] ICACValue.
func decodeUpdateNOCRequest(dec *tlv.Decoder) (mattercore.UpdateNOCRequest, error) {
	var req mattercore.UpdateNOCRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("UpdateNOC: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0:
			req.NOCValue = append([]byte(nil), el.Octets...)
		case 1:
			req.ICACValue = append([]byte(nil), el.Octets...)
		}
	}
}

// decodeUpdateFabricLabelRequest reads UpdateFabricLabel (§11.18.7.10).
// Tag [0] = Label (UTF8 string).
//
// The field is `TlvUTF8String` per the spec — chip's TLV reader lands
// the decoded value in `el.String`, not `el.Octets` (which is reserved
// for `octstr` types per `tlv/decode.go:182,193`).
// The old code read `el.Octets` and got an empty string back for
// every Apple-Home pair, so we acked `UpdateFabricLabel("")` with
// `NOCStatus=OK` while Apple's HomeKitDaemon had actually sent
// `"Mein Zuhause"`. Apple's subsequent FabricInformation read found
// the label empty and surfaced `HAPErrorDomain Code=1 "Failed to
// update fabric label for accessory"` — the bridge then went to
// `MTRDeviceStateUnreachable` 4 s later.
func decodeUpdateFabricLabelRequest(dec *tlv.Decoder) (mattercore.UpdateFabricLabelRequest, error) {
	var req mattercore.UpdateFabricLabelRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("UpdateFabricLabel: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if uint8(el.Tag.Number&0xFF) == 0 {
			req.Label = el.String
		}
	}
}

// decodeRemoveFabricRequest reads RemoveFabric (§11.18.7.11). Tag [0]
// = FabricIndex (uint8).
func decodeRemoveFabricRequest(dec *tlv.Decoder) (mattercore.RemoveFabricRequest, error) {
	var req mattercore.RemoveFabricRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("RemoveFabric: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if uint8(el.Tag.Number&0xFF) == 0 {
			req.FabricIndex = uint8(el.Uint & 0xFF)
		}
	}
}

// decodeAddTrustedRootCertificateRequest reads AddTrustedRootCertificate
// (§11.18.7.12). Tag [0] = RootCACertificate (octets).
func decodeAddTrustedRootCertificateRequest(dec *tlv.Decoder) (mattercore.AddTrustedRootCertificateRequest, error) {
	var req mattercore.AddTrustedRootCertificateRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("AddTrustedRootCertificate: %w", err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if uint8(el.Tag.Number&0xFF) == 0 {
			req.RootCACertificate = append([]byte(nil), el.Octets...)
		}
	}
}

// rewriteInvokeResponseCommand updates ent.Path.Command from the
// request command ID to the response command ID per Matter §10.6.7
// — chip-tool's TypedCommandCallback decodes the response payload by
// looking up the path's command ID in its response-type table, so
// echoing the request command ID makes it surface
// `CHIP_ERROR_SCHEMA_MISMATCH` even on a structurally valid payload.
//
// Only applied when ent has a Response (status-only entries already
// carry the request command ID by spec).
func rewriteInvokeResponseCommand(ent *im.InvokeResponseEntry) {
	if ent.IsStatus || ent.Response == nil {
		return
	}
	switch ent.Response.(type) {
	case mattercore.ArmFailSafeResponse:
		ent.Path.Command = 0x01
	case mattercore.SetRegulatoryConfigResponse:
		ent.Path.Command = 0x03
	case mattercore.CommissioningCompleteResponse:
		ent.Path.Command = 0x05
	case mattercore.AttestationResponse:
		ent.Path.Command = 0x01
	case mattercore.CertificateChainResponse:
		ent.Path.Command = 0x03
	case mattercore.CSRResponse:
		ent.Path.Command = 0x05
	case mattercore.NOCResponse:
		ent.Path.Command = 0x08
	}
	// Unknown response types (status-only commands wrapped) leave
	// the path alone — the writer emits an empty struct + the
	// request command ID, which chip-tool tolerates for void
	// responses.
}

// drainContainer consumes elements until the matching EndContainer.
// Caller has already entered the container via dec.Next() returning
// a TLV container element.
func drainContainer(dec *tlv.Decoder) error {
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return err
		}
		if el.IsContainer {
			depth++
		}
		if el.IsEndContainer {
			depth--
		}
	}
	return nil
}
