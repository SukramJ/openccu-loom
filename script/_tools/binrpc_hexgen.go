// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build hexgen

// binrpc_hexgen prints canonical wire bytes for the inputs we use as
// regression anchors in internal/client/transport/binrpc/codec_test.go.
//
// Run with:
//
//	go run -tags hexgen ./script/_tools/binrpc_hexgen.go
//
// Output is paste-ready into the test table.
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func dumpResponse(label string, v xmlrpc.Value) {
	var buf bytes.Buffer
	if err := binrpc.WriteResponse(&buf, v); err != nil {
		panic(err)
	}
	full := buf.Bytes()
	body := full[8:]
	fmt.Printf("// %s\n", label)
	fmt.Printf("// full:  %s\n", spaceHex(full))
	fmt.Printf("// body:  %s\n\n", spaceHex(body))
}

func dumpRequest(label, method string, params []xmlrpc.Value) {
	var buf bytes.Buffer
	if err := binrpc.WriteRequest(&buf, method, params); err != nil {
		panic(err)
	}
	fmt.Printf("// %s\n", label)
	fmt.Printf("// full:  %s\n\n", spaceHex(buf.Bytes()))
}

func dumpFault(label string, code int, msg string) {
	var buf bytes.Buffer
	if err := binrpc.WriteFault(&buf, &hmerr.XMLRPCFault{Code: code, Message: msg}); err != nil {
		panic(err)
	}
	fmt.Printf("// %s\n", label)
	fmt.Printf("// full:  %s\n\n", spaceHex(buf.Bytes()))
}

func spaceHex(b []byte) string {
	s := hex.EncodeToString(b)
	var out bytes.Buffer
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(s[i : i+2])
	}
	return out.String()
}

func main() {
	fmt.Println("=== Value-only canonical bytes (response body, type tag + payload) ===")
	dumpResponse(`String "openccu-loom"`, xmlrpc.StringValue("openccu-loom"))
	dumpResponse(`String ISO-8859-1 "Türöffner"`, xmlrpc.StringValue("Türöffner"))
	dumpResponse(`Integer 7`, xmlrpc.IntValue(7))
	dumpResponse(`Bool false`, xmlrpc.BoolValue(false))
	dumpResponse(`Bool true`, xmlrpc.BoolValue(true))
	dumpResponse(`Double 1.5`, xmlrpc.DoubleValue(1.5))
	dumpResponse(`Double -3.25`, xmlrpc.DoubleValue(-3.25))
	dumpResponse(`Struct VALUE=0.5`, xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "VALUE", Value: xmlrpc.DoubleValue(0.5)},
	}})
	dumpResponse(`Array [1, 2, 3]`, xmlrpc.ArrayValue{
		xmlrpc.IntValue(1), xmlrpc.IntValue(2), xmlrpc.IntValue(3),
	})

	fmt.Println("=== Request canonical frames (header + payload) ===")
	dumpRequest("system.listMethods (no params)", "system.listMethods", nil)
	dumpRequest("init test-net 192.0.2.1:8129",
		"init",
		[]xmlrpc.Value{
			xmlrpc.StringValue("xmlrpc_bin://192.0.2.1:8129"),
			xmlrpc.StringValue("openccu-loom-test"),
		})
	dumpRequest("system.multicall single event",
		"system.multicall",
		[]xmlrpc.Value{
			xmlrpc.ArrayValue{
				xmlrpc.StructValue{Members: []xmlrpc.Member{
					{Name: "methodName", Value: xmlrpc.StringValue("event")},
					{Name: "params", Value: xmlrpc.ArrayValue{
						xmlrpc.StringValue("GHM"),
						xmlrpc.StringValue("GHM0000001:1"),
						xmlrpc.StringValue("STATE"),
						xmlrpc.BoolValue(true),
					}},
				}},
			},
		})

	fmt.Println("=== Fault canonical frames ===")
	dumpFault("Fault -1 / 'test fault'", -1, "test fault")
}
