// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package binrpc

import (
	"bytes"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// mustHex turns a space-separated hex string into bytes. Used to anchor
// the canonical BIN-RPC wire form against pre-computed byte sequences.
// The expected hex strings were produced by script/_tools/binrpc_hexgen.go
// and cross-verified against a live CUxD instance via
// tests/integration/binrpc_external_test.go (build tag `cuxd_live`).
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestRoundTripPrimitives(t *testing.T) {
	cases := []xmlrpc.Value{
		xmlrpc.IntValue(0),
		xmlrpc.IntValue(42),
		xmlrpc.IntValue(-1_000_000),
		xmlrpc.BoolValue(true),
		xmlrpc.BoolValue(false),
		xmlrpc.StringValue(""),
		xmlrpc.StringValue("hello"),
		xmlrpc.StringValue("Türöffner"),
		xmlrpc.DoubleValue(0.0),
		xmlrpc.DoubleValue(1.0),
		xmlrpc.DoubleValue(-3.5),
		xmlrpc.DoubleValue(1234567.89),
	}
	for _, in := range cases {
		t.Run(in.Kind().String(), func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteRequest(&buf, "echo", []xmlrpc.Value{in}); err != nil {
				t.Fatal(err)
			}
			req, err := ReadRequest(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if req.Method != "echo" {
				t.Fatalf("method=%q", req.Method)
			}
			if len(req.Params) != 1 {
				t.Fatalf("params=%d", len(req.Params))
			}
			assertValueEqual(t, in, req.Params[0])
		})
	}
}

func assertValueEqual(t *testing.T, want, got xmlrpc.Value) {
	t.Helper()
	if want.Kind() != got.Kind() {
		t.Fatalf("kind: got %s, want %s", got.Kind(), want.Kind())
	}
	switch w := want.(type) {
	case xmlrpc.IntValue:
		g, _ := xmlrpc.AsInt(got)
		if g != int(w) {
			t.Fatalf("int: got %d, want %d", g, w)
		}
	case xmlrpc.BoolValue:
		g, _ := xmlrpc.AsBool(got)
		if g != bool(w) {
			t.Fatalf("bool: got %v, want %v", g, w)
		}
	case xmlrpc.StringValue:
		g, _ := xmlrpc.AsString(got)
		if g != string(w) {
			t.Fatalf("string: got %q, want %q", g, w)
		}
	case xmlrpc.DoubleValue:
		g, _ := xmlrpc.AsDouble(got)
		// BIN-RPC's mantissa is int32 → ~30-bit significand. Relative
		// precision bottoms out around 1e-9 and degrades with |value|.
		// 1e-6 is still far below anything CUxD cares about.
		if math.Abs(g-float64(w)) > 1e-6*math.Max(1, math.Abs(float64(w))) {
			t.Fatalf("double: got %g, want %g", g, w)
		}
	default:
		t.Fatalf("unexpected kind %T", want)
	}
}

func TestRoundTripStructAndArray(t *testing.T) {
	in := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "ADDRESS", Value: xmlrpc.StringValue("ABC:1")},
		{Name: "LEVEL", Value: xmlrpc.DoubleValue(0.75)},
		{Name: "TAGS", Value: xmlrpc.ArrayValue{
			xmlrpc.StringValue("a"),
			xmlrpc.StringValue("b"),
		}},
	}}
	var buf bytes.Buffer
	if err := WriteRequest(&buf, "setValue", []xmlrpc.Value{in}); err != nil {
		t.Fatal(err)
	}
	req, err := ReadRequest(&buf)
	if err != nil {
		t.Fatal(err)
	}
	s, err := xmlrpc.AsStruct(req.Params[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Members) != 3 {
		t.Fatalf("members=%d", len(s.Members))
	}
	addr, _ := xmlrpc.StructField[xmlrpc.StringValue](s, "ADDRESS")
	if string(addr) != "ABC:1" {
		t.Fatalf("ADDRESS=%q", addr)
	}
	tags, _ := xmlrpc.StructField[xmlrpc.ArrayValue](s, "TAGS")
	if len(tags) != 2 {
		t.Fatalf("tags=%d", len(tags))
	}
}

func TestResponseRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.IntValue(7)); err != nil {
		t.Fatal(err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fault != nil {
		t.Fatalf("unexpected fault: %v", resp.Fault)
	}
	if n, _ := xmlrpc.AsInt(resp.Value); n != 7 {
		t.Fatalf("value=%v", resp.Value)
	}
}

func TestFaultRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFault(&buf, &hmerr.XMLRPCFault{Code: -3, Message: "not found"}); err != nil {
		t.Fatal(err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fault == nil || resp.Fault.Code != -3 || resp.Fault.Message != "not found" {
		t.Fatalf("fault=%+v", resp.Fault)
	}
}

func TestNilValueEncodesAsEmptyString(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.NilValue{}); err != nil {
		t.Fatal(err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	s, err := xmlrpc.AsString(resp.Value)
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Fatalf("want empty string, got %q", s)
	}
}

func TestDateTimeRejectedAtEncoding(t *testing.T) {
	var buf bytes.Buffer
	err := WriteResponse(&buf, xmlrpc.DateTimeValue{})
	if err == nil {
		t.Fatal("expected error for DateTimeValue on BIN-RPC")
	}
}

func TestBase64RejectedAtEncoding(t *testing.T) {
	var buf bytes.Buffer
	err := WriteResponse(&buf, xmlrpc.Base64Value{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for Base64Value on BIN-RPC")
	}
}

func TestReadRequestRejectsBadMarker(t *testing.T) {
	bad := []byte("Xin\x00\x00\x00\x00\x00")
	_, err := ReadRequest(bytes.NewReader(bad))
	if err == nil {
		t.Fatal("expected bad-marker error")
	}
}

func TestReadResponseRejectsUnknownMsgType(t *testing.T) {
	// Marker + bogus message type 0x77 + 0 payload.
	raw := []byte{'B', 'i', 'n', 0x77, 0, 0, 0, 0}
	_, err := ReadResponse(bytes.NewReader(raw))
	if err == nil {
		t.Fatal("expected unknown-msg-type error")
	}
}

func TestReadRequestTrailingBytesFail(t *testing.T) {
	// Valid frame with 1 extra byte declared in header, but only 4 bytes
	// of method length field (length=0), ignoring paramCount → trailing
	// data indicates truncation.
	// Easier: build a valid frame, then tack on a byte — that breaks
	// ReadResponse because we check remaining()==0.
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.IntValue(1)); err != nil {
		t.Fatal(err)
	}
	// Inject one extra byte into the payload: raise declared size by 1.
	raw := buf.Bytes()
	// size field is at offset 4..8, big-endian uint32
	raw[7]++
	raw = append(raw, 0xFF)
	_, err := ReadResponse(bytes.NewReader(raw))
	if err == nil {
		t.Fatal("expected error due to trailing bytes")
	}
}

func TestEncodeRequestRejectsEmptyMethod(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRequest(&buf, "", nil); err == nil {
		t.Fatal("expected empty-method error")
	}
}

func TestEncodeRejectsNilParam(t *testing.T) {
	var buf bytes.Buffer
	err := WriteRequest(&buf, "m", []xmlrpc.Value{nil})
	if err == nil {
		t.Fatal("expected nil-param error")
	}
}

func TestEncodeRejectsUTF8OutsideISO8859(t *testing.T) {
	// Emoji → not representable in ISO-8859-1.
	var buf bytes.Buffer
	err := WriteResponse(&buf, xmlrpc.StringValue("🦀"))
	if err == nil {
		t.Fatal("expected error for non-Latin-1 string")
	}
}

// TestEncodeValueCanonicalBytes anchors the byte-level wire form of every
// value type. The frame body of a single-value response is precisely
// typeTag(u32) || valuePayload, so we slice the 8-byte header off the
// WriteResponse output and compare against the expected value-only hex.
// Inputs use TEST-NET-1 (RFC 5737) addresses and locally-defined device
// names so no third-party test fixtures are involved.
func TestEncodeValueCanonicalBytes(t *testing.T) {
	cases := []struct {
		name string
		in   xmlrpc.Value
		hex  string
	}{
		{
			`String "openccu-loom"`,
			xmlrpc.StringValue("openccu-loom"),
			"00 00 00 03 00 00 00 0c 6f 70 65 6e 63 63 75 2d 6c 6f 6f 6d",
		},
		{
			`String ISO-8859-1 "Türöffner"`,
			xmlrpc.StringValue("Türöffner"),
			"00 00 00 03 00 00 00 09 54 fc 72 f6 66 66 6e 65 72",
		},
		{
			"Integer 7",
			xmlrpc.IntValue(7),
			"00 00 00 01 00 00 00 07",
		},
		{
			"Bool false",
			xmlrpc.BoolValue(false),
			"00 00 00 02 00",
		},
		{
			"Bool true",
			xmlrpc.BoolValue(true),
			"00 00 00 02 01",
		},
		{
			"Double 1.5",
			xmlrpc.DoubleValue(1.5),
			"00 00 00 04 30 00 00 00 00 00 00 01",
		},
		{
			"Double -3.25",
			xmlrpc.DoubleValue(-3.25),
			"00 00 00 04 cc 00 00 00 00 00 00 02",
		},
		{
			"Struct VALUE=0.5",
			xmlrpc.StructValue{Members: []xmlrpc.Member{
				{Name: "VALUE", Value: xmlrpc.DoubleValue(0.5)},
			}},
			"00 00 01 01 00 00 00 01 00 00 00 05 56 41 4c 55 45 00 00 00 04 20 00 00 00 00 00 00 00",
		},
		{
			"Array [1, 2, 3]",
			xmlrpc.ArrayValue{xmlrpc.IntValue(1), xmlrpc.IntValue(2), xmlrpc.IntValue(3)},
			"00 00 01 00 00 00 00 03 00 00 00 01 00 00 00 01 00 00 00 01 00 00 00 02 00 00 00 01 00 00 00 03",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteResponse(&buf, c.in); err != nil {
				t.Fatalf("WriteResponse: %v", err)
			}
			// Strip the 8-byte response header to expose just the value.
			got := buf.Bytes()[8:]
			want := mustHex(t, c.hex)
			if !bytes.Equal(got, want) {
				t.Fatalf("wire mismatch\n got:  %x\n want: %x", got, want)
			}
		})
	}
}

// TestWriteRequestCanonicalFrames pins the full request frames (header
// included) for three representative cases:
//   - system.listMethods (no params)
//   - init() with a TEST-NET-1 callback URL (RFC 5737)
//   - system.multicall wrapping a single `event` sub-call — the layout
//     CUxD callbacks rely on
func TestWriteRequestCanonicalFrames(t *testing.T) {
	cases := []struct {
		name   string
		method string
		params []xmlrpc.Value
		hex    string
	}{
		{
			"system.listMethods",
			"system.listMethods",
			nil,
			"42 69 6e 00 00 00 00 1a 00 00 00 12 73 79 73 74 65 6d 2e 6c 69 73 74 4d 65 74 68 6f 64 73 00 00 00 00",
		},
		{
			"init test-net 192.0.2.1:8129",
			"init",
			[]xmlrpc.Value{
				xmlrpc.StringValue("xmlrpc_bin://192.0.2.1:8129"),
				xmlrpc.StringValue("openccu-loom-test"),
			},
			"42 69 6e 00 00 00 00 48 00 00 00 04 69 6e 69 74 00 00 00 02 00 00 00 03 00 00 00 1b 78 6d 6c 72 " +
				"70 63 5f 62 69 6e 3a 2f 2f 31 39 32 2e 30 2e 32 2e 31 3a 38 31 32 39 00 00 00 03 00 00 00 11 6f " +
				"70 65 6e 63 63 75 2d 6c 6f 6f 6d 2d 74 65 73 74",
		},
		{
			"system.multicall single event",
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
			},
			"42 69 6e 00 00 00 00 86 " +
				"00 00 00 10 73 79 73 74 65 6d 2e 6d 75 6c 74 69 63 61 6c 6c 00 00 00 01 00 00 01 00 00 00 00 01 " +
				"00 00 01 01 00 00 00 02 00 00 00 0a 6d 65 74 68 6f 64 4e 61 6d 65 00 00 00 03 00 00 00 05 65 76 " +
				"65 6e 74 00 00 00 06 70 61 72 61 6d 73 00 00 01 00 00 00 00 04 00 00 00 03 00 00 00 03 47 48 4d " +
				"00 00 00 03 00 00 00 0c 47 48 4d 30 30 30 30 30 30 31 3a 31 00 00 00 03 00 00 00 05 53 54 41 54 " +
				"45 00 00 00 02 01",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteRequest(&buf, c.method, c.params); err != nil {
				t.Fatalf("WriteRequest: %v", err)
			}
			want := mustHex(t, c.hex)
			if !bytes.Equal(buf.Bytes(), want) {
				t.Fatalf("frame mismatch\n got:  %x\n want: %x", buf.Bytes(), want)
			}
		})
	}
}

