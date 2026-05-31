// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package protocol

import (
	"bytes"
	"testing"
)

func TestConnectEncode(t *testing.T) {
	var buf bytes.Buffer
	p := &ConnectPacket{
		ClientID: "go", KeepAlive: 30, CleanSession: true,
		Username: "u", Password: "p",
		WillTopic: "t/s", WillPayload: []byte("x"), WillRetain: true, WillQoS: 1,
	}
	if err := p.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if buf.Len() < 20 || buf.Bytes()[0]>>4 != byte(PacketConnect) {
		t.Fatalf("bytes=%x", buf.Bytes())
	}
}

func TestConnackDecode(t *testing.T) {
	c, err := DecodeConnack([]byte{0x01, 0x00})
	if err != nil || !c.SessionPresent || c.ReturnCode != 0 {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}

func TestPublishRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	p := &PublishPacket{Topic: "a/b", Payload: []byte("hi"), QoS: 1, PacketID: 42}
	if err := p.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	frame, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if frame.PacketType() != PacketPublish {
		t.Fatalf("type=%d", frame.PacketType())
	}
	ib, err := DecodePublish(frame.Header, frame.Body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ib.Topic != "a/b" || string(ib.Payload) != "hi" || ib.QoS != 1 || ib.PacketID != 42 {
		t.Fatalf("ib=%+v", ib)
	}
}

func TestPublishQoS2Rejected(t *testing.T) {
	var buf bytes.Buffer
	p := &PublishPacket{Topic: "a", QoS: 2}
	if err := p.Encode(&buf); err == nil {
		t.Fatal("QoS 2 must be rejected")
	}
}

func TestSubscribeEncode(t *testing.T) {
	var buf bytes.Buffer
	s := &SubscribePacket{PacketID: 7, TopicFilter: "h/#", QoS: 1}
	if err := s.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if buf.Bytes()[0]>>4 != byte(PacketSubscribe) || buf.Bytes()[0]&0x0F != 0x02 {
		t.Fatalf("header=%x", buf.Bytes()[0])
	}
}

func TestRemainingLengthRoundTrip(t *testing.T) {
	for _, n := range []int{0, 127, 128, 16383, 16384, 2097151} {
		enc := encodeRemainingLength(n)
		r := bytes.NewReader(enc)
		got, err := readRemainingLength(r)
		if err != nil || got != n {
			t.Fatalf("n=%d got=%d err=%v", n, got, err)
		}
	}
}

func TestPingAndDisconnect(t *testing.T) {
	var buf bytes.Buffer
	_ = EncodePingReq(&buf)
	_ = EncodeDisconnect(&buf)
	f1, _ := ReadFrame(&buf)
	f2, _ := ReadFrame(&buf)
	if f1.PacketType() != PacketPingreq || f2.PacketType() != PacketDisconnect {
		t.Fatalf("types: %d %d", f1.PacketType(), f2.PacketType())
	}
}

func TestPubackEncodeDecode(t *testing.T) {
	var buf bytes.Buffer
	_ = EncodePuback(&buf, 0x1234)
	f, _ := ReadFrame(&buf)
	if f.PacketType() != PacketPuback {
		t.Fatalf("type=%d", f.PacketType())
	}
	p, err := DecodePuback(f.Body)
	if err != nil || p.PacketID != 0x1234 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}
