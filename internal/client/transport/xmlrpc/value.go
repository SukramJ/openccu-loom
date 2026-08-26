// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind identifies the concrete XML-RPC value type held behind the
// [Value] interface.
type Kind int

// XML-RPC value kinds. Order is stable (used in serialised forms;
// wire shapes mirror pkg/hmproto definitions).
const (
	KindNil Kind = iota
	KindInt
	KindBool
	KindString
	KindDouble
	KindDateTime
	KindBase64
	KindStruct
	KindArray
)

// String returns the XML token for the kind (sans the leading/closing
// "<>" chars); useful in error messages.
func (k Kind) String() string {
	switch k {
	case KindNil:
		return "nil"
	case KindInt:
		return "int"
	case KindBool:
		return "boolean"
	case KindString:
		return "string"
	case KindDouble:
		return "double"
	case KindDateTime:
		return "dateTime.iso8601"
	case KindBase64:
		return "base64"
	case KindStruct:
		return "struct"
	case KindArray:
		return "array"
	default:
		return "unknown"
	}
}

// Value is the sum type of every XML-RPC value. Exactly one concrete
// implementor is returned by the decoder and passed into the encoder.
//
// Implementations serialise themselves including the wrapping
// <value>…</value> element. Decoding is handled by [DecodeValue], which
// peeks the inner element tag and dispatches to the matching type.
type Value interface {
	// Kind returns the value's type tag.
	Kind() Kind
	// MarshalXML implements xml.Marshaler. Implementations write the
	// full <value>…</value> envelope.
	MarshalXML(e *xml.Encoder, start xml.StartElement) error
}

// valueEnvelope is the XML element used as StartElement for every
// Value's MarshalXML. Defined once to avoid string churn.
var valueEnvelope = xml.StartElement{Name: xml.Name{Local: "value"}}

// ---------------------------------------------------------------------
// Concrete value types
// ---------------------------------------------------------------------

// NilValue represents an XML-RPC <nil/>. CCU responses often use an
// empty <params/> to signal "void"; NilValue is the canonical placeholder.
type NilValue struct{}

// Kind implements [Value].
func (NilValue) Kind() Kind { return KindNil }

// MarshalXML implements xml.Marshaler.
func (NilValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	return writeTagged(e, "nil", "")
}

// IntValue wraps a 32-bit XML-RPC <int>/<i4>.
type IntValue int32

// Kind implements [Value].
func (IntValue) Kind() Kind { return KindInt }

// MarshalXML implements xml.Marshaler. Emits <int>; the CCU firmware
// (eQ-3 ReGaHss) accepts only this form on writes — <i4> is silently
// rejected with xml-rpc fault -5 ("Invalid parameter or value"), even
// though both tags are valid per the XML-RPC spec.
func (v IntValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	return writeTagged(e, "int", strconv.FormatInt(int64(v), 10))
}

// BoolValue wraps an XML-RPC <boolean>.
type BoolValue bool

// Kind implements [Value].
func (BoolValue) Kind() Kind { return KindBool }

// MarshalXML implements xml.Marshaler.
func (v BoolValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	s := "0"
	if v {
		s = "1"
	}
	return writeTagged(e, "boolean", s)
}

// StringValue wraps an XML-RPC <string> (or bare chardata).
type StringValue string

// Kind implements [Value].
func (StringValue) Kind() Kind { return KindString }

// MarshalXML implements xml.Marshaler. Writes the explicit <string> form
// for determinism; the short chardata form is only a valid input shape.
func (v StringValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	return writeTagged(e, "string", string(v))
}

// DoubleValue wraps an XML-RPC <double>.
type DoubleValue float64

// Kind implements [Value].
func (DoubleValue) Kind() Kind { return KindDouble }

// MarshalXML implements xml.Marshaler. The CCU's putParamset validator
// requires a literal decimal point even for whole numbers — "<double>1</double>"
// is rejected with xml-rpc fault -5, "<double>1.0</double>" is accepted.
// We therefore force at least one fractional digit.
func (v DoubleValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	s := strconv.FormatFloat(float64(v), 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return writeTagged(e, "double", s)
}

// DateTimeValue wraps an XML-RPC <dateTime.iso8601>. The CCU sends the
// unqualified ISO-8601 form ("YYYYMMDDTHH:MM:SS").
type DateTimeValue time.Time

// Kind implements [Value].
func (DateTimeValue) Kind() Kind { return KindDateTime }

// ISO8601CompactLayout is the CCU's canonical dateTime.iso8601 format.
const ISO8601CompactLayout = "20060102T15:04:05"

// MarshalXML implements xml.Marshaler.
func (v DateTimeValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	return writeTagged(e, "dateTime.iso8601", time.Time(v).Format(ISO8601CompactLayout))
}

// Time returns the wrapped time.Time.
func (v DateTimeValue) Time() time.Time { return time.Time(v) }

// Base64Value wraps an XML-RPC <base64>.
type Base64Value []byte

// Kind implements [Value].
func (Base64Value) Kind() Kind { return KindBase64 }

// MarshalXML implements xml.Marshaler.
func (v Base64Value) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	return writeTagged(e, "base64", base64.StdEncoding.EncodeToString([]byte(v)))
}

