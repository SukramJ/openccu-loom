// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"fmt"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// attributeValueReader is the [im.AttributeValueReader] the bridge
// plugs into [im.UnmarshalWriteRequestTLV]. It dispatches on
// (path.Cluster, path.Attribute) and converts the TLV element tree
// into the cluster-native Go value the cluster server expects in
// [interfaces.MatterClusterServer.MatterWrite].
//
// Apple Home's post-CommissioningComplete flow writes
// `AccessControl.ACL` (cluster 0x001F, attribute 0x0000) to install
// HomePod / AppleTV edge controllers as additional Administer subjects.
// Without this decoder the IM layer cannot expose the value to the
// AccessControl cluster server, the WriteResponse never lands, Apple
// times out after 10 s and tears the fabric down via RemoveFabric.
//
// Every writable list/struct attribute needs an explicit case here: the
// generic primitiveAttributeValue branch drains a container and yields a
// nil Value, so a structured write that falls through reaches the cluster
// server's MatterWrite as nil, fails its type assertion, and answers
// FAILURE. matter.js does not hand-maintain a switch — its write path
// decodes each value with the attribute's own TlvSchema
// (packages/protocol/src/action/server/AttributeWriteResponse.ts
// #decodeWithSchema), so every list/struct attribute is decodable by
// construction. Each case below mirrors the matching read-direction
// encoder in reply.go so the write path is symmetric with the read path.
//
// Add a switch case here when wiring a new writable cluster attribute
// that carries a structured value (primitives are handled by the
// generic primitiveAttributeValue branch).
func attributeValueReader(path im.ConcreteAttributePath, el tlv.Element, dec *tlv.Decoder) (im.AttributeValue, error) {
	switch {
	case path.Cluster == 0x001F && path.Attribute == 0x0000: // AccessControl.ACL
		return decodeACLList(el, dec)
	case path.Cluster == 0x001F && path.Attribute == 0x0001: // AccessControl.Extension
		return decodeExtensionList(el, dec)
	case path.Cluster == 0x003F && path.Attribute == 0x0000: // GroupKeyManagement.GroupKeyMap
		return decodeGroupKeyMapList(el, dec)
	}
	return primitiveAttributeValue(el, dec)
}

// decodeACLList reads an array of AccessControlEntryStruct (Matter
// §9.10.4.4) from the TLV stream. el is the array opener; the caller
// already consumed it via dec.Next() in [im.readAttributeData].
func decodeACLList(el tlv.Element, dec *tlv.Decoder) (im.AttributeValue, error) {
	if !el.IsContainer || el.Type != tlv.TypeArray {
		return im.AttributeValue{}, fmt.Errorf("AccessControl.ACL: value is not array (type=0x%02X)", el.Type)
	}
	var out []mattercore.AccessControlEntryStruct
	for {
		next, err := dec.Next()
		if err != nil {
			return im.AttributeValue{}, fmt.Errorf("AccessControl.ACL: %w", err)
		}
		if next.IsEndContainer {
			return im.AttributeValue{Value: out}, nil
		}
		if !next.IsContainer || next.Type != tlv.TypeStructure {
			// Skip unexpected elements but keep parsing — Apple emits
			// only structs but we tolerate trailing padding.
			if next.IsContainer {
				if derr := skipContainerTLV(dec); derr != nil {
					return im.AttributeValue{}, derr
				}
			}
			continue
		}
		entry, err := decodeACLEntry(dec)
		if err != nil {
			return im.AttributeValue{}, err
		}
		out = append(out, entry)
	}
}

// decodeACLEntry reads one AccessControlEntryStruct (§9.10.4.4):
//
//	[1]   privilege   enum8
//	[2]   auth-mode   enum8
//	[3]   subjects    nullable list of node-id (uint64)
//	[4]   targets     nullable list of struct{ [0] cluster, [1] endpoint, [2] device-type }
//	[254] fabric-index uint8
//
// Caller has consumed the entry's struct opener.
func decodeACLEntry(dec *tlv.Decoder) (mattercore.AccessControlEntryStruct, error) {
	var e mattercore.AccessControlEntryStruct
	for {
		el, err := dec.Next()
		if err != nil {
			return mattercore.AccessControlEntryStruct{}, fmt.Errorf("ACL entry: %w", err)
		}
		if el.IsEndContainer {
			return e, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 1:
			e.Privilege = uint8(el.Uint & 0xFF)
		case 2:
			e.AuthMode = uint8(el.Uint & 0xFF)
		case 3:
			if el.IsNull {
				e.Subjects = nil
				continue
			}
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return mattercore.AccessControlEntryStruct{}, fmt.Errorf("ACL entry: subjects not array (type=0x%02X)", el.Type)
			}
			subs, err := decodeACLSubjects(dec)
			if err != nil {
				return mattercore.AccessControlEntryStruct{}, err
			}
			e.Subjects = subs
		case 4:
			if el.IsNull {
				e.Targets = nil
				continue
			}
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return mattercore.AccessControlEntryStruct{}, fmt.Errorf("ACL entry: targets not array (type=0x%02X)", el.Type)
			}
			tgts, err := decodeACLTargets(dec)
			if err != nil {
				return mattercore.AccessControlEntryStruct{}, err
			}
			e.Targets = tgts
		case 254:
			e.FabricIndex = uint8(el.Uint & 0xFF)
		default:
			if el.IsContainer {
				if err := skipContainerTLV(dec); err != nil {
					return mattercore.AccessControlEntryStruct{}, err
				}
			}
		}
	}
}

