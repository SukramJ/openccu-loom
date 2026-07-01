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
	"text/tabwriter"
	"time"
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

type sysvarFlags struct {
	host     string
	token    string
	user     string
	password string
	timeout  time.Duration
	jsonOut  bool
}

func (f *sysvarFlags) bindTo(fs *flag.FlagSet) {
	fs.StringVar(&f.host, "host", "http://localhost:8119", "daemon REST base URL")
	fs.StringVar(&f.token, "token", "", "API bearer token")
	fs.StringVar(&f.user, "user", "", "basic-auth username")
	fs.StringVar(&f.password, "password", "", "basic-auth password")
	fs.DurationVar(&f.timeout, "timeout", 60*time.Second, "request timeout")
	fs.BoolVar(&f.jsonOut, "json", false, "emit raw JSON instead of a human-readable table")
}

func (f *sysvarFlags) client() *daemonClient {
	return newDaemonClient(f.host, f.token, f.user, f.password, f.timeout)
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
	var f sysvarFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	var items []sysvarSummary
	if err := f.client().getJSON(ctx, "/api/v1/sysvars", &items); err != nil {
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
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%v\t%s\n", s.Name, s.ValueType, s.Value, s.Central)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%v\n", s.Name, s.ValueType, s.Value)
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
	var f sysvarFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("sysvar get: missing <name>")
	}
	name := rest[0]

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	var sv sysvarSummary
	if err := f.client().getJSON(ctx, "/api/v1/sysvars/"+name, &sv); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, sv)
	}

	_, _ = fmt.Fprintf(stdout, "Name:     %s\n", sv.Name)
	_, _ = fmt.Fprintf(stdout, "Type:     %s\n", sv.ValueType)
	_, _ = fmt.Fprintf(stdout, "Value:    %v\n", sv.Value)
	if sv.Unit != "" {
		_, _ = fmt.Fprintf(stdout, "Unit:     %s\n", sv.Unit)
	}
	if sv.Central != "" {
		_, _ = fmt.Fprintf(stdout, "Central:  %s\n", sv.Central)
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
	var f sysvarFlags
	f.bindTo(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("sysvar set: usage: set <name> <value>")
	}
	name, rawVal := rest[0], rest[1]

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	body := sysvarSetRequest{Value: coerceValue(rawVal)}
	if err := f.client().sendJSON(ctx, http.MethodPut, "/api/v1/sysvars/"+name, body, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}

func cmdSysvarFetch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sysvar fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f sysvarFlags
	f.bindTo(fs)
	var central string
	fs.StringVar(&central, "central", "", "limit fetch to this central")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	path := "/api/v1/sysvars/fetch"
	if central != "" {
		path += "?central=" + central
	}
	if err := f.client().sendJSON(ctx, http.MethodPost, path, nil, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}
