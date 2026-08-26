// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientDecodesLatin1Body reproduces a CCU JSON-RPC method (e.g.
// Program.getAll) returning an ISO-8859-1 body — 0xFC is Latin-1 "ü" — despite
// JSON's UTF-8 requirement. Without transcoding, json.Unmarshal replaces the
// high byte with U+FFFD and the name renders as "Sp�le". The client must
// recover "Spüle".
func TestClientDecodesLatin1Body(t *testing.T) {
	latin1Body := []byte("{\"result\":[{\"name\":\"Sp\xfcle\"}]}")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(latin1Body)
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Name string `json:"name"`
	}
	if err := c.Call(context.Background(), "Program.getAll", nil, &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Spüle" {
		t.Fatalf("got %+v, want name=Spüle", got)
	}
}

// TestClientKeepsValidUTF8Body verifies a proper UTF-8 body is left untouched
// (the utf8.Valid guard means the transcoder never runs on it).
func TestClientKeepsValidUTF8Body(t *testing.T) {
	utf8Body := []byte("{\"result\":[{\"name\":\"Küche\"}]}") // real UTF-8 ü
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(utf8Body)
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	var got []struct {
		Name string `json:"name"`
	}
	if err := c.Call(context.Background(), "Program.getAll", nil, &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Küche" {
		t.Fatalf("got %+v, want name=Küche", got)
	}
}

func TestLatin1ToUTF8(t *testing.T) {
	if got := string(latin1ToUTF8([]byte("Sp\xfcle"))); got != "Spüle" {
		t.Fatalf("latin1ToUTF8 = %q, want Spüle", got)
	}
	if got := string(latin1ToUTF8([]byte("plain-ascii"))); got != "plain-ascii" {
		t.Fatalf("ASCII must map 1:1, got %q", got)
	}
}
