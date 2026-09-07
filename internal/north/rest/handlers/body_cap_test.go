// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// oversizedJSONBody builds a syntactically valid JSON object whose padding
// member pushes it past the shared request-body ceiling. The leading
// members are the ones the handler under test actually reads, so a
// handler without the cap decodes it happily and answers 2xx.
func oversizedJSONBody(leading string) []byte {
	var b bytes.Buffer
	b.WriteString("{")
	b.WriteString(leading)
	b.WriteString(`"pad":"`)
	b.WriteString(strings.Repeat("x", maxRequestBodyBytes+1))
	b.WriteString(`"}`)
	return b.Bytes()
}

// stubGroupsWriter is the minimal [GroupsWriter] the body-cap test needs.
type stubGroupsWriter struct{ created int }

func (s *stubGroupsWriter) CreateGroup(context.Context, string, CreateGroupRequest) (GroupEntry, error) {
	s.created++
	return GroupEntry{}, nil
}

func (*stubGroupsWriter) UpdateGroup(context.Context, string, int, UpdateGroupRequest) error {
	return nil
}
func (*stubGroupsWriter) DeleteGroup(context.Context, string, int) error { return nil }
func (*stubGroupsWriter) SuitableMembers(context.Context, string, string) (SuitableMembersResponse, error) {
	return SuitableMembersResponse{}, nil
}

func (*stubGroupsWriter) GroupTypes(context.Context, string) ([]GroupTypeEntry, error) {
	return nil, nil
}

// TestBodyDecodersCapTheRequestBody pins that the three handlers which
// decode their JSON body directly — instead of through [DecodeJSON] —
// enforce the same 1 MiB ceiling every other write handler does, and
// answer 413 rather than buffering an arbitrarily large body. Without the
// cap they are safe only while the OpenAPI request validator is mounted
// in front of them, which `openapi_validate: false` or a missing spec
// file removes.
func TestBodyDecodersCapTheRequestBody(t *testing.T) {
	t.Parallel()

	groups := &stubGroupsWriter{}
	position := &fakeCCUPositionSetter{}
	dev := newChannelFlagsTestDevice(t, "0001ABCD")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": dev}}
	flags := &fakeChannelFlagsWriter{}

	cases := []struct {
		name    string
		handler http.Handler
		body    []byte
		params  map[string]string
	}{
		{
			name:    "PUT /groups/{id}",
			handler: UpdateGroup(groups, nil),
			body:    oversizedJSONBody(`"name":"g",`),
			params:  map[string]string{"id": "7"},
		},
		{
			name:    "PUT /system/ccu/{central}/position",
			handler: PutCCUPosition(position, nil),
			body:    oversizedJSONBody(`"longitude":10,"latitude":50,`),
			params:  map[string]string{"central": "ccu1"},
		},
		{
			name:    "PUT /devices/{addr}/channels/{no}/flags",
			handler: PutChannelFlags(idx, flags, channelflags.New(), nil),
			body:    oversizedJSONBody(`"hidden":true,`),
			params:  map[string]string{"addr": "0001ABCD", "no": "1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(chiContext(req, tc.params))
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, req)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized body: expected 413, got %d body=%.200s", w.Code, w.Body.String())
			}
		})
	}
}
