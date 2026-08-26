// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"encoding/json"
	"testing"
)

// FuzzJSONRPCResponseEnvelope fuzzes json.Unmarshal into the on-the-wire
// response struct. Seeds include valid success responses, fault objects,
// large numbers, null results, and arrays as the result field. The fuzz
// body asserts that no panic occurs; errors from json.Unmarshal and any
// subsequent processing are acceptable.
func FuzzJSONRPCResponseEnvelope(f *testing.F) {
	// Positive corpus — well-formed response envelopes.
	f.Add([]byte(`{"result":"ok","error":null}`))
	f.Add([]byte(`{"result":42,"error":null}`))
	f.Add([]byte(`{"result":true,"error":null}`))
	f.Add([]byte(`{"result":null,"error":null}`))
	f.Add([]byte(`{"result":{"key":"value"},"error":null}`))
	f.Add([]byte(`{"result":["a","b","c"],"error":null}`))
	f.Add([]byte(`{"result":"SESSION_ID_12345","error":null,"version":"1.1"}`))
	// Fault / error responses.
	f.Add([]byte(`{"result":null,"error":{"code":-3,"message":"not found"}}`))
	f.Add([]byte(`{"result":null,"error":{"code":-8,"message":"duty cycle","data":"extra"}}`))
	f.Add([]byte(`{"result":null,"error":{"code":0,"message":""}}`))
	// Edge cases.
	f.Add([]byte(`{"result":1.7976931348623157e+308,"error":null}`))  // max float64
	f.Add([]byte(`{"result":-1.7976931348623157e+308,"error":null}`)) // min float64
	f.Add([]byte(`{"result":9007199254740993,"error":null}`))         // int > float64 precision
	f.Add([]byte(`{}`))                                               // empty object — all zero values

	// Negative corpus — malformed inputs.
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"result":` + string(make([]byte, 1000)) + `}`)) // garbage value bytes
	f.Add([]byte(`{"result":{"deeply":{"nested":{"key":1}}},"error":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic unmarshalling response envelope with input %q: %v", data, r)
			}
		}()

		var resp response
		if err := json.Unmarshal(data, &resp); err != nil {
			return // parse errors are acceptable; panics are not
		}

		// Inspect the decoded fields to exercise any lazy-decode paths.
		_ = resp.Version
		if resp.Error != nil {
			_ = resp.Error.Code
			_ = resp.Error.Message
			_ = resp.Error.Data
		}
		// resp.Result is json.RawMessage — a secondary Unmarshal would be
		// application-specific; we only test the envelope layer here.
		_ = resp.Result
	})
}

// FuzzJSONRPCErrorObject fuzzes json.Unmarshal into the wireError struct
// specifically. Seeds stress the code/message/data fields with valid and
// invalid JSON. Errors are acceptable; panics are not.
func FuzzJSONRPCErrorObject(f *testing.F) {
	// Positive corpus — well-formed error objects.
	f.Add([]byte(`{"code":-3,"message":"not found"}`))
	f.Add([]byte(`{"code":-8,"message":"duty cycle limit","data":"extra detail"}`))
	f.Add([]byte(`{"code":0,"message":""}`))
	f.Add([]byte(`{"code":2147483647,"message":"max int32"}`))
	f.Add([]byte(`{"code":-2147483648,"message":"min int32"}`))
	f.Add([]byte(`{"code":1,"message":"` + string(make([]byte, 256)) + `"}`)) // long message (NUL bytes)
	f.Add([]byte(`{"code":1,"message":"Schaltkanal","data":""}`))
	// Edge cases.
	f.Add([]byte(`{}`))                    // all zero values
	f.Add([]byte(`{"code":1}`))            // missing message
	f.Add([]byte(`{"message":"no code"}`)) // missing code

	// Negative corpus.
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"code":"string-not-int","message":"type mismatch"}`))
	f.Add([]byte(`{"code":1.5,"message":"float code"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic unmarshalling wireError with input %q: %v", data, r)
			}
		}()

		var we wireError
		if err := json.Unmarshal(data, &we); err != nil {
			return // parse errors are acceptable; panics are not
		}

		// Touch each field to ensure no lazy evaluation panics.
		_ = we.Code
		_ = we.Message
		_ = we.Data
	})
}
