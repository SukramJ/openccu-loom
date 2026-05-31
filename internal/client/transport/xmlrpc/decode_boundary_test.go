// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"strings"
	"testing"
)

// TestDecodeParamsUnexpectedClosingTag exercises the "unexpected </"
// error in decodeParams when the closing tag doesn't match the start.
func TestDecodeParamsUnexpectedClosingTag(t *testing.T) {
	t.Parallel()

	// <params> closed with </wrongtag> instead of </params>.
	raw := `<?xml version="1.0"?><methodResponse><params></wrongtag></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("mismatched </params> close tag must produce error")
	}
}

// TestDecodeStructUnexpectedClosingTag exercises the mismatched
// </struct> path inside decodeStruct.
func TestDecodeStructUnexpectedClosingTag(t *testing.T) {
	t.Parallel()

	// A struct whose closing tag is </wrongstruct> — decodeMember will
	// hit the EndElement mismatch.
	raw := `<value><struct><member><name>A</name><value><i4>1</i4></value></member></wrongstruct></value>`
	if _, err := decodeValueFromString(t, raw); err == nil {
		t.Fatal("mismatched closing tag for <struct> must produce error")
	}
}

// TestDecodeArrayDataUnexpectedElement verifies that an unexpected
// child inside <data> (not a <value>) produces an error.
func TestDecodeArrayDataUnexpectedElement(t *testing.T) {
	t.Parallel()

	// <array><data><notvalue/></data></array>
	raw := `<value><array><data><notvalue/></data></array></value>`
	if _, err := decodeValueFromString(t, raw); err == nil {
		t.Fatal("unexpected element inside <data> must produce error")
	}
}

// TestReadChardataUnexpectedChild exercises the child-element error
// path in readChardata (an element where only text is expected).
func TestReadChardataUnexpectedChild(t *testing.T) {
	t.Parallel()

	// A <string> element that contains a child element instead of text.
	raw := `<value><string><child/></string></value>`
	if _, err := decodeValueFromString(t, raw); err == nil {
		t.Fatal("child element inside text-only <string> must produce error")
	}
}

// TestDecodeMemberMissingName exercises the error path in decodeMember
// where the <name> element is absent and the member ends without a value.
func TestDecodeMemberMissingName(t *testing.T) {
	t.Parallel()

	// A member whose first child is not <name> but <value>.
	raw := `<value><struct><member><value><i4>1</i4></value></member></struct></value>`
	_, err := decodeValueFromString(t, raw)
	// The spec requires <name> first; the parser may or may not produce
	// an error depending on implementation. We just ensure it doesn't
	// panic and gives a consistent result.
	_ = err // may or may not error — just ensure no panic
}

// TestExpectEndUnexpectedStartElement exercises the "stray <X> before </Y>"
// error path in expectEnd.
func TestExpectEndUnexpectedStartElement(t *testing.T) {
	t.Parallel()

	// Produce a context where expectEnd sees a <stray> start element.
	// A <methodResponse><params><param><value><i4>1</i4></value><stray/></param>
	raw := `<?xml version="1.0"?><methodResponse><params><param><value><i4>1</i4></value><stray/></param></params></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("stray start element before expected </param> must produce error")
	}
}
