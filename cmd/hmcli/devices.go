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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
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

// ─── shared flags ─────────────────────────────────────────────────────────────

// devicesFlags holds the common connection flags shared by all devices sub-ops.
type devicesFlags struct {
	host     string
	token    string
	user     string
	password string
	timeout  time.Duration
	jsonOut  bool
}

// bindTo registers the flags on fs and returns a pointer to the populated struct.
func (f *devicesFlags) bindTo(fs *flag.FlagSet) {
	fs.StringVar(&f.host, "host", "http://localhost:8119", "daemon REST base URL")
	fs.StringVar(&f.token, "token", "", "API bearer token")
	fs.StringVar(&f.user, "user", "", "basic-auth username")
	fs.StringVar(&f.password, "password", "", "basic-auth password")
	fs.DurationVar(&f.timeout, "timeout", 60*time.Second, "request timeout")
	fs.BoolVar(&f.jsonOut, "json", false, "emit raw JSON instead of a human-readable table")
}

func (f *devicesFlags) client() *daemonClient {
	return newDaemonClient(f.host, f.token, f.user, f.password, f.timeout)
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
	var f devicesFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	var resp deviceListResponse
	if err := f.client().getJSON(ctx, "/api/v1/devices", &resp); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, resp.Items)
	}
	return printDeviceList(stdout, resp.Items, resp.Total)
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
				d.Address, d.Model, d.Name, d.Interface, d.Central, avail)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				d.Address, d.Model, d.Name, d.Interface, avail)
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
	var f devicesFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("devices get: missing <address>")
	}
	addr := rest[0]

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	var detail deviceDetail
	if err := f.client().getJSON(ctx, "/api/v1/devices/"+addr, &detail); err != nil {
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
	_, _ = fmt.Fprintf(w, "Address:   %s\n", d.Address)
	_, _ = fmt.Fprintf(w, "Model:     %s\n", d.Model)
	_, _ = fmt.Fprintf(w, "Name:      %s\n", d.Name)
	_, _ = fmt.Fprintf(w, "Interface: %s\n", d.Interface)
	if d.Central != "" {
		_, _ = fmt.Fprintf(w, "Central:   %s\n", d.Central)
	}
	_, _ = fmt.Fprintf(w, "Available: %s\n", avail)
	if len(d.Channels) > 0 {
		_, _ = fmt.Fprintln(w, "Channels:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  NO\tADDRESS\tTYPE\tNAME")
		for _, ch := range d.Channels {
			_, _ = fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", ch.Number, ch.Address, ch.Type, ch.Name)
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
	var f devicesFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 3 {
		return errors.New("devices get-value: usage: get-value <address> <channel> <parameter>")
	}
	addr, ch, param := rest[0], rest[1], rest[2]

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	path := fmt.Sprintf("/api/v1/devices/%s/channels/%s/data-points/%s", addr, ch, param)
	var dp dataPointSummary
	if err := f.client().getJSON(ctx, path, &dp); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, dp)
	}
	_, _ = fmt.Fprintf(stdout, "%v\n", dp.Value)
	return nil
}

// ─── set ──────────────────────────────────────────────────────────────────────

func cmdDevicesSet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("devices set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f devicesFlags
	f.bindTo(fs)
	priority := fs.String("priority", "", "command priority (e.g. normal, high)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 4 {
		return errors.New("devices set: usage: set <address> <channel> <parameter> <value>")
	}
	addr, ch, param, rawVal := rest[0], rest[1], rest[2], rest[3]

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	body := setValueRequest{
		Value:    coerceValue(rawVal),
		Priority: *priority,
	}
	path := fmt.Sprintf("/api/v1/devices/%s/channels/%s/data-points/%s/value", addr, ch, param)
	if err := f.client().sendJSON(ctx, http.MethodPut, path, body, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
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
