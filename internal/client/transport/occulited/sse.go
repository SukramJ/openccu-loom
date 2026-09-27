// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Server-sent events framing, as both of the box's streams use it: lines
// end in LF, CRLF or a lone CR; "field: value" lines (one space after
// the colon is dropped) build a frame; a line starting with ":" is a
// comment; a blank line ends the frame. data lines join with "\n". A
// frame cut off by the end of the stream is discarded.

// DefaultMaxLine bounds one line of a stream, so a peer that never sends
// a line end cannot grow the buffer without limit.
const DefaultMaxLine = 1 << 20

// ErrLineTooLong reports a stream line above the parser's limit.
var ErrLineTooLong = errors.New("occulited: event-stream line too long")

// Frame is one event-stream frame. A block that held only comments is a
// frame too (Comments set, no fields), because the box's heartbeat is
// such a block and a reader needs to see it.
type Frame struct {
	// ID is the id field; HasID tells an empty id from none.
	ID    string
	HasID bool
	// Event is the event field ("" when absent).
	Event string
	// Data is the data lines joined with "\n"; HasData tells an empty
	// data line from none.
	Data    string
	HasData bool
	// Retry is the retry field in milliseconds, -1 when absent or not a
	// number.
	Retry int
	// Comments are the comment lines without the leading ":" and one
	// following space.
	Comments []string
}

// empty reports whether the frame collected nothing.
func (f *Frame) empty() bool {
	return !f.HasID && f.Event == "" && !f.HasData && f.Retry < 0 && len(f.Comments) == 0
}

// Parser is an incremental event-stream parser: feed it bytes in any
// split, it emits every completed frame. Not safe for concurrent use.
type Parser struct {
	maxLine   int
	line      []byte
	skipLF    bool
	cur       Frame
	dataLines []string
}

// NewParser returns a parser with a line limit (<= 0 means
// [DefaultMaxLine]).
func NewParser(maxLine int) *Parser {
	if maxLine <= 0 {
		maxLine = DefaultMaxLine
	}
	p := &Parser{maxLine: maxLine}
	p.reset()
	return p
}

func (p *Parser) reset() {
	p.cur = Frame{Retry: -1}
	p.dataLines = p.dataLines[:0]
}

// Feed parses b and calls emit for every frame it completes. An error
// from emit stops parsing and is returned; so is [ErrLineTooLong].
func (p *Parser) Feed(b []byte, emit func(Frame) error) error {
	for len(b) > 0 {
		if p.skipLF {
			p.skipLF = false
			if b[0] == '\n' {
				b = b[1:]
				continue
			}
		}
		i := bytes.IndexAny(b, "\r\n")
		if i < 0 {
			if len(p.line)+len(b) > p.maxLine {
				return ErrLineTooLong
			}
			p.line = append(p.line, b...)
			return nil
		}
		if len(p.line)+i > p.maxLine {
			return ErrLineTooLong
		}
		p.line = append(p.line, b[:i]...)
		p.skipLF = b[i] == '\r'
		b = b[i+1:]
		line := string(p.line)
		p.line = p.line[:0]
		if err := p.processLine(line, emit); err != nil {
			return err
		}
	}
	return nil
}

// processLine applies one complete line.
func (p *Parser) processLine(line string, emit func(Frame) error) error {
	if line == "" {
		if p.cur.empty() {
			return nil
		}
		f := p.cur
		if p.cur.HasData {
			f.Data = strings.Join(p.dataLines, "\n")
		}
		if len(p.cur.Comments) > 0 {
			f.Comments = append([]string(nil), p.cur.Comments...)
		}
		p.reset()
		return emit(f)
	}
	if line[0] == ':' {
		p.cur.Comments = append(p.cur.Comments, strings.TrimPrefix(line[1:], " "))
		return nil
	}
	field, value, _ := strings.Cut(line, ":")
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "id":
		// An id with a NUL is ignored, as the event-stream format says.
		if !strings.ContainsRune(value, 0) {
			p.cur.ID, p.cur.HasID = value, true
		}
	case "event":
		p.cur.Event = value
	case "data":
		p.cur.HasData = true
		p.dataLines = append(p.dataLines, value)
	case "retry":
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			p.cur.Retry = n
		}
	}
	return nil
}

// FrameReader pulls frames out of a stream.
type FrameReader struct {
	r       io.Reader
	p       *Parser
	buf     []byte
	pending []Frame
	err     error
}

// NewFrameReader reads frames from r with the default line limit.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: r, p: NewParser(0), buf: make([]byte, 4096)}
}

// Next returns the next frame. At the end of the stream it returns
// io.EOF (an unterminated last frame is dropped); a read error is
// returned as is.
func (fr *FrameReader) Next() (Frame, error) {
	for len(fr.pending) == 0 {
		if fr.err != nil {
			return Frame{}, fr.err
		}
		n, err := fr.r.Read(fr.buf)
		if n > 0 {
			if perr := fr.p.Feed(fr.buf[:n], func(f Frame) error {
				fr.pending = append(fr.pending, f)
				return nil
			}); perr != nil {
				fr.err = perr
			}
		}
		if err != nil && fr.err == nil {
			fr.err = err
		}
	}
	f := fr.pending[0]
	fr.pending = fr.pending[1:]
	return f, nil
}
