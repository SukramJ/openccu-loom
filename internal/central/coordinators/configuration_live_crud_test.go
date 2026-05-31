// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// -- fakes -------------------------------------------------------------------

type fakeParamsetReader struct {
	values map[string]any
	err    error
}

func (f *fakeParamsetReader) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return f.values, f.err
}

type fakeParamsetWriter struct {
	written map[string]any
	err     error
}

func (f *fakeParamsetWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any) error {
	f.written = values
	return f.err
}

type fakeLinkFetcher struct {
	params hmproto.Paramset
	err    error
}

func (f *fakeLinkFetcher) GetLinkParamsetDescription(_ context.Context, _ string) (hmproto.Paramset, error) {
	return f.params, f.err
}

// -- L-A5-v13-07: GetParamset -----------------------------------------------

// TestGetParamsetDelegatesToReader verifies that GetParamset passes through
// the live values returned by the reader.
func TestGetParamsetDelegatesToReader(t *testing.T) {
	c, _, _ := newCoord(t)
	reader := &fakeParamsetReader{values: map[string]any{"LEVEL": 0.5}}

	got, err := c.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["LEVEL"] != 0.5 {
		t.Fatalf("expected LEVEL=0.5, got %v", got["LEVEL"])
	}
}

// TestGetParamsetNilReaderError verifies that a nil reader is rejected.
func TestGetParamsetNilReaderError(t *testing.T) {
	c, _, _ := newCoord(t)
	_, err := c.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, nil)
	if err == nil {
		t.Fatal("expected error for nil reader")
	}
}

// TestGetParamsetPropagatesBackendError verifies backend errors are wrapped.
func TestGetParamsetPropagatesBackendError(t *testing.T) {
	c, _, _ := newCoord(t)
	want := errors.New("rpc timeout")
	reader := &fakeParamsetReader{err: want}
	_, err := c.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, reader)
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

// -- L-A5-v13-08: PutParamset -----------------------------------------------

// TestPutParamsetDelegatesToWriter verifies that PutParamset forwards all
// values when no validation constraint is hit.
func TestPutParamsetDelegatesToWriter(t *testing.T) {
	c, _, _ := newCoord(t)
	writer := &fakeParamsetWriter{}
	values := map[string]any{"TRANSMIT_TRY_MAX": 10}

	res, err := c.PutParamset(context.Background(), hmenum.InterfaceHmIPRF, "0001ABCD:1",
		hmenum.ParamsetKeyMaster, values, false, nil, writer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatal("expected success")
	}
	if res.ParametersWritten != 1 {
		t.Fatalf("expected 1 parameter written, got %d", res.ParametersWritten)
	}
	if writer.written["TRANSMIT_TRY_MAX"] != 10 {
		t.Fatalf("value not forwarded to writer: %v", writer.written)
	}
}

// TestPutParamsetValidationRejectsOutOfBounds verifies that integer values
// below min produce a validation failure without touching the writer.
func TestPutParamsetValidationRejectsOutOfBounds(t *testing.T) {
	c, pss, _ := newCoord(t)
	pss.Put(hmenum.InterfaceHmIPRF, "0001ABCD:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"TRANSMIT_TRY_MAX": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Min:        []byte("1"),
			Max:        []byte("10"),
			Operations: hmenum.Operations(0b111),
		},
	})
	writer := &fakeParamsetWriter{}
	values := map[string]any{"TRANSMIT_TRY_MAX": 0}

	res, err := c.PutParamset(context.Background(), hmenum.InterfaceHmIPRF, "0001ABCD:1",
		hmenum.ParamsetKeyMaster, values, true, nil, writer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure for out-of-bounds value")
	}
	if _, ok := res.ValidationErrors["TRANSMIT_TRY_MAX"]; !ok {
		t.Fatalf("expected validation error for TRANSMIT_TRY_MAX, got %+v", res.ValidationErrors)
	}
	if writer.written != nil {
		t.Fatal("writer must not be called when validation fails")
	}
}

// TestPutParamsetNilWriterError verifies that a nil writer is rejected.
func TestPutParamsetNilWriterError(t *testing.T) {
	c, _, _ := newCoord(t)
	_, err := c.PutParamset(context.Background(), hmenum.InterfaceHmIPRF, "0001ABCD:1",
		hmenum.ParamsetKeyMaster, map[string]any{}, false, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil writer")
	}
}

// -- L-A5-v13-09: GetLinkParamsetDescription --------------------------------

// TestGetLinkParamsetDescriptionDelegatesToFetcher verifies result construction.
func TestGetLinkParamsetDescriptionDelegatesToFetcher(t *testing.T) {
	c, _, _ := newCoord(t)
	params := hmproto.Paramset{
		"PEER_PARAM": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger},
	}
	fetcher := &fakeLinkFetcher{params: params}

	desc, err := c.GetLinkParamsetDescription(context.Background(), "0001ABCD:1", fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc.ChannelAddress != "0001ABCD:1" {
		t.Fatalf("unexpected ChannelAddress: %q", desc.ChannelAddress)
	}
	if desc.ParamsetKey != hmenum.ParamsetKeyLink {
		t.Fatalf("unexpected ParamsetKey: %q", desc.ParamsetKey)
	}
	if _, ok := desc.Parameters["PEER_PARAM"]; !ok {
		t.Fatalf("PEER_PARAM missing from result: %+v", desc.Parameters)
	}
}

// TestGetLinkParamsetDescriptionNilFetcherError verifies nil fetcher rejection.
func TestGetLinkParamsetDescriptionNilFetcherError(t *testing.T) {
	c, _, _ := newCoord(t)
	_, err := c.GetLinkParamsetDescription(context.Background(), "0001ABCD:1", nil)
	if err == nil {
		t.Fatal("expected error for nil fetcher")
	}
}

// TestGetLinkParamsetDescriptionPropagatesError verifies error forwarding.
func TestGetLinkParamsetDescriptionPropagatesError(t *testing.T) {
	c, _, _ := newCoord(t)
	want := errors.New("rpc error")
	fetcher := &fakeLinkFetcher{err: want}
	_, err := c.GetLinkParamsetDescription(context.Background(), "0001ABCD:1", fetcher)
	if !errors.Is(err, want) {
		t.Fatalf("expected %v wrapped in error, got %v", want, err)
	}
}
