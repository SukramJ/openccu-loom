// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package secret

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Streaming AES-256-GCM container.
//
// Seal/Open operate on whole strings and buffer the entire value in
// memory, which is fine for config leaves but not for a multi-hundred-MB
// backup archive. NewEncryptWriter / NewDecryptReader instead frame the
// plaintext into fixed-size chunks, each sealed with its own GCM nonce, so
// memory stays bounded regardless of archive size.
//
// Layout of the encrypted body (the caller prepends its own magic/version
// header before the stream):
//
//	streamID : 8 random bytes, written once
//	frame*   : flag(1) || ciphertextLen(4, big-endian) || ciphertext
//
// Per frame: nonce = streamID(8) || counter(4, big-endian); the counter and
// the final-flag are authenticated as GCM additional data, so a dropped,
// reordered, or truncated frame fails authentication rather than silently
// yielding a partial archive. The stream always terminates with an
// authenticated final frame (possibly empty), so a truncated stream is
// detected as a missing final frame instead of a clean EOF.
const (
	// streamChunkSize is the plaintext chunk size. 64 KiB keeps per-frame
	// allocations small while amortising the 16-byte GCM tag overhead.
	streamChunkSize = 64 * 1024
	// streamIDSize is the random per-stream nonce prefix length. The
	// remaining 4 nonce bytes are the frame counter.
	streamIDSize = 8
	// frameHeaderSize is flag(1) + ciphertextLen(4).
	frameHeaderSize = 5
	// frameFinalFlag marks the last frame of a stream.
	frameFinalFlag = 0x01
)

// errNoKey is returned when a streaming operation is attempted without an
// available master key.
var errNoKey = errors.New("secret: streaming requires an available master key")

func streamNonce(streamID []byte, counter uint32) []byte {
	nonce := make([]byte, streamIDSize+4)
	copy(nonce, streamID)
	binary.BigEndian.PutUint32(nonce[streamIDSize:], counter)
	return nonce
}

func streamAAD(counter uint32, final bool) []byte {
	aad := make([]byte, 5)
	binary.BigEndian.PutUint32(aad[:4], counter)
	if final {
		aad[4] = frameFinalFlag
	}
	return aad
}

func (c *Cipher) sealFrame(w io.Writer, streamID []byte, counter uint32, final bool, plain []byte) error {
	ct := c.aead.Seal(nil, streamNonce(streamID, counter), plain, streamAAD(counter, final))
	hdr := make([]byte, frameHeaderSize)
	if final {
		hdr[0] = frameFinalFlag
	}
	// len(ct) is bounded by streamChunkSize + GCM overhead, far below uint32 max.
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(ct))) //nolint:gosec // ct length is bounded by streamChunkSize+overhead
	if _, err := w.Write(hdr); err != nil {
		return fmt.Errorf("secret: write frame header: %w", err)
	}
	if _, err := w.Write(ct); err != nil {
		return fmt.Errorf("secret: write frame body: %w", err)
	}
	return nil
}

// encryptWriter is the io.WriteCloser returned by NewEncryptWriter. Close
// MUST be called to flush the final authenticated frame; without it the
// stream is incomplete and DecryptReader will reject it as truncated.
type encryptWriter struct {
	c        *Cipher
	w        io.Writer
	streamID []byte
	counter  uint32
	buf      []byte
	err      error
	closed   bool
}

// NewEncryptWriter returns a writer that seals everything written to it into
// the chunked container described above and forwards the ciphertext to w. It
// writes the stream header immediately. Requires an available Cipher.
func (c *Cipher) NewEncryptWriter(w io.Writer) (io.WriteCloser, error) {
	if !c.Available() {
		return nil, errNoKey
	}
	streamID := make([]byte, streamIDSize)
	if _, err := io.ReadFull(rand.Reader, streamID); err != nil {
		return nil, fmt.Errorf("secret: stream id: %w", err)
	}
	if _, err := w.Write(streamID); err != nil {
		return nil, fmt.Errorf("secret: write stream id: %w", err)
	}
	return &encryptWriter{c: c, w: w, streamID: streamID, buf: make([]byte, 0, streamChunkSize)}, nil
}

func (e *encryptWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	written := 0
	for len(p) > 0 {
		space := streamChunkSize - len(e.buf)
		n := min(space, len(p))
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		written += n
		if len(e.buf) == streamChunkSize {
			if err := e.flush(false); err != nil {
				e.err = err
				return written, err
			}
		}
	}
	return written, nil
}

func (e *encryptWriter) flush(final bool) error {
	if !final && e.counter == math.MaxUint32 {
		return errors.New("secret: stream too large (frame counter exhausted)")
	}
	if err := e.c.sealFrame(e.w, e.streamID, e.counter, final, e.buf); err != nil {
		return err
	}
	e.buf = e.buf[:0]
	if !final {
		e.counter++
	}
	return nil
}

// Close flushes the final frame. It is idempotent.
func (e *encryptWriter) Close() error {
	if e.closed {
		return e.err
	}
	e.closed = true
	if e.err != nil {
		return e.err
	}
	// Always emit an authenticated final frame (possibly empty) so the
	// reader terminates on the final marker rather than a bare EOF.
	if err := e.flush(true); err != nil {
		e.err = err
	}
	return e.err
}

// decryptReader is the io.Reader returned by NewDecryptReader.
type decryptReader struct {
	c        *Cipher
	r        io.Reader
	streamID []byte
	counter  uint32
	plain    []byte
	done     bool
	inited   bool
	err      error
}

// NewDecryptReader returns a reader that opens the chunked container written
// by NewEncryptWriter, serving the decrypted plaintext. The stream header is
// consumed lazily on the first Read. Requires an available Cipher.
func (c *Cipher) NewDecryptReader(r io.Reader) io.Reader {
	return &decryptReader{c: c, r: r}
}

func (d *decryptReader) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if !d.inited {
		if err := d.init(); err != nil {
			d.err = err
			return 0, err
		}
	}
	for len(d.plain) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.nextFrame(); err != nil {
			d.err = err
			return 0, err
		}
	}
	n := copy(p, d.plain)
	d.plain = d.plain[n:]
	return n, nil
}

func (d *decryptReader) init() error {
	if !d.c.Available() {
		return errNoKey
	}
	d.streamID = make([]byte, streamIDSize)
	if _, err := io.ReadFull(d.r, d.streamID); err != nil {
		return fmt.Errorf("secret: read stream id: %w", err)
	}
	d.inited = true
	return nil
}

func (d *decryptReader) nextFrame() error {
	hdr := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(d.r, hdr); err != nil {
		// An io.EOF here means the stream ended before an authenticated
		// final frame — i.e. it was truncated.
		return fmt.Errorf("secret: read frame header (truncated stream?): %w", err)
	}
	final := hdr[0]&frameFinalFlag != 0
	ctLen := binary.BigEndian.Uint32(hdr[1:])
	if int(ctLen) > streamChunkSize+d.c.aead.Overhead() {
		return fmt.Errorf("secret: frame %d length %d exceeds bound", d.counter, ctLen)
	}
	ct := make([]byte, ctLen)
	if _, err := io.ReadFull(d.r, ct); err != nil {
		return fmt.Errorf("secret: read frame body: %w", err)
	}
	plain, err := d.c.aead.Open(nil, streamNonce(d.streamID, d.counter), ct, streamAAD(d.counter, final))
	if err != nil {
		return fmt.Errorf("secret: open frame %d: %w", d.counter, err)
	}
	d.plain = plain
	if final {
		d.done = true
	} else {
		d.counter++
	}
	return nil
}
