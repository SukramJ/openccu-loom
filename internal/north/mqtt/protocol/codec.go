// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// PacketType is the fixed-header packet type (4 high bits of byte 1).
type PacketType byte

// PacketType values.
const (
	PacketConnect     PacketType = 1
	PacketConnack     PacketType = 2
	PacketPublish     PacketType = 3
	PacketPuback      PacketType = 4
	PacketSubscribe   PacketType = 8
	PacketSuback      PacketType = 9
	PacketUnsubscribe PacketType = 10
	PacketUnsuback    PacketType = 11
	PacketPingreq     PacketType = 12
	PacketPingresp    PacketType = 13
	PacketDisconnect  PacketType = 14
)

// ConnectPacket is the outbound CONNECT.
type ConnectPacket struct {
	ClientID     string
	KeepAlive    uint16 // seconds
	Username     string
	Password     string
	CleanSession bool
	WillTopic    string
	WillPayload  []byte
	WillRetain   bool
	WillQoS      byte
}

// Encode writes the packet to w.
func (p *ConnectPacket) Encode(w io.Writer) error {
	var payload bytes.Buffer
	// Protocol name "MQTT" + level 4 (3.1.1).
	writeString(&payload, "MQTT")
	payload.WriteByte(4)

	// Flags.
	var flags byte
	if p.CleanSession {
		flags |= 0x02
	}
	if p.WillTopic != "" {
		flags |= 0x04
		flags |= (p.WillQoS & 0x03) << 3
		if p.WillRetain {
			flags |= 0x20
		}
	}
	if p.Password != "" {
		flags |= 0x40
	}
	if p.Username != "" {
		flags |= 0x80
	}
	payload.WriteByte(flags)

	// Keep alive.
	_ = binary.Write(&payload, binary.BigEndian, p.KeepAlive)

	// Client ID.
	writeString(&payload, p.ClientID)

	if p.WillTopic != "" {
		writeString(&payload, p.WillTopic)
		writeBytes(&payload, p.WillPayload)
	}
	if p.Username != "" {
		writeString(&payload, p.Username)
	}
	if p.Password != "" {
		writeString(&payload, p.Password)
	}

	return writePacket(w, byte(PacketConnect)<<4, payload.Bytes())
}

// ConnackPacket is the inbound CONNACK.
type ConnackPacket struct {
	SessionPresent bool
	ReturnCode     byte
}

// DecodeConnack parses a CONNACK payload (the 2-byte variable header).
func DecodeConnack(body []byte) (*ConnackPacket, error) {
	if len(body) < 2 {
		return nil, errors.New("connack: short body")
	}
	return &ConnackPacket{
		SessionPresent: body[0]&0x01 != 0,
		ReturnCode:     body[1],
	}, nil
}

// PublishPacket is the outbound PUBLISH.
type PublishPacket struct {
	Topic    string
	Payload  []byte
	QoS      byte // 0 or 1
	Retain   bool
	Dup      bool
	PacketID uint16 // set for QoS > 0
}

// Encode writes the packet.
func (p *PublishPacket) Encode(w io.Writer) error {
	if p.QoS > 1 {
		return fmt.Errorf("publish: unsupported QoS %d", p.QoS)
	}
	head := byte(PacketPublish) << 4
	if p.Dup {
		head |= 0x08
	}
	head |= (p.QoS & 0x03) << 1
	if p.Retain {
		head |= 0x01
	}
	var body bytes.Buffer
	writeString(&body, p.Topic)
	if p.QoS > 0 {
		_ = binary.Write(&body, binary.BigEndian, p.PacketID)
	}
	body.Write(p.Payload)
	return writePacket(w, head, body.Bytes())
}

// PubackPacket is the inbound PUBACK.
type PubackPacket struct {
	PacketID uint16
}

// DecodePuback parses a PUBACK.
func DecodePuback(body []byte) (*PubackPacket, error) {
	if len(body) < 2 {
		return nil, errors.New("puback: short body")
	}
	return &PubackPacket{PacketID: binary.BigEndian.Uint16(body[:2])}, nil
}

// SubscribePacket is the outbound SUBSCRIBE.
type SubscribePacket struct {
	PacketID    uint16
	TopicFilter string
	QoS         byte
}

// Encode writes the packet.
func (p *SubscribePacket) Encode(w io.Writer) error {
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, p.PacketID)
	writeString(&body, p.TopicFilter)
	body.WriteByte(p.QoS & 0x03)
	// SUBSCRIBE fixed header requires bit 1 set.
	return writePacket(w, byte(PacketSubscribe)<<4|0x02, body.Bytes())
}

