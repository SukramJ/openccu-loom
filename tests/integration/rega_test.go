// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// Package integration — Rega runner integration tests.
//
// These tests exercise openccu-loom's rega.Runner against the godevccu
// OpenCCU fixture. godevccu implements a pattern-matching ReGa engine
// (not a full interpreter) that recognises the scripts shipped with
// openccu-loom by matching on embedded keywords or comment headers.
//
// Script matching notes (relevant for the tests below):
//
// - get_backend_info.fn: body contains both "grep VERSION= /VERSION" and
// "grep PRODUCT= /VERSION", which satisfies godevccu's
// `(?is)grep.*VERSION.*grep.*PRODUCT` pattern → returns JSON.
//
// - get_serial.fn: body uses system.Exec to cat /var/board_sgtin etc.
// godevccu matches on `(?i)\bget_serial(\.fn)?\b` (the script filename
// appears in the header "!# openccu-loom — get_serial") → returns JSON.
package integration

import (
	"context"
	neturl "net/url"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newRegaRunner constructs a rega.Runner using a fresh JSON-RPC client for
// the given endpoint. The caller is responsible for logging in the returned
// client before invoking scripts that require an authenticated session.
func newRegaRunner(t *testing.T, endpoint, username, password string) (*rega.Runner, *jsonrpc.Client) {
	t.Helper()
	c, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: endpoint,
		Username: username,
		Password: password,
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	r, err := rega.NewRunner(rega.Config{Client: c})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return r, c
}

func ctx5sRega(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// ─────────────────────────────────────────────────────────────────────────────
// get_backend_info
// ─────────────────────────────────────────────────────────────────────────────

// TestRegaScriptGetBackendInfo exercises the full Rega dispatch path for the
// get_backend_info script. godevccu's pattern engine recognises the "grep
// VERSION= /VERSION … grep PRODUCT= /VERSION" idiom and returns a synthetic
// JSON payload with version, product, hostname and is_ha_addon fields.
func TestRegaScriptGetBackendInfo(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	runner, c := newRegaRunner(t, url, "Admin", "")
	ctx, cancel := ctx5sRega(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var info struct {
		Version   string `json:"version"`
		Product   string `json:"product"`
		Hostname  string `json:"hostname"`
		IsHaAddon bool   `json:"is_ha_addon"`
	}
	if err := runner.RunJSON(ctx, hmenum.RegaScriptGetBackendInfo, nil, &info); err != nil {
		t.Fatalf("RunJSON(get_backend_info): %v", err)
	}
	if info.Version == "" {
		t.Error("get_backend_info: version field is empty")
	}
	if info.Product == "" {
		t.Error("get_backend_info: product field is empty")
	}
	t.Logf("backend info: version=%q product=%q hostname=%q is_ha_addon=%v",
		info.Version, info.Product, info.Hostname, info.IsHaAddon)
}

// ─────────────────────────────────────────────────────────────────────────────
// get_serial
// ─────────────────────────────────────────────────────────────────────────────

// TestRegaScriptGetSerial verifies that the get_serial script is recognised
// by godevccu's rega engine and returns a non-empty serial string.
//
// godevccu's pattern `(?i)\bget_serial(\.fn)?\b` matches both the filename
// token in the script header ("!# openccu-loom — get_serial") and any
// legacy "name: get_serial.fn" style, so this test now passes without skipping.
func TestRegaScriptGetSerial(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	runner, c := newRegaRunner(t, url, "Admin", "")
	ctx, cancel := ctx5sRega(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var result struct {
		Serial string `json:"serial"`
	}
	if err := runner.RunJSON(ctx, hmenum.RegaScriptGetSerial, nil, &result); err != nil {
		t.Fatalf("RunJSON(get_serial): %v", err)
	}
	if result.Serial == "" {
		t.Fatal("get_serial: serial field is empty")
	}
	t.Logf("serial: %q", result.Serial)
}

// ─────────────────────────────────────────────────────────────────────────────
// get_program_descriptions — exercising the rega→program path
// ─────────────────────────────────────────────────────────────────────────────

// TestRegaScriptGetProgramDescriptions verifies that the get_program_descriptions
// script executes without error over the OpenCCU fixture. The godevccu state
// manager is pre-seeded with SetupDefaults which may or may not add programs;
// either an empty slice or a populated one is acceptable — the test asserts
// only that the script dispatches and the JSON is well-formed.
func TestRegaScriptGetProgramDescriptions(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	runner, c := newRegaRunner(t, url, "Admin", "")
	ctx, cancel := ctx5sRega(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var programs []map[string]any
	if err := runner.RunJSON(ctx, hmenum.RegaScriptGetProgramDescriptions, nil, &programs); err != nil {
		t.Fatalf("RunJSON(get_program_descriptions): %v", err)
	}
	t.Logf("get_program_descriptions: %d programs", len(programs))
}

// ─────────────────────────────────────────────────────────────────────────────
// get_system_variable_descriptions — exercising the rega→sysvar path
// ─────────────────────────────────────────────────────────────────────────────

// TestRegaScriptGetSystemVariableDescriptions verifies the sysvar script
// dispatches correctly and round-trips the explicit channel assignment
// (CCU WebUI "Kanalzuordnung") end-to-end through the typed runner DTO:
// string-framed ids, URL-encoded description, and channel_address for
// an assigned variable (empty for an unassigned one).
func TestRegaScriptGetSystemVariableDescriptions(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	runner, c := newRegaRunner(t, url, "Admin", "")
	ctx, cancel := ctx5sRega(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	srv.v.State().AddSystemVariable("svRainCounterToday", "FLOAT", 1.5, godevccu.AddSystemVariableOpts{
		ID:             52574,
		Description:    "HAHM",
		ChannelAddress: "VCU0000123:1",
	})
	srv.v.State().AddSystemVariable("svPlain", "BOOL", false, godevccu.AddSystemVariableOpts{
		ID: 52575,
	})

	descs, err := runner.GetSystemVariableDescriptions(ctx)
	if err != nil {
		t.Fatalf("GetSystemVariableDescriptions: %v", err)
	}
	byID := make(map[string]rega.SystemVariableDescription, len(descs))
	for _, d := range descs {
		byID[d.ID] = d
	}

	assigned, ok := byID["52574"]
	if !ok {
		t.Fatalf("seeded sysvar 52574 missing from descriptions (string-id framing broken?): %v", descs)
	}
	ch, err := neturl.QueryUnescape(assigned.ChannelAddress)
	if err != nil {
		t.Fatalf("channel_address not URL-decodable: %v", err)
	}
	if ch != "VCU0000123:1" {
		t.Fatalf("channel_address = %q, want VCU0000123:1", ch)
	}
	desc, err := neturl.QueryUnescape(assigned.Description)
	if err != nil {
		t.Fatalf("description not URL-decodable: %v", err)
	}
	if desc != "HAHM" {
		t.Fatalf("description = %q, want HAHM", desc)
	}

	plain, ok := byID["52575"]
	if !ok {
		t.Fatalf("seeded sysvar 52575 missing from descriptions: %v", descs)
	}
	if plain.ChannelAddress != "" {
		t.Fatalf("unassigned sysvar channel_address = %q, want empty", plain.ChannelAddress)
	}
}
