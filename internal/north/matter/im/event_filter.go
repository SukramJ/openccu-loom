// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import "github.com/SukramJ/openccu-loom/internal/north/matter/tlv"

// matchDataVersionFilter searches filters for a matching (endpoint, cluster)
// pair and returns (cachedVersion, true) on hit.
// Returns (0, false) when no filter covers the (endpoint, cluster).
func matchDataVersionFilter(filters []DataVersionFilter, endpoint uint16, cluster uint32) (uint32, bool) {
	return MatchDataVersionFilter(filters, endpoint, cluster)
}

// MatchDataVersionFilter is the exported form of [matchDataVersionFilter]
// used by the bridge's Subscribe path (bridge/subscribe.go) to apply
// the same filter evaluation logic as HandleReadRequest.
func MatchDataVersionFilter(filters []DataVersionFilter, endpoint uint16, cluster uint32) (uint32, bool) {
	for _, f := range filters {
		if f.Endpoint == endpoint && f.Cluster == cluster {
			return f.DataVersion, true
		}
	}
	return 0, false
}

// readDataVersionFilterArray parses an Array of DataVersionFilter structs.
// The array opener has already been consumed by the caller.
// Called from [UnmarshalReadRequestTLV] and [UnmarshalSubscribeRequestTLV].
// Each element is a struct per Matter Core Spec §10.6.4 DataVersionFilter:
//
//	tag 0 = Path (List: tag 1=EndpointID, tag 2=ClusterID)
//	tag 1 = DataVersion (uint32)
func readDataVersionFilterArray(dec *tlv.Decoder) ([]DataVersionFilter, error) {
	var filters []DataVersionFilter
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, err
		}
		if el.IsEndContainer {
			return filters, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeStructure {
			continue
		}
		f, err := readDataVersionFilterFields(dec)
		if err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
}

// readDataVersionFilterFields parses the inner fields of a DataVersionFilter
// struct after the struct opener has been consumed.
// Matter §10.6.4 DataVersionFilterIB:
//
//	tag 0 = Path (ClusterPathIB — a List with tag 1=EndpointID, tag 2=ClusterID)
//	tag 1 = DataVersion (uint32)
func readDataVersionFilterFields(dec *tlv.Decoder) (DataVersionFilter, error) {
	var f DataVersionFilter
	for {
		el, err := dec.Next()
		if err != nil {
			return DataVersionFilter{}, err
		}
		if el.IsEndContainer {
			return f, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case 0: // Path (ClusterPathIB — a List)
			if !el.IsContainer {
				continue
			}
			for {
				inner, innerErr := dec.Next()
				if innerErr != nil {
					return DataVersionFilter{}, innerErr
				}
				if inner.IsEndContainer {
					break
				}
				if inner.Tag.Kind != tlv.TagKindContext {
					continue
				}
				switch uint8(inner.Tag.Number & 0xFF) {
				case 1: // EndpointID
					f.Endpoint = uint16(inner.Uint & 0xFFFF)
				case 2: // ClusterID
					f.Cluster = uint32(inner.Uint & 0xFFFFFFFF)
				}
			}
		case 1: // DataVersion (uint32)
			f.DataVersion = uint32(el.Uint & 0xFFFFFFFF)
		}
	}
}

// readEventFilterArray parses an Array of EventFilterIB structs per
// Matter Core Spec §10.6.9. The array opener has already been consumed
// by the caller. Called from [UnmarshalSubscribeRequestTLV] and
// [UnmarshalReadRequestTLV].
//
// Mirrors chip src/app/ReadHandler.cpp ProcessEventFilters.
func readEventFilterArray(dec *tlv.Decoder) ([]EventMinimumNumber, error) {
	var filters []EventMinimumNumber
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, err
		}
		if el.IsEndContainer {
			return filters, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeStructure {
			// Skip non-struct elements — forward-compat tolerance. A
			// scalar has already been fully consumed by dec.Next(); a
			// container (e.g. a nested Array/List) still has its inner
			// elements pending and must be drained via skipContainer —
			// otherwise the next dec.Next() call reads that container's
			// first inner element as if it were the following
			// EventFilters array member and desyncs the rest of the
			// message, mirroring the read.go / subscribe.go fix for the
			// sibling EventFilters-field case.
			if el.IsContainer {
				if err := skipContainer(dec); err != nil {
					return nil, err
				}
			}
			continue
		}
		f, err := readEventFilterFields(dec)
		if err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
}

// readEventFilterFields parses the inner fields of an EventFilterIB struct
// after the struct opener has been consumed.
//
// Matter §10.6.9 EventFilterIB:
//
//	tag 0 = NodeID (uint64, optional)
//	tag 1 = EventMin (uint64, mandatory)
//
// Mirrors matter.js packages/types/src/protocol/types/TlvEventFilter.ts:16-17
// (`nodeId: TlvOptionalField(0, TlvNodeId)`, `eventMin: TlvField(1,
// TlvUInt64)`) and chip src/app/ReadHandler.cpp ProcessEventFilters.
func readEventFilterFields(dec *tlv.Decoder) (EventMinimumNumber, error) {
	const (
		tagEventFilterNodeID   uint8 = 0
		tagEventFilterEventMin uint8 = 1
	)

	var f EventMinimumNumber
	for {
		el, err := dec.Next()
		if err != nil {
			return EventMinimumNumber{}, err
		}
		if el.IsEndContainer {
			return f, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			if el.IsContainer {
				if skipErr := skipContainer(dec); skipErr != nil {
					return EventMinimumNumber{}, skipErr
				}
			}
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagEventFilterNodeID:
			f.NodeID = el.Uint
			f.HasNodeID = true
		case tagEventFilterEventMin:
			f.EventMin = el.Uint
		default:
			if el.IsContainer {
				if skipErr := skipContainer(dec); skipErr != nil {
					return EventMinimumNumber{}, skipErr
				}
			}
		}
	}
}