// UnsubscribePacket is the outbound UNSUBSCRIBE.
type UnsubscribePacket struct {
	PacketID    uint16
	TopicFilter string
}

// Encode writes the packet.
func (p *UnsubscribePacket) Encode(w io.Writer) error {
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, p.PacketID)
	writeString(&body, p.TopicFilter)
	return writePacket(w, byte(PacketUnsubscribe)<<4|0x02, body.Bytes())
}

// InboundPublish bundles an incoming PUBLISH as the client cares
// about it.
type InboundPublish struct {
	Topic    string
	Payload  []byte
	QoS      byte
	PacketID uint16
	Retain   bool
}

// DecodePublish parses a PUBLISH payload. header is the byte-1 value
// (so the caller can pass QoS/DUP/RETAIN bits).
func DecodePublish(header byte, body []byte) (*InboundPublish, error) {
	qos := (header >> 1) & 0x03
	retain := header&0x01 != 0
	idx := 0
	topic, n, err := readString(body)
	if err != nil {
		return nil, err
	}
	idx += n
	var pktID uint16
	if qos > 0 {
		if idx+2 > len(body) {
			return nil, errors.New("publish: missing packet id")
		}
		pktID = binary.BigEndian.Uint16(body[idx : idx+2])
		idx += 2
	}
	return &InboundPublish{
		Topic:    topic,
		Payload:  body[idx:],
		QoS:      qos,
		PacketID: pktID,
		Retain:   retain,
	}, nil
}

// EncodePingReq writes a PINGREQ.
func EncodePingReq(w io.Writer) error {
	return writePacket(w, byte(PacketPingreq)<<4, nil)
}

// EncodeDisconnect writes DISCONNECT.
func EncodeDisconnect(w io.Writer) error {
	return writePacket(w, byte(PacketDisconnect)<<4, nil)
}

// EncodePuback writes a PUBACK for id.
func EncodePuback(w io.Writer, id uint16) error {
	body := make([]byte, 2)
	binary.BigEndian.PutUint16(body, id)
	return writePacket(w, byte(PacketPuback)<<4, body)
}

// Frame is a decoded fixed-header + remaining bytes tuple.
type Frame struct {
	Header byte
	Body   []byte
}

// PacketType returns the packet type bits of the header.
func (f Frame) PacketType() PacketType { return PacketType(f.Header >> 4) }

// ReadFrame reads one MQTT packet from r.
func ReadFrame(r io.Reader) (Frame, error) {
	head := make([]byte, 1)
	if _, err := io.ReadFull(r, head); err != nil {
		return Frame{}, err
	}
	length, err := readRemainingLength(r)
	if err != nil {
		return Frame{}, err
	}
	body := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, body); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Header: head[0], Body: body}, nil
}

// --- helpers ---

func writePacket(w io.Writer, header byte, body []byte) error {
	if _, err := w.Write([]byte{header}); err != nil {
		return err
	}
	length := encodeRemainingLength(len(body))
	if _, err := w.Write(length); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	_, err := w.Write(body)
	return err
}

func encodeRemainingLength(n int) []byte {
	var out []byte
	for {
		digit := byte(n & 0x7F)
		n >>= 7
		if n > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if n == 0 {
			break
		}
	}
	return out
}

func readRemainingLength(r io.Reader) (int, error) {
	var mult uint32 = 1
	var length uint32
	buf := make([]byte, 1)
	for range 4 {
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		length += uint32(buf[0]&0x7F) * mult
		if buf[0]&0x80 == 0 {
			return int(length), nil
		}
		mult *= 128
	}
	return 0, errors.New("mqtt: malformed remaining length")
}

func writeString(w *bytes.Buffer, s string) {
	_ = binary.Write(w, binary.BigEndian, uint16(len(s))) //nolint:gosec // bounded by string length
	w.WriteString(s)
}

func writeBytes(w *bytes.Buffer, b []byte) {
	_ = binary.Write(w, binary.BigEndian, uint16(len(b))) //nolint:gosec // bounded by payload length
	w.Write(b)
}

func readString(b []byte) (value string, bytesRead int, err error) {
	if len(b) < 2 {
		return "", 0, errors.New("mqtt: short string header")
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	if len(b) < 2+n {
		return "", 0, errors.New("mqtt: short string body")
	}
	return string(b[2 : 2+n]), 2 + n, nil
}
