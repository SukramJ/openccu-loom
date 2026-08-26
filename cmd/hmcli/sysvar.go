// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

type sysvarSummary struct {
	Central     string   `json:"central,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	ValueType   string   `json:"value_type"`
	Value       any      `json:"value"`
	Observed    bool     `json:"observed"`
	ValueList   []string `json:"value_list,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
}

type sysvarSetRequest struct {
	Value any `json:"value"`
}

func cmdSysvar(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("sysvar: missing operation (try: list, get, set, fetch)")
	}
	switch args[0] {
	case "list":
		return cmdSysvarList(args[1:], stdout, stderr)
	case "get":
		return cmdSysvarGet(args[1:], stdout, stderr)
	case "set":
		return cmdSysvarSet(args[1:], stdout, stderr)
	case "fetch":
		return cmdSysvarFetch(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("sysvar: unknown operation %q", args[0])
	}
}

func cmdSysvarList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sysvar list", flag.ContinueOnError)
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

	var items []sysvarSummary
	if err := client.getJSON(ctx, "/api/v1/sysvars", &items); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, items)
	}

	multiCentral := false
	if len(items) > 0 {
		first := items[0].Central
		for i := 1; i < len(items); i++ {
			if items[i].Central != first {
				multiCentral = true
				break
			}
		}
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	if multiCentral {
		_, _ = fmt.Fprintln(tw, "NAME\tTYPE\tVALUE\tCENTRAL")
	} else {
		_, _ = fmt.Fprintln(tw, "NAME\tTYPE\tVALUE")
	}
	for i := range items {
		s := &items[i]
		if multiCentral {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				sanitizeForTerminal(s.Name), sanitizeForTerminal(s.ValueType),
				sanitizeValue(s.Value), sanitizeForTerminal(s.Central))
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				sanitizeForTerminal(s.Name), sanitizeForTerminal(s.ValueType), sanitizeValue(s.Value))
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("sysvar list: flush table: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "total: %d\n", len(items))
	return nil
}

func cmdSysvarGet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sysvar get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	var central string
	fs.StringVar(&central, "central", "", "disambiguate a name that exists on more than one CCU")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("sysvar get: missing <name>")
	}
	name := rest[0]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	path := "/api/v1/sysvars/" + url.PathEscape(name)
	if central != "" {
		path += "?" + url.Values{"central": {central}}.Encode()
	}
	var sv sysvarSummary
	if err := client.getJSON(ctx, path, &sv); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, sv)
	}

	_, _ = fmt.Fprintf(stdout, "Name:     %s\n", sanitizeForTerminal(sv.Name))
	_, _ = fmt.Fprintf(stdout, "Type:     %s\n", sanitizeForTerminal(sv.ValueType))
	_, _ = fmt.Fprintf(stdout, "Value:    %s\n", sanitizeValue(sv.Value))
	if sv.Unit != "" {
		_, _ = fmt.Fprintf(stdout, "Unit:     %s\n", sanitizeForTerminal(sv.Unit))
	}
	if sv.Central != "" {
		_, _ = fmt.Fprintf(stdout, "Central:  %s\n", sanitizeForTerminal(sv.Central))
	}
	obs := "no"
	if sv.Observed {
		obs = "yes"
	}
	_, _ = fmt.Fprintf(stdout, "Observed: %s\n", obs)
	return nil
}

func cmdSysvarSet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sysvar set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	var central string
	fs.StringVar(&central, "central", "", "required on a multi-CCU daemon unless the name is unique across every CCU")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("sysvar set: usage: set <name> <value>")
	}
	name, rawVal := rest[0], rest[1]

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	path := "/api/v1/sysvars/" + url.PathEscape(name)
	if central != "" {
		path += "?" + url.Values{"central": {central}}.Encode()
	}
	body := sysvarSetRequest{Value: coerceValue(rawVal)}
	if err := client.sendJSON(ctx, http.MethodPut, path, body, nil); err != nil {
		return err
	}
	return writeOKResult(stdout, f.jsonOut, map[string]any{"name": name, "value": body.Value})
}

func cmdSysvarFetch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sysvar fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	var central string
	fs.StringVar(&central, "central", "", "limit fetch to this central")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := f.requestContext()
	defer cancel()

	path := "/api/v1/sysvars/fetch"
	if central != "" {
		path += "?" + url.Values{"central": {central}}.Encode()
	}
	if err := client.sendJSON(ctx, http.MethodPost, path, nil, nil); err != nil {
		return err
	}
	return writeOKResult(stdout, f.jsonOut, map[string]any{"central": central})
}
