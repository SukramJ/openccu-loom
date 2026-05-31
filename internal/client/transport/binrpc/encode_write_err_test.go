// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests that exercise write-error branches in the encoder by injecting a
// failing io.Writer after N bytes.

package binrpc

import (
	"errors"
	"io"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// failWriter fails after writing N bytes.
type failWriter struct {
	n   int
	err error
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	if len(p) <= f.n {
		f.n -= len(p)
		return len(p), nil
	}
	written := f.n
	f.n = 0
	return written, f.err
}

var errIO = errors.New("injected write error")

// failAt returns a *failWriter that lets through 'skip' bytes then fails.
func failAt(skip int) io.Writer {
	return &failWriter{n: skip, err: errIO}
}

// --- writeFrame header write error ---

func TestWriteResponseFailsOnHeaderWrite(t *testing.T) {
	// Fail after 0 bytes → header write fails.
	err := WriteResponse(failAt(0), xmlrpc.IntValue(1))
	if err == nil {
		t.Error("expected write error")
	}
}

func TestWriteResponseFailsOnPayloadWrite(t *testing.T) {
	// Fail after 8 bytes (header succeeds, payload fails).
	err := WriteResponse(failAt(8), xmlrpc.IntValue(1))
	if err == nil {
		t.Error("expected payload write error")
	}
}

// --- writeInt: tag + value write error ---

func TestWriteIntTagFails(t *testing.T) {
	// Fail right after header (offset 8) → tag write for Int fails.
	err := WriteResponse(failAt(8), xmlrpc.IntValue(99))
	if err == nil {
		t.Error("expected error when int type-tag write fails")
	}
}

func TestWriteIntValueFails(t *testing.T) {
	// header(8) + type_tag(4) → value int32 write fails.
	err := WriteResponse(failAt(8+4), xmlrpc.IntValue(99))
	if err == nil {
		t.Error("expected error when int value write fails")
	}
}

// --- writeBool: tag + value byte write error ---

func TestWriteBoolTagFails(t *testing.T) {
	err := WriteResponse(failAt(8), xmlrpc.BoolValue(true))
	if err == nil {
		t.Error("expected error when bool type-tag write fails")
	}
}

func TestWriteBoolValueByteFails(t *testing.T) {
	// header(8) + type_tag(4) succeeds → value byte write fails.
	err := WriteResponse(failAt(8+4), xmlrpc.BoolValue(true))
	if err == nil {
		t.Error("expected error when bool value byte write fails")
	}
}

// --- writeString: tag + raw string ---

func TestWriteStringTagFails(t *testing.T) {
	err := WriteResponse(failAt(8), xmlrpc.StringValue("hello"))
	if err == nil {
		t.Error("expected error when string type tag write fails")
	}
}

func TestWriteStringLengthFails(t *testing.T) {
	// header(8) + type_tag(4) → length u32 write fails.
	err := WriteResponse(failAt(8+4), xmlrpc.StringValue("hello"))
	if err == nil {
		t.Error("expected error when string length write fails")
	}
}

func TestWriteStringBytesFail(t *testing.T) {
	// header(8) + type_tag(4) + length(4) → byte payload write fails.
	err := WriteResponse(failAt(8+4+4), xmlrpc.StringValue("hello"))
	if err == nil {
		t.Error("expected error when string bytes write fails")
	}
}

// --- writeDouble: tag + mantissa + exp write error ---

func TestWriteDoubleTagFails(t *testing.T) {
	err := WriteResponse(failAt(8), xmlrpc.DoubleValue(1.5))
	if err == nil {
		t.Error("expected error when double type-tag write fails")
	}
}

func TestWriteDoubleMantissaFails(t *testing.T) {
	// header(8) + type_tag(4) → mantissa int32 write fails.
	err := WriteResponse(failAt(8+4), xmlrpc.DoubleValue(1.5))
	if err == nil {
		t.Error("expected error when double mantissa write fails")
	}
}

func TestWriteDoubleExpFails(t *testing.T) {
	// header(8) + type_tag(4) + mantissa(4) → exp int32 write fails.
	err := WriteResponse(failAt(8+4+4), xmlrpc.DoubleValue(1.5))
	if err == nil {
		t.Error("expected error when double exponent write fails")
	}
}

// --- writeStruct: tag + count + member errors ---

func TestWriteStructTagFails(t *testing.T) {
	err := WriteResponse(failAt(8), xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "X", Value: xmlrpc.IntValue(1)},
	}})
	if err == nil {
		t.Error("expected error when struct type-tag write fails")
	}
}

func TestWriteStructCountFails(t *testing.T) {
	// header(8) + type_tag(4) → count write fails.
	err := WriteResponse(failAt(8+4), xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "X", Value: xmlrpc.IntValue(1)},
	}})
	if err == nil {
		t.Error("expected error when struct count write fails")
	}
}

func TestWriteStructMemberNameFailsViaResponse(t *testing.T) {
	// header(8) + type_tag(4) + count(4) → member name length fails.
	err := WriteResponse(failAt(8+4+4), xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "X", Value: xmlrpc.IntValue(1)},
	}})
	if err == nil {
		t.Error("expected error when struct member name write fails")
	}
}

// --- writeArray: tag + count + element errors ---

func TestWriteArrayTagFails(t *testing.T) {
	err := WriteResponse(failAt(8), xmlrpc.ArrayValue{xmlrpc.IntValue(1)})
	if err == nil {
		t.Error("expected error when array type-tag write fails")
	}
}

func TestWriteArrayCountFails(t *testing.T) {
	// header(8) + type_tag(4) → count write fails.
	err := WriteResponse(failAt(8+4), xmlrpc.ArrayValue{xmlrpc.IntValue(1)})
	if err == nil {
		t.Error("expected error when array count write fails")
	}
}

func TestWriteArrayElementFailsViaResponse(t *testing.T) {
	// header(8) + type_tag(4) + count(4) → element type-tag write fails.
	err := WriteResponse(failAt(8+4+4), xmlrpc.ArrayValue{xmlrpc.IntValue(1)})
	if err == nil {
		t.Error("expected error when array element write fails")
	}
}

// --- WriteFault: failing writer ---

func TestWriteFaultFailsOnWrite(t *testing.T) {
	err := WriteFault(failAt(0), &hmerr.XMLRPCFault{Code: -1, Message: "err"})
	if err == nil {
		t.Error("expected write error from WriteFault")
	}
}

// --- WriteRequest: failing writer ---

func TestWriteRequestFailsOnWrite(t *testing.T) {
	err := WriteRequest(failAt(0), "ping", nil)
	if err == nil {
		t.Error("expected write error from WriteRequest")
	}
}

// --- writeParamArray: nil param element ---

func TestWriteParamArrayNilElementFails(t *testing.T) {
	// writeParamArray is called from WriteRequest; pass a nil inside the slice.
	var buf failAt0Writer
	err := WriteRequest(&buf, "ping", []xmlrpc.Value{nil})
	if err == nil {
		t.Error("nil param element should fail")
	}
}

type failAt0Writer struct{}

func (*failAt0Writer) Write(_ []byte) (int, error) { return 0, io.ErrClosedPipe }
