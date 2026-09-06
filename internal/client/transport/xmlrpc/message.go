// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
)

// DefaultResponseLimit bounds how many bytes we accept from a CCU
// response. 10 MB matches the CCU's own typical hard limit.
const DefaultResponseLimit = 10 * 1024 * 1024

// MethodCall is the client-side request envelope.
type MethodCall struct {
	Method string
	Params []Value
}

// MethodResponse is the server-side reply envelope. Exactly one of
// Params / Fault is populated on a correctly-formed response.
type MethodResponse struct {
	Params []Value
	Fault  *hmerr.XMLRPCFault
}

// EncodeCall writes mc to w using the CCU-canonical ISO-8859-1 encoding.
// Every string body is assumed to be UTF-8 at the Go level; the charmap
// encoder converts at the byte-emission boundary.
func EncodeCall(w io.Writer, mc *MethodCall) error {
	if mc == nil {
		return errors.New("xmlrpc: EncodeCall: nil MethodCall")
	}
	if mc.Method == "" {
		return errors.New("xmlrpc: EncodeCall: Method is empty")
	}
	if err := checkEncodable(mc.Method); err != nil {
		return err
	}
	for _, p := range mc.Params {
		if err := checkValueEncodable(p); err != nil {
			return err
		}
	}
	ew := charmap.ISO8859_1.NewEncoder().Writer(w)
	if _, err := io.WriteString(ew, xmlPreamble); err != nil {
		return err
	}
	enc := xml.NewEncoder(ew)
	root := xml.StartElement{Name: xml.Name{Local: "methodCall"}}
	if err := enc.EncodeToken(root); err != nil {
		return err
	}
	if err := writeBareElement(enc, "methodName", mc.Method); err != nil {
		return err
	}
	if err := encodeParams(enc, mc.Params); err != nil {
		return err
	}
	if err := enc.EncodeToken(root.End()); err != nil {
		return err
	}
	return enc.Flush()
}

// EncodeResponse writes mr to w. If Fault is set, Params is ignored; if
// both are nil, an empty <params/> is emitted (CCU "void" convention).
func EncodeResponse(w io.Writer, mr *MethodResponse) error {
	if mr == nil {
		return errors.New("xmlrpc: EncodeResponse: nil MethodResponse")
	}
	if mr.Fault != nil {
		if err := checkEncodable(mr.Fault.Message); err != nil {
			return err
		}
	}
	for _, p := range mr.Params {
		if err := checkValueEncodable(p); err != nil {
			return err
		}
	}
	ew := charmap.ISO8859_1.NewEncoder().Writer(w)
	if _, err := io.WriteString(ew, xmlPreamble); err != nil {
		return err
	}
	enc := xml.NewEncoder(ew)
	root := xml.StartElement{Name: xml.Name{Local: "methodResponse"}}
	if err := enc.EncodeToken(root); err != nil {
		return err
	}
	if mr.Fault != nil {
		if err := encodeFault(enc, mr.Fault); err != nil {
			return err
		}
	} else if err := encodeParams(enc, mr.Params); err != nil {
		return err
	}
	if err := enc.EncodeToken(root.End()); err != nil {
		return err
	}
	return enc.Flush()
}

// DecodeCall reads a <methodCall> from r. Charset handling accepts both
// UTF-8 and the ISO-8859-1 the CCU emits by default.
func DecodeCall(r io.Reader) (*MethodCall, error) {
	dec := newDecoder(r)
	start, err := findStart(dec, "methodCall")
	if err != nil {
		return nil, err
	}

	var out MethodCall
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: decode methodCall: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "methodName":
				s, err := readChardata(dec, t)
				if err != nil {
					return nil, err
				}
				out.Method = s
			case "params":
				out.Params, err = decodeParams(dec, t)
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("xmlrpc: methodCall: unexpected <%s>", t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return nil, fmt.Errorf("xmlrpc: methodCall: unexpected </%s>", t.Name.Local)
			}
			if out.Method == "" {
				return nil, errors.New("xmlrpc: methodCall missing <methodName>")
			}
			return &out, nil
		}
	}
}

// DecodeResponse reads a <methodResponse> from r. A fault maps to a
// populated [MethodResponse.Fault]; callers typically propagate it as
// an error.
func DecodeResponse(r io.Reader) (*MethodResponse, error) {
	dec := newDecoder(r)
	start, err := findStart(dec, "methodResponse")
	if err != nil {
		return nil, err
	}

	var out MethodResponse
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: decode methodResponse: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "params":
				out.Params, err = decodeParams(dec, t)
				if err != nil {
					return nil, err
				}
			case "fault":
				out.Fault, err = decodeFault(dec, t)
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("xmlrpc: methodResponse: unexpected <%s>", t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return nil, fmt.Errorf("xmlrpc: methodResponse: unexpected </%s>", t.Name.Local)
			}
			return &out, nil
		}
	}
}

// maxUnencodableEcho bounds how much of an offending string the error
// echoes back: enough to identify the value, short enough for a log line.
const maxUnencodableEcho = 32

// checkEncodable refuses a string carrying a rune outside ISO-8859-1.
// The CCU is Latin-1 end to end and its XML parser resolves only the five
// named XML entities, so there is no faithful transliteration — refusing is
// correct, but the refusal has to be a sentinel the caller can match
// (a raw charmap failure surfaces as an opaque "encode call" error).
func checkEncodable(s string) error {
	for _, r := range s {
		if r > 0xFF {
			return fmt.Errorf("%w: %q", hmerr.ErrUnencodableString, truncateRunes(s, maxUnencodableEcho))
		}
	}
	return nil
}

