// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package binrpc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"golang.org/x/text/encoding/charmap"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// WriteRequest frames a single BIN-RPC request to w.
func WriteRequest(w io.Writer, method string, params []xmlrpc.Value) error {
	if method == "" {
		return errors.New("binrpc: WriteRequest: empty method")
	}
	var payload bytes.Buffer
	if err := writeRawString(&payload, method); err != nil {
		return fmt.Errorf("binrpc: encode method: %w", err)
	}
	if err := writeParamArray(&payload, params); err != nil {
		return err
	}
	return writeFrame(w, msgTypeRequest, payload.Bytes())
}

// WriteResponse frames a single-value BIN-RPC response to w.
// A nil value is encoded as an empty string, matching CUxD convention.
func WriteResponse(w io.Writer, v xmlrpc.Value) error {
	var payload bytes.Buffer
	if v == nil {
		if err := writeValue(&payload, xmlrpc.StringValue("")); err != nil {
			return err
		}
	} else if err := writeValue(&payload, v); err != nil {
		return err
	}
	return writeFrame(w, msgTypeResponse, payload.Bytes())
}

// WriteFault frames a BIN-RPC fault packet. fault.Code and fault.Message
// are copied into a struct payload exactly as CUxD expects.
func WriteFault(w io.Writer, fault *hmerr.XMLRPCFault) error {
	if fault == nil {
		return errors.New("binrpc: WriteFault: nil fault")
	}
	payloadStruct := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "faultCode", Value: xmlrpc.IntValue(int32(fault.Code))}, //nolint:gosec // fault codes fit int32
		{Name: "faultString", Value: xmlrpc.StringValue(fault.Message)},
	}}
	var payload bytes.Buffer
	if err := writeValue(&payload, payloadStruct); err != nil {
		return err
	}
	return writeFrame(w, msgTypeFault, payload.Bytes())
}

// writeFrame emits the 8-byte header + payload.
func writeFrame(w io.Writer, msgType uint8, payload []byte) error {
	if int64(len(payload)) > MaxMessageSize {
		return fmt.Errorf("binrpc: payload exceeds %d bytes", MaxMessageSize)
	}
	header := make([]byte, 0, 8)
	header = append(header, marker[:]...)
	header = append(header, msgType)
	header = binary.BigEndian.AppendUint32(header, uint32(len(payload))) //nolint:gosec // bounds-checked above
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("binrpc: write header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("binrpc: write payload: %w", err)
	}
	return nil
}

// writeParamArray writes `<count:u32><value>…</value>*`.
func writeParamArray(w io.Writer, params []xmlrpc.Value) error {
	if err := binary.Write(w, binary.BigEndian, uint32(len(params))); err != nil { //nolint:gosec // len bound by int → u32 fits
		return fmt.Errorf("binrpc: write param count: %w", err)
	}
	for i, p := range params {
		if p == nil {
			return fmt.Errorf("binrpc: param %d is nil", i)
		}
		if err := writeValue(w, p); err != nil {
			return fmt.Errorf("binrpc: param %d: %w", i, err)
		}
	}
	return nil
}

// writeValue dispatches on the concrete value type and emits
// `<typeTag:u32><payload>`.
func writeValue(w io.Writer, v xmlrpc.Value) error {
	switch x := v.(type) {
	case xmlrpc.NilValue:
		// BIN-RPC has no nil tag; represent as an empty string.
		return writeString(w, "")
	case xmlrpc.IntValue:
		return writeInt(w, int32(x))
	case xmlrpc.BoolValue:
		return writeBool(w, bool(x))
	case xmlrpc.StringValue:
		return writeString(w, string(x))
	case xmlrpc.DoubleValue:
		return writeDouble(w, float64(x))
	case xmlrpc.StructValue:
		return writeStruct(w, x)
	case xmlrpc.ArrayValue:
		return writeArray(w, x)
	case xmlrpc.DateTimeValue, xmlrpc.Base64Value:
		return fmt.Errorf("binrpc: kind %s is not supported over BIN-RPC", v.Kind())
	default:
		return fmt.Errorf("binrpc: unknown value kind %T", v)
	}
}

func writeInt(w io.Writer, n int32) error {
	if err := binary.Write(w, binary.BigEndian, typeInt); err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, n)
}

func writeBool(w io.Writer, b bool) error {
	if err := binary.Write(w, binary.BigEndian, typeBool); err != nil {
		return err
	}
	var byt uint8
	if b {
		byt = 1
	}
	return binary.Write(w, binary.BigEndian, byt)
}

func writeString(w io.Writer, s string) error {
	if err := binary.Write(w, binary.BigEndian, typeString); err != nil {
		return err
	}
	return writeRawString(w, s)
}

// writeRawString writes `<length:u32><ISO-8859-1 bytes>` with no type tag.
// Used for the method name in a request and as the tail of a typeString.
func writeRawString(w io.Writer, s string) error {
	enc := charmap.ISO8859_1.NewEncoder()
	raw, err := enc.Bytes([]byte(s))
	if err != nil {
		return fmt.Errorf("binrpc: encode string to ISO-8859-1: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(raw))); err != nil { //nolint:gosec // string length bounded
		return err
	}
	_, err = w.Write(raw)
	return err
}

func writeDouble(w io.Writer, f float64) error {
	if err := binary.Write(w, binary.BigEndian, typeDouble); err != nil {
		return err
	}
	mant, exp, err := encodeDouble(f)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, mant); err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, exp)
}

func writeStruct(w io.Writer, s xmlrpc.StructValue) error {
	if err := binary.Write(w, binary.BigEndian, typeStruct); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(s.Members))); err != nil { //nolint:gosec // len bounded
		return err
	}
	for _, m := range s.Members {
		if err := writeRawString(w, m.Name); err != nil {
			return err
		}
		if m.Value == nil {
			return fmt.Errorf("binrpc: struct member %q is nil", m.Name)
		}
		if err := writeValue(w, m.Value); err != nil {
			return err
		}
	}
	return nil
}

func writeArray(w io.Writer, a xmlrpc.ArrayValue) error {
	if err := binary.Write(w, binary.BigEndian, typeArray); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(a))); err != nil { //nolint:gosec // len bounded
		return err
	}
	for i, v := range a {
		if v == nil {
			return fmt.Errorf("binrpc: array element %d is nil", i)
		}
		if err := writeValue(w, v); err != nil {
			return err
		}
	}
	return nil
}

// encodeDouble returns the (mantissa, exp) pair BIN-RPC expects:
//
//	value = mantissa * 2^exp / 2^30
//
// Zero is encoded as (0, 0) rather than running the logarithm. NaN and
// Inf are rejected — CUxD never sends them and the format cannot
// represent them precisely.
func encodeDouble(val float64) (mantissa, exponent int32, err error) {
	if math.IsNaN(val) {
		return 0, 0, errors.New("binrpc: NaN is not representable")
	}
	if math.IsInf(val, 0) {
		return 0, 0, errors.New("binrpc: Inf is not representable")
	}
	if val == 0 {
		return 0, 0, nil
	}
	exp := math.Floor(math.Log(math.Abs(val))/math.Ln2) + 1
	mant := math.Floor((val * math.Pow(2, -exp)) * mantissaScale)
	if exp < math.MinInt32 || exp > math.MaxInt32 {
		return 0, 0, fmt.Errorf("binrpc: exponent out of range: %g", exp)
	}
	if mant < math.MinInt32 || mant > math.MaxInt32 {
		return 0, 0, fmt.Errorf("binrpc: mantissa out of range: %g", mant)
	}
	return int32(mant), int32(exp), nil
}
