// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// maxDecodeDepth bounds how deeply nested <struct>/<array> values may be
// in a single decoded value. Without it, a crafted payload of deeply
// nested arrays drives the decoder into unbounded recursion and crashes
// the process with a non-recoverable stack-overflow. Real CCU responses
// nest only a few levels; 64 mirrors the equivalent guard in
// transport/binrpc/decode.go and is far above any legitimate response.
const maxDecodeDepth = 64

// DecodeValue reads a single <value>…</value> element from d, starting
// at start. The returned Value is one of the concrete types in value.go.
//
// The function is tolerant of the two string shapes CCU emits:
//   - <value><string>abc</string></value>
//   - <value>abc</value>
//
// It is strict about everything else: unknown inner elements produce an
// error rather than silently decoding as a StringValue.
func DecodeValue(d *xml.Decoder, start xml.StartElement) (Value, error) {
	return decodeValue(d, start, 0)
}

// decodeValue is the depth-tracking implementation behind [DecodeValue].
// depth counts <struct>/<array> nesting so a crafted deeply-nested
// payload cannot drive unbounded recursion into a stack-overflow crash;
// see [maxDecodeDepth].
func decodeValue(d *xml.Decoder, start xml.StartElement, depth int) (Value, error) {
	if start.Name.Local != "value" {
		return nil, fmt.Errorf("xmlrpc: expected <value>, got <%s>", start.Name.Local)
	}
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("xmlrpc: nesting exceeds max depth %d", maxDecodeDepth)
	}

	var chardata strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: read value token: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			v, err := decodeTypedValue(d, t, depth)
			if err != nil {
				return nil, err
			}
			// Consume the closing </value>.
			if err := expectEnd(d, "value"); err != nil {
				return nil, err
			}
			return v, nil
		case xml.CharData:
			chardata.Write(t)
		case xml.EndElement:
			if t.Name.Local != "value" {
				return nil, fmt.Errorf("xmlrpc: expected </value>, got </%s>", t.Name.Local)
			}
			return StringValue(chardata.String()), nil
		case xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		}
	}
}

// decodeTypedValue dispatches on the inner element's local name and
// reads its contents, assuming the caller has already consumed the
// StartElement.
func decodeTypedValue(d *xml.Decoder, start xml.StartElement, depth int) (Value, error) { //nolint:funlen // wire/dispatch table over many attribute/opcode cases
	switch start.Name.Local {
	case "nil":
		if err := consumeCloseOrSelfClose(d, start); err != nil {
			return nil, err
		}
		return NilValue{}, nil

	case "i4", "int":
		s, err := readChardata(d, start)
		if err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: invalid int %q: %w", s, err)
		}
		return IntValue(n), nil

	case "boolean":
		s, err := readChardata(d, start)
		if err != nil {
			return nil, err
		}
		switch strings.TrimSpace(s) {
		case "0", "false":
			return BoolValue(false), nil
		case "1", "true":
			return BoolValue(true), nil
		default:
			return nil, fmt.Errorf("xmlrpc: invalid boolean %q", s)
		}

	case "string":
		s, err := readChardata(d, start)
		if err != nil {
			return nil, err
		}
		return StringValue(s), nil

	case "double":
		s, err := readChardata(d, start)
		if err != nil {
			return nil, err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: invalid double %q: %w", s, err)
		}
		// ParseFloat accepts "nan"/"inf" as valid float text, but a
		// non-finite value has no JSON representation: it reaches the
		// model, then breaks every north-bound JSON encoding of whatever
		// batch or paramset carries it alongside healthy values. Reject
		// it here, at the wire boundary, mirroring the write-side guard
		// in binrpc encodeDouble.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("xmlrpc: non-finite double %q", s)
		}
		return DoubleValue(f), nil

	case "dateTime.iso8601":
		s, err := readChardata(d, start)
		if err != nil {
			return nil, err
		}
		s = strings.TrimSpace(s)
		// Accept both compact and RFC3339 forms; callers typically see
		// compact. TryParse returns the first layout that sticks.
		layouts := []string{ISO8601CompactLayout, time.RFC3339}
		for _, l := range layouts {
			if t, err := time.Parse(l, s); err == nil {
				return DateTimeValue(t), nil
			}
		}
		return nil, fmt.Errorf("xmlrpc: invalid dateTime.iso8601 %q", s)

	case "base64":
		s, err := readChardata(d, start)
		if err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: invalid base64: %w", err)
		}
		return Base64Value(raw), nil

	case "struct":
		return decodeStruct(d, start, depth)

	case "array":
		return decodeArray(d, start, depth)

	default:
		return nil, fmt.Errorf("xmlrpc: unknown value kind <%s>", start.Name.Local)
	}
}