// StructValue wraps an XML-RPC <struct> as an ordered member list.
// Order matters for the CCU when constructing paramset responses.
type StructValue struct {
	Members []Member
}

// Member is one <member>…</member> inside a StructValue.
type Member struct {
	Name  string
	Value Value
}

// Kind implements [Value].
func (StructValue) Kind() Kind { return KindStruct }

// Get returns the member matching name and reports whether it existed.
func (s StructValue) Get(name string) (Value, bool) {
	for _, m := range s.Members {
		if m.Name == name {
			return m.Value, true
		}
	}
	return nil, false
}

// MarshalXML implements xml.Marshaler.
func (s StructValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	if err := e.EncodeToken(valueEnvelope); err != nil {
		return err
	}
	structStart := xml.StartElement{Name: xml.Name{Local: "struct"}}
	if err := e.EncodeToken(structStart); err != nil {
		return err
	}
	for _, m := range s.Members {
		memberStart := xml.StartElement{Name: xml.Name{Local: "member"}}
		if err := e.EncodeToken(memberStart); err != nil {
			return err
		}
		if err := writeBareElement(e, "name", m.Name); err != nil {
			return err
		}
		if m.Value == nil {
			return fmt.Errorf("xmlrpc: struct member %q has nil value", m.Name)
		}
		if err := m.Value.MarshalXML(e, valueEnvelope); err != nil {
			return err
		}
		if err := e.EncodeToken(memberStart.End()); err != nil {
			return err
		}
	}
	if err := e.EncodeToken(structStart.End()); err != nil {
		return err
	}
	return e.EncodeToken(valueEnvelope.End())
}

// ArrayValue wraps an XML-RPC <array>.
type ArrayValue []Value

// Kind implements [Value].
func (ArrayValue) Kind() Kind { return KindArray }

// MarshalXML implements xml.Marshaler.
func (a ArrayValue) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	if err := e.EncodeToken(valueEnvelope); err != nil {
		return err
	}
	arrayStart := xml.StartElement{Name: xml.Name{Local: "array"}}
	if err := e.EncodeToken(arrayStart); err != nil {
		return err
	}
	dataStart := xml.StartElement{Name: xml.Name{Local: "data"}}
	if err := e.EncodeToken(dataStart); err != nil {
		return err
	}
	for i, v := range a {
		if v == nil {
			return fmt.Errorf("xmlrpc: array element %d is nil", i)
		}
		if err := v.MarshalXML(e, valueEnvelope); err != nil {
			return err
		}
	}
	if err := e.EncodeToken(dataStart.End()); err != nil {
		return err
	}
	if err := e.EncodeToken(arrayStart.End()); err != nil {
		return err
	}
	return e.EncodeToken(valueEnvelope.End())
}

// ---------------------------------------------------------------------
// Helpers shared by the concrete types
// ---------------------------------------------------------------------

// writeTagged emits <value><tag>content</tag></value>. Used by every
// leaf value type so they share identical framing.
func writeTagged(e *xml.Encoder, tag, content string) error {
	if err := e.EncodeToken(valueEnvelope); err != nil {
		return err
	}
	inner := xml.StartElement{Name: xml.Name{Local: tag}}
	if err := e.EncodeToken(inner); err != nil {
		return err
	}
	if content != "" {
		if err := e.EncodeToken(xml.CharData(content)); err != nil {
			return err
		}
	}
	if err := e.EncodeToken(inner.End()); err != nil {
		return err
	}
	return e.EncodeToken(valueEnvelope.End())
}

// writeBareElement emits <tag>content</tag> — without the <value>
// wrapper that writeTagged adds. Used for framing elements like
// <methodName> and the <name> child of <member>.
func writeBareElement(e *xml.Encoder, tag, content string) error {
	start := xml.StartElement{Name: xml.Name{Local: tag}}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	if content != "" {
		if err := e.EncodeToken(xml.CharData(content)); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

// String renders a compact debug representation. Purposefully concise —
// we do not guarantee the format.
func stringify(v Value) string {
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case NilValue:
		return "nil"
	case IntValue:
		return strconv.FormatInt(int64(x), 10)
	case BoolValue:
		if x {
			return "true"
		}
		return "false"
	case StringValue:
		return strconv.Quote(string(x))
	case DoubleValue:
		return strconv.FormatFloat(float64(x), 'g', -1, 64)
	case DateTimeValue:
		return time.Time(x).Format(ISO8601CompactLayout)
	case Base64Value:
		return base64.StdEncoding.EncodeToString([]byte(x))
	case StructValue:
		var b strings.Builder
		b.WriteByte('{')
		for i, m := range x.Members {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(m.Name)
			b.WriteByte(':')
			b.WriteString(stringify(m.Value))
		}
		b.WriteByte('}')
		return b.String()
	case ArrayValue:
		var b strings.Builder
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(stringify(e))
		}
		b.WriteByte(']')
		return b.String()
	default:
		return fmt.Sprintf("<%T>", v)
	}
}

// Format renders the value in debug form (see [stringify]).
func Format(v Value) string { return stringify(v) }
