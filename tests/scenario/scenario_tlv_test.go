// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scenario

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"
)

// tlvTopLevelContextTags walks the IM body's outer anonymous struct
// once and returns the set of context-tag numbers it found at depth 1.
// Assertions stay at tag presence; a scenario that needs to inspect
// deeper (e.g. the cluster ID inside attribute_reports[0].path) gets
// its own extractor below rather than a generalised path language.
func tlvTopLevelContextTags(body []byte) (map[uint8]bool, error) {
	if len(body) == 0 {
		return nil, errors.New("empty IM body")
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
	slices.Sort(out)
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
// the global IM-revision marker (tag 0xFF uint8). Used to assert the
// engine clamped the negotiated cadence to the configured ceiling.
func tlvSubscribeResponseMaxInterval(body []byte) (uint16, error) {
	v, err := tlvTopLevelContextUint(body, 2)
	if err != nil {
		return 0, err
	}
	if v > 0xFFFF {
		return 0, fmt.Errorf("MaxInterval overflow: %d", v)
	}
	return uint16(v), nil //nolint:gosec // G115: range guarded by the overflow check above
}

// tlvSubscribeResponseSubscriptionID extracts the SubscriptionID
// (context tag 0 uint32) a SubscribeResponseMessage echoes back. The
// harness needs it to resolve the *subscription.Subscription the
// bridge admitted for a wire-driven Subscribe, since the manager
// keys its registry by that identifier.
func tlvSubscribeResponseSubscriptionID(body []byte) (uint32, error) {
	v, err := tlvTopLevelContextUint(body, 0)
	if err != nil {
		return 0, err
	}
	if v > 0xFFFFFFFF {
		return 0, fmt.Errorf("SubscriptionID overflow: %d", v)
	}
	return uint32(v), nil //nolint:gosec // G115: range guarded by the overflow check above
}

// tlvTopLevelContextUint returns the unsigned value carried by the
// first non-container element at depth 1 whose context tag is `tag`.
func tlvTopLevelContextUint(body []byte, tag uint32) (uint64, error) {
	if len(body) == 0 {
		return 0, errors.New("empty IM body")
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
		if depth == 1 && el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == tag && !el.IsContainer {
			return el.Uint, nil
		}
		if el.IsContainer {
			depth++
		}
	}
	return 0, fmt.Errorf("top-level context tag %d not found", tag)
}

// tlvFirstAttributeUintValue walks the IM body's outer anonymous
// struct → attributeReports[0] → AttributeDataIB → Data (tag 2)
// and returns the value as a uint64. Used by fabric-scoped read
// scenarios: the fake fabric-scoped reader returns the dispatch
// context's FabricIndex as a uint8 attribute value, so the scenario
// can verify the value the bridge encoded matches the per-session
// FabricIndex it provided.
func tlvFirstAttributeUintValue(body []byte) (uint64, error) {
	return tlvFirstAttributeDataField(body, 2)
}

// tlvFirstAttributeDataVersion walks into the IM body's outer
// anonymous struct → attributeReports array (tag 1) → first
// AttributeReportIB container → AttributeDataIB (tag 1) →
// DataVersion (tag 0 uint) and returns the value. Used by
// DataVersion-monotonicity scenarios to assert per-cluster
// DataVersion advancement across consecutive fires.
func tlvFirstAttributeDataVersion(body []byte) (uint32, error) {
	v, err := tlvFirstAttributeDataField(body, 0)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil //nolint:gosec // G115: DataVersion is a uint32 TLV field; value fits by spec
}

// tlvFirstAttributeDataField descends outer struct → attributeReports
// array (tag 1) → first AttributeReportIB → AttributeDataIB (tag 1)
// and returns the unsigned value carried at `field` inside it.
func tlvFirstAttributeDataField(body []byte, field uint32) (uint64, error) {
	if len(body) == 0 {
		return 0, errors.New("empty IM body")
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
						if dDepth == 1 && dEl.Tag.Kind == tlv.TagKindContext && dEl.Tag.Number == field && !dEl.IsContainer {
							return dEl.Uint, nil
						}
						if dEl.IsContainer {
							dDepth++
						}
					}
					return 0, fmt.Errorf("AttributeDataIB missing field tag %d", field)
				}
				if rEl.IsContainer {
					rDepth++
				}
			}
			return 0, errors.New("AttributeReportIB missing AttributeDataIB")
		}
		if el.IsContainer {
			depth++
		}
	}
	return 0, errors.New("ReportData has no attributeReports array")
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
// returns the first per-command Status code. Used to assert
// MoveToLevel succeeded (status 0) rather than returning
// 0x85 InvalidCommand / 0xC4 UnsupportedCluster.
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
		return 0, errors.New("empty IM body")
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
	return 0, errors.New("InvokeResponse missing invokeResponses array")
}

// encodeScenarioWriteRequest builds a minimal WriteRequestMessage
// targeting a single attribute path with a boolean value. Tag
// layout mirrors the production decoder in go-fabric's im package.
// Used by the send_write_request step.
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
		return 0, errors.New("empty IM body")
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
