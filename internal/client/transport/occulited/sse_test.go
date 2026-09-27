// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

func parseAll(t *testing.T, input string) []occulited.Frame {
	t.Helper()
	fr := occulited.NewFrameReader(strings.NewReader(input))
	var out []occulited.Frame
	for {
		f, err := fr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, f)
	}
}

func TestSSEParserFramesTheBoxStream(t *testing.T) {
	in := ": connected\n\n" +
		"id: 0123456789abcdef-7\nevent: hello\ndata: {\"seq\":7}\n\n" +
		": ping\n\n" +
		"event: resync\ndata: {\"reason\":\"gap\"}\n\n" +
		"data: {\"revision\":3}\n\n"
	got := parseAll(t, in)
	want := []occulited.Frame{
		{Retry: -1, Comments: []string{"connected"}},
		{ID: "0123456789abcdef-7", HasID: true, Event: "hello", Data: `{"seq":7}`, HasData: true, Retry: -1},
		{Retry: -1, Comments: []string{"ping"}},
		{Event: "resync", Data: `{"reason":"gap"}`, HasData: true, Retry: -1},
		{Data: `{"revision":3}`, HasData: true, Retry: -1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames\n got %+v\nwant %+v", got, want)
	}
}

func TestSSEParserLineEndingsMultiLineDataAndFields(t *testing.T) {
	in := "data: a\r\ndata:b\rdata\n\r\n" + // CRLF, lone CR, bare field name
		"id\nretry: 1500\nevent:x\nunknown: y\n\n" +
		"id: with\x00nul\ndata: z\n\n" +
		"data: cut off"
	got := parseAll(t, in)
	want := []occulited.Frame{
		{Data: "a\nb\n", HasData: true, Retry: -1},
		{ID: "", HasID: true, Event: "x", Retry: 1500},
		{Data: "z", HasData: true, Retry: -1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames\n got %+v\nwant %+v", got, want)
	}
}

func TestSSEParserRefusesAnEndlessLine(t *testing.T) {
	p := occulited.NewParser(8)
	err := p.Feed([]byte("data: 0123456789"), func(occulited.Frame) error { return nil })
	if !errors.Is(err, occulited.ErrLineTooLong) {
		t.Errorf("err %v, want ErrLineTooLong", err)
	}
}

// FuzzSSEFrameParser checks that the parser never panics and that the
// frames it emits do not depend on how the input is split into feeds:
// the byte-at-a-time run must produce exactly the whole-input result.
func FuzzSSEFrameParser(f *testing.F) {
	for _, seed := range []string{
		": connected\n\nid: a-1\nevent: hello\ndata: {}\n\n",
		"data: a\r\ndata: b\r\rdata: c\n\n",
		"id\x00\nretry: x\n:\n\n",
		"\r\n\r\n\n\r",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		collect := func(chunks [][]byte) ([]occulited.Frame, error) {
			p := occulited.NewParser(1 << 16)
			var out []occulited.Frame
			for _, c := range chunks {
				if err := p.Feed(c, func(fr occulited.Frame) error { out = append(out, fr); return nil }); err != nil {
					return out, err
				}
			}
			return out, nil
		}
		whole, errWhole := collect([][]byte{in})
		bytewise := make([][]byte, len(in))
		for i := range in {
			bytewise[i] = in[i : i+1]
		}
		split, errSplit := collect(bytewise)
		if (errWhole == nil) != (errSplit == nil) {
			t.Fatalf("error differs: whole %v, split %v", errWhole, errSplit)
		}
		if errWhole == nil && !reflect.DeepEqual(whole, split) {
			t.Fatalf("frames differ:\nwhole %+v\nsplit %+v", whole, split)
		}
		for _, fr := range whole {
			if strings.ContainsAny(fr.ID, "\r\n\x00") || strings.ContainsAny(fr.Event, "\r\n") {
				t.Fatalf("line break leaked into a field: %+v", fr)
			}
		}
	})
}
