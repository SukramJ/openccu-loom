// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// errWriteFail is the sentinel error injected by errWriter.
var errWriteFail = errors.New("write failed")

// errWriter is an io.Writer that always fails after n successful bytes.
type errWriter struct {
	n   int // bytes written successfully before failing
	err error
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, w.err
	}
	if len(p) > w.n {
		written := w.n
		w.n = 0
		return written, w.err
	}
	w.n -= len(p)
	return len(p), nil
}

// immediateErrWriter always fails on the first Write.
func immediateErrWriter() *errWriter {
	return &errWriter{n: 0, err: errWriteFail}
}

// TestEncodeCallWriterError exercises the early-return error paths
// in EncodeCall via a writer that immediately fails.
func TestEncodeCallWriterError(t *testing.T) {
	t.Parallel()

	mc := &MethodCall{Method: "test", Params: []Value{StringValue("x")}}
	if err := EncodeCall(immediateErrWriter(), mc); err == nil {
		t.Fatal("EncodeCall with failing writer must return error")
	}
}

// TestEncodeResponseWriterError exercises the early-return error paths
// in EncodeResponse via a writer that immediately fails.
func TestEncodeResponseWriterError(t *testing.T) {
	t.Parallel()

	mr := &MethodResponse{Params: []Value{IntValue(1)}}
	if err := EncodeResponse(immediateErrWriter(), mr); err == nil {
		t.Fatal("EncodeResponse with failing writer must return error")
	}
}

// TestEncodeResponseFaultWriterError exercises the encodeFault write-
// failure path.
func TestEncodeResponseFaultWriterError(t *testing.T) {
	t.Parallel()

	mr := &MethodResponse{Fault: &hmerr.XMLRPCFault{Code: -1, Message: "err"}}
	if err := EncodeResponse(immediateErrWriter(), mr); err == nil {
		t.Fatal("EncodeResponse(fault) with failing writer must return error")
	}
}

// TestDecodeParamUnexpectedElement exercises the "unexpected element inside
// <param>" error path in decodeParam (a <foo> tag instead of <value>).
func TestDecodeParamUnexpectedElement(t *testing.T) {
	t.Parallel()

	raw := `<?xml version="1.0"?><methodResponse><params><param><foo/></param></params></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("unexpected element inside <param> must produce error")
	}
}
