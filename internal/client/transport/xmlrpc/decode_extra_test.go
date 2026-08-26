// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"encoding/xml"
	"strings"
	"testing"
)

// helper: decode the first Value from an XML fragment of the form
// <value>...</value>.
func decodeValueFromString(t *testing.T, fragment string) (Value, error) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(fragment))
	tok, err := d.Token()
	if err != nil {
		t.Fatalf("decodeValueFromString: read token: %v", err)
	}
	start, ok := tok.(xml.StartElement)
	if !ok || start.Name.Local != "value" {
		t.Fatalf("decodeValueFromString: expected <value>, got %T", tok)
	}
	return DecodeValue(d, start)
}

// TestDecodeValueDateTimeISO8601 verifies that the compact
// ISO8601 format used by the CCU is accepted.
func TestDecodeValueDateTimeISO8601(t *testing.T) {
	t.Parallel()

	got, err := decodeValueFromString(t, `<value><dateTime.iso8601>20260428T12:00:00</dateTime.iso8601></value>`)
	if err != nil {
		t.Fatalf("DecodeValue dateTime: %v", err)
	}
	if got.Kind() != KindDateTime {
		t.Fatalf("kind=%v, want KindDateTime", got.Kind())
	}
}

// TestDecodeValueDateTimeRFC3339 verifies that RFC 3339 is also accepted.
func TestDecodeValueDateTimeRFC3339(t *testing.T) {
	t.Parallel()

	got, err := decodeValueFromString(t, `<value><dateTime.iso8601>2026-04-28T12:00:00Z</dateTime.iso8601></value>`)
	if err != nil {
		t.Fatalf("DecodeValue dateTime RFC3339: %v", err)
	}
	if got.Kind() != KindDateTime {
		t.Fatalf("kind=%v, want KindDateTime", got.Kind())
	}
}

// TestDecodeValueDateTimeInvalid checks that a malformed dateTime is rejected.
func TestDecodeValueDateTimeInvalid(t *testing.T) {
	t.Parallel()

	if _, err := decodeValueFromString(t, `<value><dateTime.iso8601>not-a-date</dateTime.iso8601></value>`); err == nil {
		t.Fatal("expected error for invalid dateTime")
	}
}

// TestDecodeValueUnknownKind verifies that an unknown inner element
// (<foo>) produces an error.
func TestDecodeValueUnknownKind(t *testing.T) {
	t.Parallel()

	if _, err := decodeValueFromString(t, `<value><foo>x</foo></value>`); err == nil {
		t.Fatal("unknown element inside <value> must produce error")
	}
}

// TestDecodeValueInvalidInt checks that a non-numeric <i4> content
// returns an error.
func TestDecodeValueInvalidInt(t *testing.T) {
	t.Parallel()

	if _, err := decodeValueFromString(t, `<value><i4>not-a-number</i4></value>`); err == nil {
		t.Fatal("expected error for invalid i4 content")
	}
}

// TestDecodeValueInvalidDouble checks that a non-numeric <double> content
// returns an error.
func TestDecodeValueInvalidDouble(t *testing.T) {
	t.Parallel()

	if _, err := decodeValueFromString(t, `<value><double>abc</double></value>`); err == nil {
		t.Fatal("expected error for invalid double content")
	}
}

// TestDecodeValueNonFiniteDoubleRejected checks that NaN and Inf — both
// of which strconv.ParseFloat accepts as valid float text — are rejected
// at the wire boundary rather than reaching the model as a DoubleValue
// with no JSON representation.
func TestDecodeValueNonFiniteDoubleRejected(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"nan", "NaN", "inf", "+Inf", "-Inf", "infinity"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeValueFromString(t, `<value><double>`+text+`</double></value>`); err == nil {
				t.Fatalf("expected error for non-finite double %q", text)
			}
		})
	}
}

// TestDecodeValueInvalidBase64 checks that bad base64 is rejected.
func TestDecodeValueInvalidBase64(t *testing.T) {
	t.Parallel()

	if _, err := decodeValueFromString(t, `<value><base64>!!!notbase64!!!</base64></value>`); err == nil {
		t.Fatal("expected error for invalid base64 content")
	}
}

// TestDecodeValueInvalidBoolean checks that an unrecognised boolean value
// is rejected.
func TestDecodeValueInvalidBoolean(t *testing.T) {
	t.Parallel()

	if _, err := decodeValueFromString(t, `<value><boolean>maybe</boolean></value>`); err == nil {
		t.Fatal("expected error for invalid boolean content")
	}
}

// TestDecodeValueImplicitString exercises the bare-chardata form that
// the CCU uses for implicit strings: <value>abc</value> (no inner element).
func TestDecodeValueImplicitString(t *testing.T) {
	t.Parallel()

	got, err := decodeValueFromString(t, `<value>implicit</value>`)
	if err != nil {
		t.Fatalf("DecodeValue implicit string: %v", err)
	}
	s, err := AsString(got)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if s != "implicit" {
		t.Fatalf("string = %q, want implicit", s)
	}
}

// TestDecodeArrayEmptyData exercises the path where an <array> has an
// empty <data/> element.
func TestDecodeArrayEmptyData(t *testing.T) {
	t.Parallel()

	got, err := decodeValueFromString(t, `<value><array><data/></array></value>`)
	if err != nil {
		t.Fatalf("decode empty array: %v", err)
	}
	arr, err := AsArray(got)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if len(arr) != 0 {
		t.Fatalf("array len=%d, want 0", len(arr))
	}
}

// TestDecodeArrayUnexpectedElement exercises the "not <data>" error path
// inside decodeArray.
func TestDecodeArrayUnexpectedElement(t *testing.T) {
	t.Parallel()

	// <array> contains <foo/> instead of <data> — should error.
	if _, err := decodeValueFromString(t, `<value><array><foo/></array></value>`); err == nil {
		t.Fatal("unexpected element inside <array> must produce error")
	}
}

// TestDecodeValueWrongStart checks that passing a non-<value> start element
// to DecodeValue produces an error.
func TestDecodeValueWrongStart(t *testing.T) {
	t.Parallel()

	d := xml.NewDecoder(strings.NewReader(`<param/>`))
	tok, _ := d.Token()
	start := tok.(xml.StartElement)
	if _, err := DecodeValue(d, start); err == nil {
		t.Fatal("DecodeValue with non-<value> start must return error")
	}
}

// TestExpectEndWrongClosingTag exercises the "wrong closing tag" path
// of expectEnd. We inject a <struct> content whose closing </member>
// fires before </struct>.
func TestExpectEndWrongClosingTag(t *testing.T) {
	t.Parallel()

	// consumeCloseOrSelfClose is internal; test via a full round-trip
	// that triggers the mismatch. An <array> whose closing tag is
	// </wrongtag> instead of </array> should trigger an error in the
	// codec.
	raw := `<value><array><data><value>1</value></data></wrongtag></value>`
	if _, err := decodeValueFromString(t, raw); err == nil {
		t.Fatal("mismatched closing tag must produce error")
	}
}