func decodeACLSubjects(dec *tlv.Decoder) ([]uint64, error) {
	var out []uint64
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, fmt.Errorf("ACL subjects: %w", err)
		}
		if el.IsEndContainer {
			return out, nil
		}
		out = append(out, el.Uint)
	}
}

func decodeACLTargets(dec *tlv.Decoder) ([]mattercore.ACLTargetStruct, error) {
	var out []mattercore.ACLTargetStruct
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, fmt.Errorf("ACL targets: %w", err)
		}
		if el.IsEndContainer {
			return out, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeStructure {
			continue
		}
		t, err := decodeACLTarget(dec)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
}

func decodeACLTarget(dec *tlv.Decoder) (mattercore.ACLTargetStruct, error) {
	var t mattercore.ACLTargetStruct
	for {
		el, err := dec.Next()
		if err != nil {
			return mattercore.ACLTargetStruct{}, fmt.Errorf("ACL target: %w", err)
		}
		if el.IsEndContainer {
			return t, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 0:
			if !el.IsNull {
				v := uint32(el.Uint & 0xFFFFFFFF)
				t.Cluster = &v
			}
		case 1:
			if !el.IsNull {
				v := uint16(el.Uint & 0xFFFF)
				t.Endpoint = &v
			}
		case 2:
			if !el.IsNull {
				v := uint32(el.Uint & 0xFFFFFFFF)
				t.DeviceType = &v
			}
		}
	}
}

// decodeGroupKeyMapList reads an array of GroupKeyMapStruct
// (Matter §11.2.10.4.1) from the TLV stream. el is the array opener; the
// caller already consumed it via dec.Next(). Field tags mirror matter.js
// packages/model/src/standard/elements/group-key-management.element.ts:106-111
// (GroupId [1] uint16, GroupKeySetId [2] uint16, FabricIndex [254] uint8)
// and the read-direction encoder in reply.go's []GroupKeyMapStruct case.
// Draining this to nil (the old primitive fall-through) made every
// GroupKeyMap write answer FAILURE, so no group-cast key binding was ever
// established.
func decodeGroupKeyMapList(el tlv.Element, dec *tlv.Decoder) (im.AttributeValue, error) {
	if !el.IsContainer || el.Type != tlv.TypeArray {
		return im.AttributeValue{}, fmt.Errorf("GroupKeyManagement.GroupKeyMap: value is not array (type=0x%02X)", el.Type)
	}
	var out []mattercore.GroupKeyMapStruct
	for {
		next, err := dec.Next()
		if err != nil {
			return im.AttributeValue{}, fmt.Errorf("GroupKeyManagement.GroupKeyMap: %w", err)
		}
		if next.IsEndContainer {
			return im.AttributeValue{Value: out}, nil
		}
		if !next.IsContainer || next.Type != tlv.TypeStructure {
			if next.IsContainer {
				if derr := skipContainerTLV(dec); derr != nil {
					return im.AttributeValue{}, derr
				}
			}
			continue
		}
		entry, err := decodeGroupKeyMapEntry(dec)
		if err != nil {
			return im.AttributeValue{}, err
		}
		out = append(out, entry)
	}
}

// decodeGroupKeyMapEntry reads one GroupKeyMapStruct (§11.2.10.4.1):
//
//	[1]   group-id        uint16
//	[2]   group-key-set-id uint16
//	[254] fabric-index    uint8
//
// Caller has consumed the entry's struct opener.
func decodeGroupKeyMapEntry(dec *tlv.Decoder) (mattercore.GroupKeyMapStruct, error) {
	var e mattercore.GroupKeyMapStruct
	for {
		el, err := dec.Next()
		if err != nil {
			return mattercore.GroupKeyMapStruct{}, fmt.Errorf("GroupKeyMap entry: %w", err)
		}
		if el.IsEndContainer {
			return e, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 1:
			e.GroupID = uint16(el.Uint & 0xFFFF)
		case 2:
			e.GroupKeySetID = uint16(el.Uint & 0xFFFF)
		case 254:
			e.FabricIndex = uint8(el.Uint & 0xFF)
		default:
			if el.IsContainer {
				if err := skipContainerTLV(dec); err != nil {
					return mattercore.GroupKeyMapStruct{}, err
				}
			}
		}
	}
}

