// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// Add a switch case here when wiring a new writable cluster attribute
// that carries a structured value (primitives are handled by the
// generic primitiveAttributeValue branch).
func attributeValueReader(path im.ConcreteAttributePath, el tlv.Element, dec *tlv.Decoder) (im.AttributeValue, error) {
	if path.Cluster == 0x001F && path.Attribute == 0x0000 { // AccessControl.ACL
		return decodeACLList(el, dec)
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
			e.Privilege = uint8(el.Uint) //nolint:gosec // enum8 per spec
		case 2:
			e.AuthMode = uint8(el.Uint) //nolint:gosec // enum8 per spec
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
			e.FabricIndex = uint8(el.Uint) //nolint:gosec // FabricIndex is uint8
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
				v := uint32(el.Uint) //nolint:gosec // ClusterID fits uint32 by spec
				t.Cluster = &v
			}
		case 1:
			if !el.IsNull {
				v := uint16(el.Uint) //nolint:gosec // EndpointID fits uint16 by spec
				t.Endpoint = &v
			}
		case 2:
			if !el.IsNull {
				v := uint32(el.Uint) //nolint:gosec // DeviceType fits uint32 by spec
				t.DeviceType = &v
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
		return im.AttributeValue{Value: string(el.Octets)}, nil
	case tlv.TypeOctetStr1, tlv.TypeOctetStr2, tlv.TypeOctetStr4, tlv.TypeOctetStr8:
		return im.AttributeValue{Value: append([]byte(nil), el.Octets...)}, nil
	default:
		// Numeric / enum / float fall-through: surface as uint64 (caller
		// downcasts to the cluster's native width). Adequate for the v1.1
		// writable surface; richer typing is a follow-up.
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
