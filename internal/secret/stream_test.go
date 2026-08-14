// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package secret_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/secret"
)

// loadCipher builds an available Cipher from a fixed env key.
func loadCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	c, err := secret.Load("", envWithKey(validKey()), noopLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Available() {
		t.Fatal("cipher not available with a valid env key")
	}
	return c
}

// encryptStream is a helper that seals src through NewEncryptWriter.
func encryptStream(t *testing.T, c *secret.Cipher, src []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := c.NewEncryptWriter(&buf)
	if err != nil {
		t.Fatalf("NewEncryptWriter: %v", err)
	}
	if _, err := w.Write(src); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

// TestStreamRoundTripSizes seals then opens payloads spanning the chunk
// boundary so the multi-frame framing is exercised end-to-end.
func TestStreamRoundTripSizes(t *testing.T) {
	t.Parallel()
	c := loadCipher(t)

	const chunk = 64 * 1024
	sizes := []int{0, 1, 100, chunk - 1, chunk, chunk + 1, 3*chunk + 17}
	for _, n := range sizes {
		src := make([]byte, n)
		if _, err := io.ReadFull(rand.Reader, src); err != nil {
			t.Fatalf("rand: %v", err)
		}
		ct := encryptStream(t, c, src)

		// Sanity: the plaintext must not appear verbatim in the ciphertext.
		// Only meaningful for a payload long enough that a chance substring
		// match is impossible (a handful of random bytes can collide).
		if n >= 64 && bytes.Contains(ct, src) {
			t.Fatalf("size %d: plaintext leaked into ciphertext", n)
		}

		got, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader(ct)))
		if err != nil {
			t.Fatalf("size %d: decrypt: %v", n, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("size %d: round-trip mismatch (got %d bytes)", n, len(got))
		}
	}
}

// TestStreamManyWritesRoundTrip verifies that many small Writes reassemble
// into the correct plaintext regardless of write boundaries.
func TestStreamManyWritesRoundTrip(t *testing.T) {
	t.Parallel()
	c := loadCipher(t)

	src := make([]byte, 200*1024)
	if _, err := io.ReadFull(rand.Reader, src); err != nil {
		t.Fatalf("rand: %v", err)
	}
	var buf bytes.Buffer
	w, err := c.NewEncryptWriter(&buf)
	if err != nil {
		t.Fatalf("NewEncryptWriter: %v", err)
	}
	for off := 0; off < len(src); off += 7777 {
		end := min(off+7777, len(src))
		if _, err := w.Write(src[off:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("chunked-write round-trip mismatch")
	}
}

// TestStreamTruncationDetected drops trailing bytes so the stream ends before
// its authenticated final frame; decryption must error rather than yield a
// clean short read.
func TestStreamTruncationDetected(t *testing.T) {
	t.Parallel()
	c := loadCipher(t)

	src := make([]byte, 3*64*1024) // multiple frames
	if _, err := io.ReadFull(rand.Reader, src); err != nil {
		t.Fatalf("rand: %v", err)
	}
	ct := encryptStream(t, c, src)

	truncated := ct[:len(ct)-50] // lop off the tail (final frame)
	_, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader(truncated)))
	if err == nil {
		t.Fatal("expected error for truncated stream, got nil")
	}
}

// TestStreamTamperDetected flips a ciphertext byte; GCM must reject it.
func TestStreamTamperDetected(t *testing.T) {
	t.Parallel()
	c := loadCipher(t)

	src := bytes.Repeat([]byte("payload"), 5000)
	ct := encryptStream(t, c, src)

	// Flip a byte in the middle of the first frame body (past the 8-byte
	// stream id + 5-byte frame header).
	tampered := append([]byte(nil), ct...)
	tampered[20] ^= 0xff
	_, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader(tampered)))
	if err == nil {
		t.Fatal("expected authentication failure for tampered ciphertext, got nil")
	}
}

// TestStreamFrameLengthBoundRejected hand-builds frame headers that declare a
// ciphertext far larger than a frame can legally carry. The reader must reject
// them with the descriptive bound error and must never reach the allocation:
// where int is 32 bits, a declared length above MaxInt32 wraps negative and
// make() panics instead.
func TestStreamFrameLengthBoundRejected(t *testing.T) {
	t.Parallel()
	c := loadCipher(t)

	const (
		chunk       = 64 * 1024
		gcmOverhead = 16
		streamIDLen = 8 // random per-stream nonce prefix
		headerLen   = 5 // flag(1) || ciphertextLen(4)
	)

	// A header-only stream: the stream id, then flag(1) || length(4).
	header := func(ctLen uint32) []byte {
		buf := make([]byte, streamIDLen+headerLen)
		binary.BigEndian.PutUint32(buf[streamIDLen+1:], ctLen)
		return buf
	}

	for _, declared := range []uint32{chunk + gcmOverhead + 1, 0xF0000000, math.MaxUint32} {
		_, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader(header(declared))))
		if err == nil {
			t.Fatalf("declared length %#x: expected an error, got nil", declared)
		}
		if !strings.Contains(err.Error(), "exceeds bound") {
			t.Fatalf("declared length %#x: want the length-bound error, got %v", declared, err)
		}
	}

	// The largest legal length must still pass the bound check and fail later,
	// on the missing frame body — the bound must not be tightened by accident.
	_, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader(header(chunk + gcmOverhead))))
	if err == nil {
		t.Fatal("expected an error for a frame with a missing body, got nil")
	}
	if strings.Contains(err.Error(), "exceeds bound") {
		t.Fatalf("the maximum legal frame length must pass the bound check, got %v", err)
	}
}

// TestStreamWrongKeyRejected seals with one key and opens with another.
func TestStreamWrongKeyRejected(t *testing.T) {
	t.Parallel()
	c := loadCipher(t)
	src := []byte("secret archive bytes")
	ct := encryptStream(t, c, src)

	// A different key.
	other, err := secret.Load("", envWithKey(otherKey()), noopLogger())
	if err != nil {
		t.Fatalf("Load other: %v", err)
	}
	_, err = io.ReadAll(other.NewDecryptReader(bytes.NewReader(ct)))
	if err == nil {
		t.Fatal("expected error decrypting with the wrong key, got nil")
	}
}

// TestStreamNoKeyRefused verifies the streaming API refuses to operate when no
// master key is available (it must never silently pass plaintext through).
func TestStreamNoKeyRefused(t *testing.T) {
	t.Parallel()
	c, err := secret.Load("", emptyEnv, noopLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Available() {
		t.Skip("environment resolved a key; cannot test the no-key path here")
	}
	if _, err := c.NewEncryptWriter(io.Discard); err == nil {
		t.Fatal("NewEncryptWriter must error without a key")
	}
	if _, err := io.ReadAll(c.NewDecryptReader(bytes.NewReader([]byte("x")))); err == nil {
		t.Fatal("NewDecryptReader must error without a key")
	}
}

// otherKey returns a distinct valid base64 32-byte key (all 0x01) so it
// differs from validKey's all-zero key.
func otherKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32))
}
