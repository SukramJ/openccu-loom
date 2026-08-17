// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
)

// ─── local DTO types ──────────────────────────────────────────────────────────

// deviceSummary is a minimal projection of the REST API's DeviceSummary shape.
// Only the fields needed for CLI display are decoded; extra fields are ignored.
type deviceSummary struct {
	Address       string `json:"address"`
	Central       string `json:"central,omitempty"`
	Interface     string `json:"interface"`
	Model         string `json:"model"`
	ModelLabel    string `json:"model_label,omitempty"`
	Name          string `json:"name"`
	Available     bool   `json:"available"`
	ChannelsCount int    `json:"channels_count"`
}

// channelSummary is a minimal projection of the REST API's ChannelSummary.
type channelSummary struct {
	Address string `json:"address"`
	Number  int    `json:"number"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
}

// deviceDetail embeds deviceSummary and adds the channels list.
type deviceDetail struct {
	deviceSummary
	Channels []channelSummary `json:"channels"`
}

// deviceListResponse is the paginated envelope returned by GET /api/v1/devices.
type deviceListResponse struct {
	Items   []deviceSummary `json:"items"`
	Total   int             `json:"total"`
	Page    int             `json:"page"`
	PerPage int             `json:"per_page"`
}

// dataPointSummary is a minimal projection of the REST API's DataPointSummary.
type dataPointSummary struct {
	Parameter string `json:"parameter"`
	Value     any    `json:"value"`
	Observed  bool   `json:"observed"`
}

// setValueRequest is the body for PUT …/data-points/{param}/value.
type setValueRequest struct {
	Value    any    `json:"value"`
	Priority string `json:"priority,omitempty"`
}

// ─── dispatcher ───────────────────────────────────────────────────────────────

// cmdDevices dispatches `devices <op> [args…]` to the appropriate handler.
func cmdDevices(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("devices: missing operation (try: list, get, get-value, set)")
	}
	switch args[0] {
	case "list":
		return cmdDevicesList(args[1:], stdout, stderr)
	case "get":
		return cmdDevicesGet(args[1:], stdout, stderr)
	case "get-value":
		return cmdDevicesGetValue(args[1:], stdout, stderr)
	case "set":
		return cmdDevicesSet(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("devices: unknown operation %q", args[0])
	}
}

// ─── list ─────────────────────────────────────────────────────────────────────

func cmdDevicesList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("devices list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	items, total, err := fetchAllDevices(ctx, client)
	if err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, items)
	}
	return printDeviceList(stdout, items, total)
}

// devicesListPageSize is the per-request page size `devices list` asks for.
// It is the daemon's documented upper bound (parsePagination clamps anything
// larger back to the 50 default), so a fleet of any realistic size needs only
// a handful of round trips.
const devicesListPageSize = 500

// fetchAllDevices walks GET /api/v1/devices page by page until every device
// the envelope's `total` announces has been collected.
//
// The endpoint paginates with a default of 50 per page, so a single unqualified
// request returns the first 50 devices and nothing else — silently, since the
// envelope's `total` still reports the full fleet. A CLI that shows a table (or
// emits JSON a script greps for a device address) must not stop there.
// The accumulator starts non-nil so an empty fleet marshals as `[]` rather
// than `null`: scripts pipe `devices list --json` into `jq '.[]'`, which aborts
// on a null document but yields nothing on an empty array.
func fetchAllDevices(ctx context.Context, client *daemonClient) (items []deviceSummary, total int, err error) {
	items = []deviceSummary{}
	for page := 1; ; page++ {
		var resp deviceListResponse
		path := fmt.Sprintf("/api/v1/devices?page=%d&per_page=%d", page, devicesListPageSize)
		if err := client.getJSON(ctx, path, &resp); err != nil {
			return nil, 0, err
		}
		total = resp.Total
		items = append(items, resp.Items...)
		// A short page is the last one; an empty page ends the walk even if
		// `total` and the sliced items disagree, so a mid-walk device removal
		// cannot spin this loop forever.
		if len(resp.Items) == 0 || len(resp.Items) < devicesListPageSize || len(items) >= total {
			return items, total, nil
		}
	}
}

// printDeviceList renders a human-readable table. When items span more than one
// central the Central column is included so operators can distinguish them.
func printDeviceList(w io.Writer, items []deviceSummary, total int) error {
	multiCentral := false
	if len(items) > 0 {
		first := items[0].Central
		for _, d := range items[1:] {
			if d.Central != first {
				multiCentral = true
				break
			}
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if multiCentral {
		_, _ = fmt.Fprintln(tw, "ADDRESS\tMODEL\tNAME\tINTERFACE\tCENTRAL\tAVAILABLE")
	} else {
		_, _ = fmt.Fprintln(tw, "ADDRESS\tMODEL\tNAME\tINTERFACE\tAVAILABLE")
	}
	for _, d := range items {
		avail := "yes"
		if !d.Available {
			avail = "no"
		}
		if multiCentral {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				sanitizeForTerminal(d.Address), sanitizeForTerminal(d.Model),
				sanitizeForTerminal(d.Name), sanitizeForTerminal(d.Interface),
				sanitizeForTerminal(d.Central), avail)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				sanitizeForTerminal(d.Address), sanitizeForTerminal(d.Model),
				sanitizeForTerminal(d.Name), sanitizeForTerminal(d.Interface), avail)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("devices list: flush table: %w", err)
	}
	_, _ = fmt.Fprintf(w, "total: %d\n", total)
	return nil
}

// ─── get ──────────────────────────────────────────────────────────────────────

func cmdDevicesGet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("devices get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("devices get: missing <address>")
	}
	addr := rest[0]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	var detail deviceDetail
	if err := client.getJSON(ctx, "/api/v1/devices/"+url.PathEscape(addr), &detail); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, detail)
	}
	return printDeviceDetail(stdout, detail)
}

func printDeviceDetail(w io.Writer, d deviceDetail) error {
	avail := "yes"
	if !d.Available {
		avail = "no"
	}
	_, _ = fmt.Fprintf(w, "Address:   %s\n", sanitizeForTerminal(d.Address))
	_, _ = fmt.Fprintf(w, "Model:     %s\n", sanitizeForTerminal(d.Model))
	_, _ = fmt.Fprintf(w, "Name:      %s\n", sanitizeForTerminal(d.Name))
	_, _ = fmt.Fprintf(w, "Interface: %s\n", sanitizeForTerminal(d.Interface))
	if d.Central != "" {
		_, _ = fmt.Fprintf(w, "Central:   %s\n", sanitizeForTerminal(d.Central))
	}
	_, _ = fmt.Fprintf(w, "Available: %s\n", avail)
	if len(d.Channels) > 0 {
		_, _ = fmt.Fprintln(w, "Channels:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  NO\tADDRESS\tTYPE\tNAME")
		for _, ch := range d.Channels {
			_, _ = fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", ch.Number,
				sanitizeForTerminal(ch.Address), sanitizeForTerminal(ch.Type), sanitizeForTerminal(ch.Name))
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("devices get: flush table: %w", err)
		}
	}
	return nil
}

// ─── get-value ────────────────────────────────────────────────────────────────

func cmdDevicesGetValue(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("devices get-value", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 3 {
		return errors.New("devices get-value: usage: get-value <address> <channel> <parameter>")
	}
	addr, ch, param := rest[0], rest[1], rest[2]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	path := fmt.Sprintf("/api/v1/devices/%s/channels/%s/data-points/%s",
		url.PathEscape(addr), url.PathEscape(ch), url.PathEscape(param))
	var dp dataPointSummary
	if err := client.getJSON(ctx, path, &dp); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, dp)
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", sanitizeValue(dp.Value))
	return nil
}

// ─── set ──────────────────────────────────────────────────────────────────────

// validCommandPriorities is the REST API's SetValueRequest.priority enum
// (assets/openapi.yaml). A value outside this set is rejected by the
// server's OpenAPI request validator — or, with validation disabled,
// silently coerced to "high" (see parsePriority) — neither of which is the
// value the operator asked for, so the CLI validates locally and fails with
// a usable message instead of relaying either outcome.
var validCommandPriorities = map[string]bool{
	"critical": true,
	"high":     true,
	"default":  true,
	"low":      true,
}

func cmdDevicesSet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("devices set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	priority := fs.String("priority", "", "command priority (critical, high, default, low)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 4 {
		return errors.New("devices set: usage: set <address> <channel> <parameter> <value>")
	}
	if *priority != "" && !validCommandPriorities[*priority] {
		return fmt.Errorf("devices set: invalid --priority %q (must be one of critical, high, default, low)", *priority)
	}
	addr, ch, param, rawVal := rest[0], rest[1], rest[2], rest[3]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	body := setValueRequest{
		Value:    coerceValue(rawVal),
		Priority: *priority,
	}
	path := fmt.Sprintf("/api/v1/devices/%s/channels/%s/data-points/%s/value",
		url.PathEscape(addr), url.PathEscape(ch), url.PathEscape(param))
	if err := client.sendJSON(ctx, http.MethodPut, path, body, nil); err != nil {
		return err
	}
	return writeOKResult(stdout, f.jsonOut, map[string]any{
		"address": addr, "channel": ch, "parameter": param, "value": body.Value,
	})
}

// coerceValue converts a raw string argument to the most specific Go type the
// REST API will accept: bool → int → float64 → string.
func coerceValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// writeJSON encodes v as indented JSON to w.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeOKResult reports a write/action command's success. With --json it
// emits `{"status":"ok", ...fields}` so a script piping into jq can rely on
// a parseable object; without it, it keeps the plain "ok" line every
// existing script already expects.
func writeOKResult(w io.Writer, jsonOut bool, fields map[string]any) error {
	if !jsonOut {
		_, err := fmt.Fprintln(w, "ok")
		return err
	}
	out := make(map[string]any, len(fields)+1)
	out["status"] = "ok"
	for k, v := range fields {
		out[k] = v
	}
	return writeJSON(w, out)
}