// truncateRunes shortens s to at most n runes without splitting one.
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// checkValueEncodable walks a value's string carriers — including struct
// member names and nested arrays — and applies [checkEncodable] to each.
func checkValueEncodable(v Value) error {
	switch t := v.(type) {
	case StringValue:
		return checkEncodable(string(t))
	case StructValue:
		for _, m := range t.Members {
			if err := checkEncodable(m.Name); err != nil {
				return err
			}
			if err := checkValueEncodable(m.Value); err != nil {
				return err
			}
		}
	case ArrayValue:
		for _, item := range t {
			if err := checkValueEncodable(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

const xmlPreamble = `<?xml version="1.0" encoding="ISO-8859-1"?>` + "\n"

// newDecoder returns an xml.Decoder with CCU-friendly charset handling.
func newDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.CharsetReader = charset.NewReaderLabel
	return d
}

// findStart advances d to the first StartElement whose local name
// matches expected. Anything but whitespace/comments/prolog raises.
func findStart(d *xml.Decoder, expected string) (xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return xml.StartElement{}, fmt.Errorf("xmlrpc: find <%s>: %w", expected, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != expected {
				return xml.StartElement{}, fmt.Errorf("xmlrpc: expected <%s>, got <%s>", expected, t.Name.Local)
			}
			return t, nil
		case xml.CharData, xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		case xml.EndElement:
			return xml.StartElement{}, fmt.Errorf("xmlrpc: unexpected </%s> while looking for <%s>", t.Name.Local, expected)
		}
	}
}

// encodeParams writes <params><param><value>…</value></param>…</params>.
func encodeParams(e *xml.Encoder, params []Value) error {
	paramsStart := xml.StartElement{Name: xml.Name{Local: "params"}}
	if err := e.EncodeToken(paramsStart); err != nil {
		return err
	}
	for i, p := range params {
		paramStart := xml.StartElement{Name: xml.Name{Local: "param"}}
		if err := e.EncodeToken(paramStart); err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("xmlrpc: param %d is nil", i)
		}
		if err := p.MarshalXML(e, valueEnvelope); err != nil {
			return err
		}
		if err := e.EncodeToken(paramStart.End()); err != nil {
			return err
		}
	}
	return e.EncodeToken(paramsStart.End())
}

// decodeParams reads the interior of a <params> element up to its closing tag.
func decodeParams(d *xml.Decoder, start xml.StartElement) ([]Value, error) {
	var out []Value
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: decode params: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "param" {
				return nil, fmt.Errorf("xmlrpc: params: unexpected <%s>", t.Name.Local)
			}
			v, err := decodeParam(d)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return nil, fmt.Errorf("xmlrpc: params: unexpected </%s>", t.Name.Local)
			}
			return out, nil
		}
	}
}

func decodeParam(d *xml.Decoder) (Value, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: decode param: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "value" {
				return nil, fmt.Errorf("xmlrpc: param: unexpected <%s>", t.Name.Local)
			}
			v, err := DecodeValue(d, t)
			if err != nil {
				return nil, err
			}
			if err := expectEnd(d, "param"); err != nil {
				return nil, err
			}
			return v, nil
		case xml.EndElement:
			if t.Name.Local == "param" {
				// Allow an empty <param/> to decode as NilValue — seen in
				// the wild on CCU "void" replies.
				return NilValue{}, nil
			}
		}
	}
}

// encodeFault writes a <fault> with a struct containing faultCode and
// faultString, matching the Spec.
func encodeFault(e *xml.Encoder, f *hmerr.XMLRPCFault) error {
	faultStart := xml.StartElement{Name: xml.Name{Local: "fault"}}
	if err := e.EncodeToken(faultStart); err != nil {
		return err
	}
	payload := StructValue{Members: []Member{
		{Name: "faultCode", Value: IntValue(int32(f.Code))}, //nolint:gosec // fault codes fit int32; see #20
		{Name: "faultString", Value: StringValue(f.Message)},
	}}
	if err := payload.MarshalXML(e, valueEnvelope); err != nil {
		return err
	}
	return e.EncodeToken(faultStart.End())
}

func decodeFault(d *xml.Decoder, start xml.StartElement) (*hmerr.XMLRPCFault, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("xmlrpc: decode fault: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "value" {
				return nil, fmt.Errorf("xmlrpc: fault: unexpected <%s>", t.Name.Local)
			}
			v, err := DecodeValue(d, t)
			if err != nil {
				return nil, err
			}
			code, err := StructField[IntValue](v, "faultCode")
			if err != nil {
				return nil, fmt.Errorf("xmlrpc: fault: %w", err)
			}
			msg, err := StructField[StringValue](v, "faultString")
			if err != nil {
				return nil, fmt.Errorf("xmlrpc: fault: %w", err)
			}
			if err := expectEnd(d, "fault"); err != nil {
				return nil, err
			}
			return &hmerr.XMLRPCFault{Code: int(code), Message: string(msg)}, nil
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil, errors.New("xmlrpc: <fault> missing <value>")
			}
		}
	}
}

// MarshalBytes is a convenience wrapper around EncodeCall returning the
// serialised request as a byte slice.
func MarshalBytes(mc *MethodCall) ([]byte, error) {
	var buf bytes.Buffer
	if err := EncodeCall(&buf, mc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
