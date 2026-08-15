// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"text/tabwriter"
)

type programSummary struct {
	Central      string `json:"central,omitempty"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Active       *bool  `json:"active,omitempty"`
	LastExecuted string `json:"last_executed,omitempty"`
	IsInternal   bool   `json:"is_internal,omitempty"`
}

type setProgramActiveRequest struct {
	Active bool `json:"active"`
}

func activeStar(b *bool) string {
	if b == nil {
		return "-"
	}
	if *b {
		return "yes"
	}
	return "no"
}

func cmdProgram(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("program: missing operation (try: list, get, run, enable, disable)")
	}
	switch args[0] {
	case "list":
		return cmdProgramList(args[1:], stdout, stderr)
	case "get":
		return cmdProgramGet(args[1:], stdout, stderr)
	case "run":
		return cmdProgramRun(args[1:], stdout, stderr)
	case "enable":
		return cmdProgramEnable(args[1:], stdout, stderr)
	case "disable":
		return cmdProgramDisable(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("program: unknown operation %q", args[0])
	}
}

func cmdProgramList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("program list", flag.ContinueOnError)
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

	var items []programSummary
	if err := client.getJSON(ctx, "/api/v1/programs", &items); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, items)
	}

	multiCentral := false
	if len(items) > 0 {
		first := items[0].Central
		for _, p := range items[1:] {
			if p.Central != first {
				multiCentral = true
				break
			}
		}
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	if multiCentral {
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tACTIVE\tCENTRAL")
	} else {
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tACTIVE")
	}
	for _, p := range items {
		if multiCentral {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				sanitizeForTerminal(p.ID), sanitizeForTerminal(p.Name),
				activeStar(p.Active), sanitizeForTerminal(p.Central))
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				sanitizeForTerminal(p.ID), sanitizeForTerminal(p.Name), activeStar(p.Active))
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("program list: flush table: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "total: %d\n", len(items))
	return nil
}

func cmdProgramGet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("program get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("program get: missing <id>")
	}
	id := rest[0]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	var p programSummary
	if err := client.getJSON(ctx, "/api/v1/programs/"+url.PathEscape(id), &p); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, p)
	}

	_, _ = fmt.Fprintf(stdout, "ID:     %s\n", sanitizeForTerminal(p.ID))
	_, _ = fmt.Fprintf(stdout, "Name:   %s\n", sanitizeForTerminal(p.Name))
	_, _ = fmt.Fprintf(stdout, "Active: %s\n", activeStar(p.Active))
	if p.Central != "" {
		_, _ = fmt.Fprintf(stdout, "Central: %s\n", sanitizeForTerminal(p.Central))
	}
	if p.LastExecuted != "" {
		_, _ = fmt.Fprintf(stdout, "LastExecuted: %s\n", sanitizeForTerminal(p.LastExecuted))
	}
	if p.Description != "" {
		_, _ = fmt.Fprintf(stdout, "Description: %s\n", sanitizeForTerminal(p.Description))
	}
	return nil
}

func cmdProgramRun(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("program run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("program run: missing <id>")
	}
	id := rest[0]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	if err := client.sendJSON(ctx, http.MethodPost, "/api/v1/programs/"+url.PathEscape(id)+"/execute", nil, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}

func cmdProgramEnable(args []string, stdout, stderr io.Writer) error {
	return cmdProgramSetActive(args, stdout, stderr, true)
}

func cmdProgramDisable(args []string, stdout, stderr io.Writer) error {
	return cmdProgramSetActive(args, stdout, stderr, false)
}

func cmdProgramSetActive(args []string, stdout, stderr io.Writer, active bool) error {
	opName := "enable"
	if !active {
		opName = "disable"
	}
	fs := flag.NewFlagSet("program "+opName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("program %s: missing <id>", opName)
	}
	id := rest[0]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	body := setProgramActiveRequest{Active: active}
	if err := client.sendJSON(ctx, http.MethodPatch, "/api/v1/programs/"+url.PathEscape(id), body, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}
