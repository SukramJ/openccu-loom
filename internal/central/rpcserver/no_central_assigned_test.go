// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"testing"
)

func TestNoCentralAssignedTrueWhenEmpty(t *testing.T) {
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	if !srv.NoCentralAssigned() {
		t.Fatal("NoCentralAssigned should be true when no centrals registered")
	}
}

func TestNoCentralAssignedFalseAfterRegister(t *testing.T) {
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	srv.Register("ccu1", nil)
	if srv.NoCentralAssigned() {
		t.Fatal("NoCentralAssigned should be false after Register")
	}
	srv.Deregister("ccu1")
	if !srv.NoCentralAssigned() {
		t.Fatal("NoCentralAssigned should be true after Deregister removes last central")
	}
}
