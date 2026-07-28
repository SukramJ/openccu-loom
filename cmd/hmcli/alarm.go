// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"text/tabwriter"
)

// alarmIncidentRef mirrors hmapi.AlarmIncidentRef: the open-incident
// reference nested in an zone's live status, present only while the
// zone is triggered.
type alarmIncidentRef struct {
	ID       string `json:"id"`
	Silenced bool   `json:"silenced"`
}

// alarmZoneStatus mirrors hmapi.AlarmZoneStatus: one alarm zone's live
// status, as returned by GET /alarm/state. Only the fields this break-
// glass CLI surfaces are decoded; the rest of the wire body is ignored.
type alarmZoneStatus struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Mode     string            `json:"mode,omitempty"`
	Incident *alarmIncidentRef `json:"incident,omitempty"`
}

// alarmStateResponse is the envelope of GET /alarm/state.
type alarmStateResponse struct {
	Zones []alarmZoneStatus `json:"zones"`
}

// alarmArmRequest mirrors hmapi.AlarmArmRequest: the body of
// POST /alarm/zones/{id}/arm.
type alarmArmRequest struct {
	Mode      string `json:"mode"`
	Force     bool   `json:"force,omitempty"`
	SkipDelay bool   `json:"skip_delay,omitempty"`
}

// alarmArmAccepted mirrors hmapi.AlarmArmAccepted: the 200 response of
// POST /alarm/zones/{id}/arm.
type alarmArmAccepted struct {
	State      string `json:"state"`
	ExitDelayS int    `json:"exit_delay_s,omitempty"`
}

func cmdAlarm(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("alarm: missing operation (try: status, arm, disarm, silence, ack)")
	}
	switch args[0] {
	case "status":
		return cmdAlarmStatus(args[1:], stdout, stderr)
	case "arm":
		return cmdAlarmArm(args[1:], stdout, stderr)
	case "disarm":
		return cmdAlarmDisarm(args[1:], stdout, stderr)
	case "silence":
		return cmdAlarmSilence(args[1:], stdout, stderr)
	case "ack":
		return cmdAlarmAck(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("alarm: unknown operation %q", args[0])
	}
}

// cmdAlarmStatus is `alarm status`: GET /api/v1/alarm/state, printed as
// one row per zone (zone, state, mode, incident).
func cmdAlarmStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("alarm status", flag.ContinueOnError)
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

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	var resp alarmStateResponse
	if err := client.getJSON(ctx, "/api/v1/alarm/state", &resp); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, resp)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ZONE\tSTATE\tMODE\tINCIDENT")
	for _, a := range resp.Zones {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			sanitizeForTerminal(alarmZoneLabel(a)), sanitizeForTerminal(a.State),
			sanitizeForTerminal(dashIfEmpty(a.Mode)), sanitizeForTerminal(alarmIncidentLabel(a.Incident)))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("alarm status: flush table: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "total: %d\n", len(resp.Zones))
	return nil
}

// cmdAlarmArm is `alarm arm --zone <id> --mode <mode> [--force] [--skip-delay]`:
// POST /api/v1/alarm/zones/{id}/arm.
func cmdAlarmArm(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("alarm arm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	zone := fs.String("zone", "", "alarm zone id (required)")
	mode := fs.String("mode", "", "target protection mode: perimeter, full, night, vacation, custom (required)")
	force := fs.Bool("force", false, "arm despite readiness blockers, where the engine's blocker policy allows it")
	skipDelay := fs.Bool("skip-delay", false, "skip the configured exit delay and arm immediately")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *zone == "" {
		return errors.New("alarm arm: missing --zone <id>")
	}
	if *mode == "" {
		return errors.New("alarm arm: missing --mode <mode>")
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	body := alarmArmRequest{Mode: *mode, Force: *force, SkipDelay: *skipDelay}
	var resp alarmArmAccepted
	path := "/api/v1/alarm/zones/" + url.PathEscape(*zone) + "/arm"
	if err := client.sendJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		return fmt.Errorf("alarm arm: %w", err)
	}

	if f.jsonOut {
		return writeJSON(stdout, resp)
	}
	if resp.ExitDelayS > 0 {
		_, _ = fmt.Fprintf(stdout, "ok: state=%s exit_delay_s=%d\n", sanitizeForTerminal(resp.State), resp.ExitDelayS)
	} else {
		_, _ = fmt.Fprintf(stdout, "ok: state=%s\n", sanitizeForTerminal(resp.State))
	}
	return nil
}

// cmdAlarmDisarm is `alarm disarm --zone <id>`: POST
// /api/v1/alarm/zones/{id}/disarm.
func cmdAlarmDisarm(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("alarm disarm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	zone := fs.String("zone", "", "alarm zone id (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *zone == "" {
		return errors.New("alarm disarm: missing --zone <id>")
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	path := "/api/v1/alarm/zones/" + url.PathEscape(*zone) + "/disarm"
	if err := client.sendJSON(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("alarm disarm: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}

// cmdAlarmSilence is `alarm silence [--zone <id>|--all]`: POST
// /api/v1/alarm/zones/{id}/silence for one zone, or POST
// /api/v1/alarm/silence-all across every zone. Exactly one of --zone
// or --all is required — silencing "everything" must be an explicit
// choice, not the fallback of an empty --zone.
func cmdAlarmSilence(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("alarm silence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	zone := fs.String("zone", "", "alarm zone id (mutually exclusive with --all)")
	all := fs.Bool("all", false, "silence every zone (mutually exclusive with --zone)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	hasZone := *zone != ""
	if hasZone == *all {
		return errors.New("alarm silence: exactly one of --zone <id> or --all is required")
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	path := "/api/v1/alarm/silence-all"
	if *zone != "" {
		path = "/api/v1/alarm/zones/" + url.PathEscape(*zone) + "/silence"
	}
	if err := client.sendJSON(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("alarm silence: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}

// cmdAlarmAck is `alarm ack --zone <id>`: POST
// /api/v1/alarm/zones/{id}/acknowledge.
func cmdAlarmAck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("alarm ack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	zone := fs.String("zone", "", "alarm zone id (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *zone == "" {
		return errors.New("alarm ack: missing --zone <id>")
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	path := "/api/v1/alarm/zones/" + url.PathEscape(*zone) + "/acknowledge"
	if err := client.sendJSON(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("alarm ack: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}

// alarmZoneLabel formats an zone's AREA column: the id alone, or
// "id (name)" when a distinct display name is set. The id is kept
// primary because it is what --zone <id> expects on every other
// subcommand.
func alarmZoneLabel(a alarmZoneStatus) string {
	if a.Name == "" || a.Name == a.ID {
		return a.ID
	}
	return fmt.Sprintf("%s (%s)", a.ID, a.Name)
}

// alarmIncidentLabel formats the INCIDENT column: "-" when the zone has
// no open incident, the incident id otherwise, with a "(silenced)"
// suffix while the incident's sounders are silenced.
func alarmIncidentLabel(inc *alarmIncidentRef) string {
	if inc == nil {
		return "-"
	}
	if inc.Silenced {
		return inc.ID + " (silenced)"
	}
	return inc.ID
}

// dashIfEmpty returns "-" for an empty string, s otherwise. Used for
// table columns that are legitimately absent (e.g. mode while
// disarmed).
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
