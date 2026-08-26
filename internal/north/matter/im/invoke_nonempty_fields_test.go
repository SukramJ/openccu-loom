// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestInvokeRequest_NonEmptyFieldsDecodes is an ADR-0013 "earlier-catch" test
// for Decision #6.
//
// Bug-pattern: when a CommandDataIB contains a non-empty Fields container and
// the caller supplies a CommandFieldsReader, the reader must be invoked and the
// result must be stored in CommandInvocation.Fields. A nil-typed-interface
// returned by the reader is fine for "unrecognised command", but when the reader
// recognises the command and returns a non-nil struct, Fields must NOT be nil.
//
// The test wires a minimal CommandFieldsReader that drains the container and
// returns a typed sentinel struct, then verifies that the decoded Invocation
// has Fields populated (not nil) after UnmarshalInvokeRequestTLV.
func TestInvokeRequest_NonEmptyFieldsDecodes(t *testing.T) {
	t.Parallel()

	// Sentinel type the fieldsReader returns for cluster=0x0006, command=0x01
	// (OnOff::On — chosen arbitrarily; the reader doesn't need to be spec-perfect).
	type onFields struct{ Parsed bool }

	// Build a synthetic InvokeRequestMessage with a single Invoke that carries
	// a non-empty Fields struct.
	//
	// Wire layout (Matter Core Spec §10.6.7):
	//   Structure {                             ← top InvokeRequestMessage
	//     [0] bool SuppressResponse = false
	//     [1] bool TimedRequest     = false
	//     [2] Array InvokeRequests {
	//       Structure {                         ← CommandDataIB
	//         [0] List CommandPathIB {
	//           [0] u16 Endpoint = 1
	//           [1] u32 Cluster  = 0x0006
	//           [2] u32 Command  = 0x01
	//         }
	//         [1] Structure CommandFields {     ← non-empty; one context-tag field
	//           [0] bool value = true
	//         }
	//       }
	//     }
	//   }
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())    // top
	enc.PutBool(tlv.ContextTag(0), false)  // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false)  // TimedRequest
	enc.StartArray(tlv.ContextTag(2))      // InvokeRequests
	enc.StartStruct(tlv.AnonymousTag())    //   CommandDataIB
	enc.StartList(tlv.ContextTag(0))       //     CommandPathIB
	enc.PutUint(tlv.ContextTag(0), 1)      //       Endpoint: 1
	enc.PutUint(tlv.ContextTag(1), 0x0006) //       Cluster: OnOff (0x0006)
	enc.PutUint(tlv.ContextTag(2), 0x01)   //       Command: On (0x01)
	_ = enc.EndContainer()                 //     end CommandPathIB
	enc.StartStruct(tlv.ContextTag(1))     //     CommandFields (non-empty)
	enc.PutBool(tlv.ContextTag(0), true)   //       some field
	_ = enc.EndContainer()                 //     end CommandFields
	_ = enc.EndContainer()                 //   end CommandDataIB
	_ = enc.EndContainer()                 // end InvokeRequests array
	_ = enc.EndContainer()                 // end top

	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode InvokeRequestMessage: %v", err)
	}

	// CommandFieldsReader: recognises cluster=0x0006 command=0x01 and drains
	// the container before returning a non-nil onFields{}.
	fieldsReader := im.CommandFieldsReader(func(
		path im.ConcreteCommandPath,
		dec *tlv.Decoder,
		_ tlv.Element,
	) (any, error) {
		// Drain the container so the outer decoder is left positioned
		// past the EndContainer — matching the contract described in
		// CommandFieldsReader.
		for {
			el, err := dec.Next()
			if err != nil {
				return nil, err
			}
			if el.IsEndContainer {
				break
			}
			if el.IsContainer {
				// nested container — skip recursively (not needed here but safe)
				for depth := 1; depth > 0; {
					inner, err := dec.Next()
					if err != nil {
						return nil, err
					}
					if inner.IsContainer {
						depth++
					}
					if inner.IsEndContainer {
						depth--
					}
				}
			}
		}
		if path.Cluster == 0x0006 && path.Command == 0x01 {
			return onFields{Parsed: true}, nil
		}
		return nil, nil
	})

	dec := tlv.NewDecoder(wire)
	req, err := im.UnmarshalInvokeRequestTLV(dec, fieldsReader)
	if err != nil {
		t.Fatalf("UnmarshalInvokeRequestTLV: %v", err)
	}
	if len(req.Invokes) != 1 {
		t.Fatalf("Invokes: got %d, want 1", len(req.Invokes))
	}

	inv := req.Invokes[0]
	if inv.Path.Cluster != 0x0006 || inv.Path.Command != 0x01 {
		t.Errorf("path: cluster=%#x cmd=%#x, want cluster=0x0006 cmd=0x01",
			inv.Path.Cluster, inv.Path.Command)
	}

	// ADR-0013 D#6 invariant: Fields must be non-nil when the reader
	// recognised the command and returned a non-nil struct.
	if inv.Fields == nil {
		t.Fatal("ADR-0013 D#6: Fields is nil; expected onFields{Parsed:true} from reader")
	}
	got, ok := inv.Fields.(onFields)
	if !ok {
		t.Fatalf("Fields type = %T, want onFields", inv.Fields)
	}
	if !got.Parsed {
		t.Error("Fields.Parsed = false, want true")
	}
}
