// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"fmt"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// tlvTopLevelContextTags walks the IM body's outer anonymous struct
// once and returns the set of context-tag numbers it found at depth
// 1. Phase-B keeps assertions to tag-presence; Phase-D can promote to
// path-keyed value comparison if a scenario needs to inspect deeper
// (e.g. the cluster ID inside attribute_reports[0].path).
func tlvTopLevelContextTags(body []byte) (map[uint8]bool, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil {
		return nil, fmt.Errorf("outer struct: %w", err)
	}
	if !first.IsContainer || first.Type != tlv.TypeStructure {
		return nil, fmt.Errorf("outer element is %v, want anonymous struct", first.Type)
	}
	found := make(map[uint8]bool)
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return nil, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext {
			found[uint8(el.Tag.Number)] = true //nolint:gosec // spec-conforming context tags are 0..7
		}
		if el.IsContainer {
			depth++
		}
	}
	return found, nil
}

// sortedTagKeys is a deterministic dump of a tag-set used in error
// messages so the diagnostic order doesn't drift across runs.
func sortedTagKeys(m map[uint8]bool) []uint8 {
	out := make([]uint8, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// sortedStringKeys is the string-keyed counterpart of sortedTagKeys.
func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tlvSubscribeResponseMaxInterval extracts the MaxInterval field
// (context tag 2 uint16) from a SubscribeResponseMessage payload.
// The outer struct also carries SubscriptionID (tag 0 uint32) and
// the global IM-revision marker (tag 0xFF uint8). Used by Phase-T
// scenarios to assert the engine clamped the negotiated cadence
// to the configured ceiling.
func tlvSubscribeResponseMaxInterval(body []byte) (uint16, error) {
	if len(body) == 0 {
		return 0, fmt.Errorf("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil || !first.IsContainer || first.Type != tlv.TypeStructure {
		return 0, fmt.Errorf("outer struct: err=%w el=%+v", err, first)
	}
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return 0, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 2 && !el.IsContainer {
			if el.Uint > 0xFFFF {
				return 0, fmt.Errorf("MaxInterval overflow: %d", el.Uint)
			}
			return uint16(el.Uint), nil //nolint:gosec // G115: range guarded by el.Uint > 0xFFFF check above
		}
		if el.IsContainer {
			depth++
		}
	}
	return 0, fmt.Errorf("SubscribeResponse missing MaxInterval (tag 2)")
}

// tlvFirstAttributeUintValue walks the IM body's outer anonymous
// struct → attributeReports[0] → AttributeDataIB → Data (tag 2)
// and returns the value as a uint64. Used by fabric-scoped read
// scenarios: the fake FabricScopedReader returns the dispatch
// context's FabricIndex as a uint8 attribute value, so the
// scenario can verify the value the bridge encoded matches the
// per-session FabricIndex it provided.
func tlvFirstAttributeUintValue(body []byte) (uint64, error) {
	if len(body) == 0 {
		return 0, fmt.Errorf("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil || !first.IsContainer || first.Type != tlv.TypeStructure {
		return 0, fmt.Errorf("outer struct: err=%w el=%+v", err, first)
	}
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return 0, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.IsContainer && el.Type == tlv.TypeArray &&
			el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 1 {
			el2, err := dec.Next()
			if err != nil || !el2.IsContainer {
				return 0, fmt.Errorf("attributeReports[0]: err=%w el=%+v", err, el2)
			}
			rDepth := 1
			for rDepth > 0 {
				rEl, err := dec.Next()
				if err != nil {
					return 0, fmt.Errorf("walk attr report: %w", err)
				}
				if rEl.IsEndContainer {
					rDepth--
					continue
				}
				if rDepth == 1 && rEl.IsContainer && rEl.Tag.Kind == tlv.TagKindContext && rEl.Tag.Number == 1 {
					dDepth := 1
					for dDepth > 0 {
						dEl, err := dec.Next()
						if err != nil {
							return 0, fmt.Errorf("walk attr data: %w", err)
						}
						if dEl.IsEndContainer {
							dDepth--
							continue
						}
						if dDepth == 1 && dEl.Tag.Kind == tlv.TagKindContext && dEl.Tag.Number == 2 && !dEl.IsContainer {
							return dEl.Uint, nil
						}
						if dEl.IsContainer {
							dDepth++
						}
					}
					return 0, fmt.Errorf("AttributeDataIB missing Data (tag 2)")
				}
				if rEl.IsContainer {
					rDepth++
				}
			}
			return 0, fmt.Errorf("AttributeReportIB missing AttributeDataIB")
		}
		if el.IsContainer {
			depth++
		}
	}
	return 0, fmt.Errorf("ReportData has no attributeReports array")
}

// tlvFirstAttributeDataVersion walks into the IM body's outer
// anonymous struct → attributeReports array (tag 1) → first
// AttributeReportIB container → AttributeDataIB (tag 1) →
// DataVersion (tag 0 uint) and returns the value. Used by
// DataVersion-monotonicity scenarios to assert per-cluster
// DataVersion advancement across consecutive fires.
func tlvFirstAttributeDataVersion(body []byte) (uint32, error) {
	if len(body) == 0 {
		return 0, fmt.Errorf("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil {
		return 0, fmt.Errorf("outer struct: %w", err)
	}
	if !first.IsContainer || first.Type != tlv.TypeStructure {
		return 0, fmt.Errorf("outer element is %v, want anonymous struct", first.Type)
	}
	// Find the attributeReports array (tag 1) at depth 1.
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return 0, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.IsContainer && el.Type == tlv.TypeArray &&
			el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 1 {
			// Enter the array; first element is the first AttributeReportIB.
			el2, err := dec.Next()
			if err != nil {
				return 0, fmt.Errorf("read first attr report: %w", err)
			}
			if !el2.IsContainer {
				return 0, fmt.Errorf("attributeReports[0] not a container")
			}
			// Find AttributeDataIB at tag 1 inside this report.
			rDepth := 1
			for rDepth > 0 {
				rEl, err := dec.Next()
				if err != nil {
					return 0, fmt.Errorf("walk attr report: %w", err)
				}
				if rEl.IsEndContainer {
					rDepth--
					continue
				}
				if rDepth == 1 && rEl.IsContainer && rEl.Tag.Kind == tlv.TagKindContext && rEl.Tag.Number == 1 {
					// Found AttributeDataIB; find DataVersion (tag 0).
					dDepth := 1
					for dDepth > 0 {
						dEl, err := dec.Next()
						if err != nil {
							return 0, fmt.Errorf("walk attr data: %w", err)
						}
						if dEl.IsEndContainer {
							dDepth--
							continue
						}
						if dDepth == 1 && dEl.Tag.Kind == tlv.TagKindContext && dEl.Tag.Number == 0 && !dEl.IsContainer {
							return uint32(dEl.Uint), nil //nolint:gosec // G115: DataVersion is a uint32 TLV field; value fits by spec
						}
						if dEl.IsContainer {
							dDepth++
						}
					}
					return 0, fmt.Errorf("AttributeDataIB missing DataVersion (tag 0)")
				}
				if rEl.IsContainer {
					rDepth++
				}
			}
			return 0, fmt.Errorf("AttributeReportIB missing AttributeDataIB (tag 1)")
		}
		if el.IsContainer {
			depth++
		}
	}
	return 0, fmt.Errorf("ReportData has no attributeReports array")
}

// encodeScenarioInvokeMoveToLevel builds an IM:InvokeRequestMessage
// shipping LevelControl.MoveToLevel (cluster 0x0008, command 0x00)
// on the given endpoint with the supplied Level byte. The fields
// struct carries tag 0 Level (uint8) only — TransitionTime and
// Options fields are omitted (server treats them as defaults).
func encodeScenarioInvokeMoveToLevel(endpoint uint16, level uint8) ([]byte, error) {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // InvokeRequests
	enc.StartStruct(tlv.AnonymousTag())   // CommandDataIB
	// CommandPath = list of endpoint/cluster/command
	enc.StartList(tlv.ContextTag(0))
	enc.PutUint(tlv.ContextTag(0), uint64(endpoint))
	enc.PutUint(tlv.ContextTag(1), 0x0008) // LevelControl
	enc.PutUint(tlv.ContextTag(2), 0x0000) // MoveToLevel
	_ = enc.EndContainer()
	// CommandFields (tag 1): struct with tag 0 Level (uint8).
	enc.StartStruct(tlv.ContextTag(1))
	enc.PutUint(tlv.ContextTag(0), uint64(level))
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(0xFF), uint64(im.MatterInteractionModelRevision))
	_ = enc.EndContainer()
	return enc.Bytes()
}

// tlvInvokeResponseFirstStatus walks an InvokeResponseMessage and
// returns the first per-command Status code. Used by Phase-X
// scenarios to assert MoveToLevel succeeded (status 0) vs. the
// bug case (status 0x85 InvalidCommand / 0xC4 UnsupportedCluster).
//
// Layout (Matter §10.6.7):
//
//	struct
//	 [0] SuppressResponse bool
//	 [1] InvokeResponses [array of struct
//	      [0] CommandData [struct CommandDataIB]
//	      [1] CommandStatus [struct CommandStatusIB
//	           [0] CommandPath
//	           [1] StatusIB struct { [0] Status uint8 }
//	          ]
//	     ]
//	 [0xFF] IMRevision
func tlvInvokeResponseFirstStatus(body []byte) (uint8, error) {
	if len(body) == 0 {
		return 0, fmt.Errorf("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil || !first.IsContainer || first.Type != tlv.TypeStructure {
		return 0, fmt.Errorf("outer struct: err=%w el=%+v", err, first)
	}
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return 0, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			continue
		}
		if depth == 1 && el.IsContainer && el.Type == tlv.TypeArray &&
			el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 1 {
			// First InvokeResponseEntry
			entryStart, err := dec.Next()
			if err != nil || !entryStart.IsContainer {
				return 0, fmt.Errorf("invokeResponses[0]: err=%w el=%+v", err, entryStart)
			}
			// Walk this entry looking for CommandStatus (tag 1) →
			// StatusIB (tag 1) → Status (tag 0). Success entries
			// don't carry CommandStatus; treat that as status=0.
			eDepth := 1
			for eDepth > 0 {
				e, err := dec.Next()
				if err != nil {
					return 0, fmt.Errorf("walk entry: %w", err)
				}
				if e.IsEndContainer {
					eDepth--
					continue
				}
				if eDepth == 1 && e.IsContainer && e.Tag.Kind == tlv.TagKindContext && e.Tag.Number == 1 {
					// CommandStatus struct
					csDepth := 1
					for csDepth > 0 {
						cs, err := dec.Next()
						if err != nil {
							return 0, fmt.Errorf("walk command status: %w", err)
						}
						if cs.IsEndContainer {
							csDepth--
							continue
						}
						if csDepth == 1 && cs.IsContainer && cs.Tag.Kind == tlv.TagKindContext && cs.Tag.Number == 1 {
							// StatusIB
							sDepth := 1
							for sDepth > 0 {
								st, err := dec.Next()
								if err != nil {
									return 0, fmt.Errorf("walk status: %w", err)
								}
								if st.IsEndContainer {
									sDepth--
									continue
								}
								if sDepth == 1 && st.Tag.Kind == tlv.TagKindContext && st.Tag.Number == 0 && !st.IsContainer {
									return uint8(st.Uint), nil //nolint:gosec // G115: Status field is uint8 by Matter §10.6.1.5; value fits by spec
								}
								if st.IsContainer {
									sDepth++
								}
							}
						} else if cs.IsContainer {
							csDepth++
						}
					}
				} else if e.IsContainer {
					eDepth++
				}
			}
			// Reached end of first InvokeResponse entry without seeing a
			// CommandStatus — that's a CommandData-only success.
			return 0, nil
		}
		if el.IsContainer {
			depth++
		}
	}
	return 0, fmt.Errorf("InvokeResponse missing invokeResponses array")
}