// decodeExtensionList reads an array of AccessControlExtensionStruct
// (Matter §9.10.4.6) from the TLV stream. el is the array opener; the
// caller already consumed it via dec.Next(). Field tags mirror matter.js
// packages/model/src/standard/elements/access-control.element.ts:204-208
// (Data [1] octstr<128>, FabricIndex [254] uint8). The cluster server
// re-stamps FabricIndex from the session context on write; it is decoded
// here for symmetry with the read encoder. Draining this to nil (the old
// primitive fall-through) made every Extension write answer FAILURE.
func decodeExtensionList(el tlv.Element, dec *tlv.Decoder) (im.AttributeValue, error) {
	if !el.IsContainer || el.Type != tlv.TypeArray {
		return im.AttributeValue{}, fmt.Errorf("AccessControl.Extension: value is not array (type=0x%02X)", el.Type)
	}
	var out []mattercore.AccessControlExtensionEntry
	for {
		next, err := dec.Next()
		if err != nil {
			return im.AttributeValue{}, fmt.Errorf("AccessControl.Extension: %w", err)
		}
		if next.IsEndContainer {
			return im.AttributeValue{Value: out}, nil
		}
		if !next.IsContainer || next.Type != tlv.TypeStructure {
			if next.IsContainer {
				if derr := skipContainerTLV(dec); derr != nil {
					return im.AttributeValue{}, derr
				}
			}
			continue
		}
		entry, err := decodeExtensionEntry(dec)
		if err != nil {
			return im.AttributeValue{}, err
		}
		out = append(out, entry)
	}
}

// decodeExtensionEntry reads one AccessControlExtensionStruct (§9.10.4.6):
//
//	[1]   data         octstr<128>
//	[254] fabric-index uint8
//
// Caller has consumed the entry's struct opener.
func decodeExtensionEntry(dec *tlv.Decoder) (mattercore.AccessControlExtensionEntry, error) {
	var e mattercore.AccessControlExtensionEntry
	for {
		el, err := dec.Next()
		if err != nil {
			return mattercore.AccessControlExtensionEntry{}, fmt.Errorf("extension entry: %w", err)
		}
		if el.IsEndContainer {
			return e, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 1:
			e.Data = append([]byte(nil), el.Octets...)
		case 254:
			e.FabricIndex = uint8(el.Uint & 0xFF)
		default:
			if el.IsContainer {
				if err := skipContainerTLV(dec); err != nil {
					return mattercore.AccessControlExtensionEntry{}, err
				}
			}
		}
	}
}

// primitiveAttributeValue extracts a primitive Go value from a
// non-container TLV element. Containers are drained — without a
// cluster-aware decoder the bridge has no way to surface the inner
// structure and the cluster server will get a zero-typed value.
func primitiveAttributeValue(el tlv.Element, dec *tlv.Decoder) (im.AttributeValue, error) {
	if el.IsNull {
		return im.AttributeValue{IsNull: true}, nil
	}
	if el.IsContainer {
		if err := skipContainerTLV(dec); err != nil {
			return im.AttributeValue{}, err
		}
		return im.AttributeValue{}, nil
	}
	switch el.Type {
	case tlv.TypeBoolTrue:
		return im.AttributeValue{Value: true}, nil
	case tlv.TypeBoolFalse:
		return im.AttributeValue{Value: false}, nil
	case tlv.TypeUTF8Str1, tlv.TypeUTF8Str2, tlv.TypeUTF8Str4, tlv.TypeUTF8Str8:
		// UTF-8 strings are decoded into el.String (octet strings use
		// el.Octets); reading el.Octets here dropped every UTF-8 attribute
		// write — e.g. a NodeLabel — to the empty string.
		return im.AttributeValue{Value: el.String}, nil
	case tlv.TypeOctetStr1, tlv.TypeOctetStr2, tlv.TypeOctetStr4, tlv.TypeOctetStr8:
		return im.AttributeValue{Value: append([]byte(nil), el.Octets...)}, nil
	case tlv.TypeSignedInt1, tlv.TypeSignedInt2, tlv.TypeSignedInt4, tlv.TypeSignedInt8:
		// Signed integers carry their sign-extended value in el.Int.
		// Returning el.Uint (the old fall-through) dropped every signed
		// attribute write — e.g. a Thermostat setpoint — to 0, because the
		// decoder only fills el.Int for signed types. The cluster server
		// narrows int64 to its native width.
		return im.AttributeValue{Value: el.Int}, nil
	case tlv.TypeFloat4, tlv.TypeFloat8:
		return im.AttributeValue{Value: el.Float}, nil
	default:
		// Unsigned integers / enums surface as uint64; the cluster server
		// narrows to its native width (e.g. enum8 SystemMode → uint8).
		return im.AttributeValue{Value: el.Uint}, nil
	}
}

// skipContainerTLV mirrors im.skipContainer for the bridge package.
// Caller has just consumed a container opener; this drains until the
// matching EndContainer.
func skipContainerTLV(dec *tlv.Decoder) error {
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