// TestWriteFaultCanonical pins the exact bytes of a fault frame —
// faultCode comes first, faultString second, struct member-count is two.
func TestWriteFaultCanonical(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFault(&buf, &hmerr.XMLRPCFault{Code: -1, Message: "test fault"}); err != nil {
		t.Fatalf("WriteFault: %v", err)
	}
	want := mustHex(t,
		"42 69 6e ff 00 00 00 3e "+
			"00 00 01 01 00 00 00 02 "+
			"00 00 00 09 66 61 75 6c 74 43 6f 64 65 "+
			"00 00 00 01 ff ff ff ff "+
			"00 00 00 0b 66 61 75 6c 74 53 74 72 69 6e 67 "+
			"00 00 00 03 00 00 00 0a 74 65 73 74 20 66 61 75 6c 74")
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("fault frame mismatch\n got:  %x\n want: %x", buf.Bytes(), want)
	}
}

// TestReadRequestCanonical proves the decoder works against pre-recorded
// bytes (no encoder in the loop). The hex is the canonical "init" frame
// for the TEST-NET-1 callback URL used in TestWriteRequestCanonicalFrames.
func TestReadRequestCanonical(t *testing.T) {
	raw := mustHex(t,
		"42 69 6e 00 00 00 00 48 00 00 00 04 69 6e 69 74 00 00 00 02 00 00 00 03 00 00 00 1b 78 6d 6c 72 "+
			"70 63 5f 62 69 6e 3a 2f 2f 31 39 32 2e 30 2e 32 2e 31 3a 38 31 32 39 00 00 00 03 00 00 00 11 6f "+
			"70 65 6e 63 63 75 2d 6c 6f 6f 6d 2d 74 65 73 74")
	req, err := ReadRequest(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if req.Method != "init" {
		t.Fatalf("method=%q, want %q", req.Method, "init")
	}
	if len(req.Params) != 2 {
		t.Fatalf("params=%d, want 2", len(req.Params))
	}
	url, err := xmlrpc.AsString(req.Params[0])
	if err != nil {
		t.Fatal(err)
	}
	if url != "xmlrpc_bin://192.0.2.1:8129" {
		t.Fatalf("url=%q", url)
	}
	tag, err := xmlrpc.AsString(req.Params[1])
	if err != nil {
		t.Fatal(err)
	}
	if tag != "openccu-loom-test" {
		t.Fatalf("tag=%q", tag)
	}
}

// TestDoubleExactRepresentation covers values that are exactly
// representable in BIN-RPC's 30-bit-mantissa scheme: any (sign × dyadic
// rational with |mant| < 2^30) survives a roundtrip with bit-exact
// equality. The chosen values mix small integers, 2^k fractions and a
// power-of-two upper-band value to catch off-by-one drift in encodeDouble.
func TestDoubleExactRepresentation(t *testing.T) {
	exacts := []float64{
		7.875,      // 63/8 — small dyadic
		1.0 / 4096, // 2^-12 — sub-unity
		0.0,
		1.0,
		-1.0,
		0.5,
		-0.125,
		32768.0, // 2^15 — mid-range power of two
	}
	for _, want := range exacts {
		t.Run(strconv.FormatFloat(want, 'g', -1, 64), func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteResponse(&buf, xmlrpc.DoubleValue(want)); err != nil {
				t.Fatalf("WriteResponse: %v", err)
			}
			resp, err := ReadResponse(&buf)
			if err != nil {
				t.Fatalf("ReadResponse: %v", err)
			}
			got, err := xmlrpc.AsDouble(resp.Value)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("got %v, want %v (bit-exact equality required)", got, want)
			}
		})
	}
}

// TestEncodeRejectsNaN locks in that the encoder refuses NaN — the format
// has no representation for it and CUxD never sends it.
func TestEncodeRejectsNaN(t *testing.T) {
	var buf bytes.Buffer
	err := WriteResponse(&buf, xmlrpc.DoubleValue(math.NaN()))
	if err == nil {
		t.Fatal("expected NaN-rejection error")
	}
}

// TestEncodeRejectsInf locks in that the encoder refuses ±Inf.
func TestEncodeRejectsInf(t *testing.T) {
	for _, v := range []float64{math.Inf(1), math.Inf(-1)} {
		var buf bytes.Buffer
		if err := WriteResponse(&buf, xmlrpc.DoubleValue(v)); err == nil {
			t.Fatalf("expected Inf-rejection error for %v", v)
		}
	}
}

func TestReadResponseRespectsMaxMessageSize(t *testing.T) {
	// Declare an absurdly large payload size.
	raw := make([]byte, 8)
	copy(raw, marker[:])
	raw[3] = msgTypeResponse
	raw[4] = 0xFF
	raw[5] = 0xFF
	raw[6] = 0xFF
	raw[7] = 0xFF
	_, err := ReadResponse(bytes.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("want limit error, got %v", err)
	}
}