// encodeScenarioWriteRequest builds a minimal WriteRequestMessage
// targeting a single attribute path with a boolean value. Tag
// layout mirrors the production decoder in im/write.go +
// im/path.go. Used by send_write_request step.
func encodeScenarioWriteRequest(path im.ConcreteAttributePath, value bool) ([]byte, error) {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // WriteRequests
	enc.StartStruct(tlv.AnonymousTag())   // AttributeDataIB
	enc.PutUint(tlv.ContextTag(0), 0)     // DataVersion (optional, 0 = no constraint)
	path.MarshalTLV(enc, tlv.ContextTag(1))
	enc.PutBool(tlv.ContextTag(2), value)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(0xFF), uint64(im.MatterInteractionModelRevision))
	_ = enc.EndContainer()
	return enc.Bytes()
}

// tlvAttributeReportsCount enters the outer struct and the
// attributeReports array (tag 1) and counts container-opener
// entries at depth 2 — each is one AttributeReport.
func tlvAttributeReportsCount(body []byte) (int, error) {
	if len(body) == 0 {
		return 0, fmt.Errorf("empty IM body")
	}
	dec := tlv.NewDecoder(body)
	first, err := dec.Next()
	if err != nil {
		return 0, fmt.Errorf("outer struct: %w", err)
	}
	if !first.IsContainer || first.Type != tlv.TypeStructure {
		return 0, fmt.Errorf("outer element is %v, want anonymous struct", first.Type)
	}
	count := 0
	depth := 1
	inAttrReportsArray := false
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return 0, fmt.Errorf("walk: %w", err)
		}
		if el.IsEndContainer {
			depth--
			if depth == 1 {
				inAttrReportsArray = false
			}
			continue
		}
		if depth == 1 && el.IsContainer && el.Type == tlv.TypeArray &&
			el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 1 {
			inAttrReportsArray = true
			depth++
			continue
		}
		if depth == 2 && inAttrReportsArray && el.IsContainer {
			count++
		}
		if el.IsContainer {
			depth++
		}
	}
	return count, nil
}