// decodeStruct reads the contents of a <struct> element, consuming the
// closing </struct>. Members are preserved in source order.
func decodeStruct(d *xml.Decoder, start xml.StartElement, depth int) (StructValue, error) {
	var out StructValue
	for {
		tok, err := d.Token()
		if err != nil {
			return StructValue{}, fmt.Errorf("xmlrpc: read struct: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "member" {
				return StructValue{}, fmt.Errorf("xmlrpc: struct: unexpected <%s>", t.Name.Local)
			}
			m, err := decodeMember(d, depth)
			if err != nil {
				return StructValue{}, err
			}
			out.Members = append(out.Members, m)
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return StructValue{}, fmt.Errorf("xmlrpc: struct: unexpected </%s>", t.Name.Local)
			}
			return out, nil
		case xml.CharData, xml.Comment, xml.ProcInst, xml.Directive:
			// ignore whitespace between members
		}
	}
}

func decodeMember(d *xml.Decoder, depth int) (Member, error) {
	var m Member
	haveName := false
	haveValue := false
	for {
		tok, err := d.Token()
		if err != nil {
			return Member{}, fmt.Errorf("xmlrpc: read member: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "name":
				s, err := readChardata(d, t)
				if err != nil {
					return Member{}, err
				}
				m.Name = s
				haveName = true
			case "value":
				v, err := decodeValue(d, t, depth+1)
				if err != nil {
					return Member{}, err
				}
				m.Value = v
				haveValue = true
			default:
				return Member{}, fmt.Errorf("xmlrpc: member: unexpected <%s>", t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local != "member" {
				return Member{}, fmt.Errorf("xmlrpc: member: unexpected </%s>", t.Name.Local)
			}
			if !haveName {
				return Member{}, errors.New("xmlrpc: struct member missing <name>")
			}
			if !haveValue {
				return Member{}, errors.New("xmlrpc: struct member missing <value>")
			}
			return m, nil
		}
	}
}

// decodeArray reads the contents of an <array>, expecting a single
// <data> child element.
func decodeArray(d *xml.Decoder, start xml.StartElement, depth int) (ArrayValue, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: read array: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "data" {
				return nil, fmt.Errorf("xmlrpc: array: unexpected <%s>", t.Name.Local)
			}
			return decodeArrayData(d, depth)
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return nil, fmt.Errorf("xmlrpc: array: unexpected </%s>", t.Name.Local)
			}
			return nil, nil
		}
	}
}

func decodeArrayData(d *xml.Decoder, depth int) (ArrayValue, error) {
	var out ArrayValue
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: read array data: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "value" {
				return nil, fmt.Errorf("xmlrpc: array data: unexpected <%s>", t.Name.Local)
			}
			v, err := decodeValue(d, t, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		case xml.EndElement:
			if t.Name.Local == "data" {
				// The next token should be </array>; let the array-level
				// loop observe it.
				if err := expectEnd(d, "array"); err != nil {
					return nil, err
				}
				return out, nil
			}
		}
	}
}

// readChardata reads text content up to the matching EndElement.
func readChardata(d *xml.Decoder, start xml.StartElement) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return "", fmt.Errorf("xmlrpc: read <%s>: %w", start.Name.Local, err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return "", fmt.Errorf("xmlrpc: expected </%s>, got </%s>", start.Name.Local, t.Name.Local)
			}
			return b.String(), nil
		case xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		case xml.StartElement:
			return "", fmt.Errorf("xmlrpc: <%s> must be text-only, saw <%s>", start.Name.Local, t.Name.Local)
		}
	}
}

// consumeCloseOrSelfClose absorbs the closing tag of start. Self-closing
// elements (<nil/>) do not emit a separate EndElement in Go's decoder —
// they're surfaced via the next StartElement/EndElement of the parent.
// For safety we peek a single token and require the matching EndElement.
func consumeCloseOrSelfClose(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("xmlrpc: close <%s>: %w", start.Name.Local, err)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return fmt.Errorf("xmlrpc: expected </%s>, got </%s>", start.Name.Local, t.Name.Local)
			}
			return nil
		case xml.CharData, xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		case xml.StartElement:
			return fmt.Errorf("xmlrpc: <%s/> must be empty, saw <%s>", start.Name.Local, t.Name.Local)
		}
	}
}

// expectEnd skips trivia until the matching EndElement is seen.
func expectEnd(d *xml.Decoder, local string) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("xmlrpc: expect </%s>: %w", local, err)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local != local {
				return fmt.Errorf("xmlrpc: expected </%s>, got </%s>", local, t.Name.Local)
			}
			return nil
		case xml.CharData, xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		case xml.StartElement:
			return fmt.Errorf("xmlrpc: stray <%s> before </%s>", t.Name.Local, local)
		}
	}
}
