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
	"sort"
	"text/tabwriter"
)

func cmdParamset(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("paramset: missing operation (try: get, set)")
	}
	switch args[0] {
	case "get":
		return cmdParamsetGet(args[1:], stdout, stderr)
	case "set":
		return cmdParamsetSet(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("paramset: unknown operation %q", args[0])
	}
}

var validParamsetKeys = map[string]bool{
	"VALUES": true,
	"MASTER": true,
	"LINK":   true,
}

func cmdParamsetGet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("paramset get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("paramset get: usage: get <addr> <KEY>")
	}
	addr, key := rest[0], rest[1]
	if !validParamsetKeys[key] {
		return fmt.Errorf("paramset get: invalid KEY %q (must be VALUES, MASTER, or LINK)", key)
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	var params map[string]any
	if err := client.getJSON(ctx, "/api/v1/devices/"+url.PathEscape(addr)+"/paramsets/"+url.PathEscape(key), &params); err != nil {
		return err
	}

	if f.jsonOut {
		return writeJSON(stdout, params)
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PARAM\tVALUE")
	for _, k := range keys {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", sanitizeForTerminal(k), sanitizeValue(params[k]))
	}
	return tw.Flush()
}

func cmdParamsetSet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("paramset set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f connFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 4 {
		return errors.New("paramset set: usage: set <addr> <KEY> <param> <value>")
	}
	addr, key, param, rawVal := rest[0], rest[1], rest[2], rest[3]
	if !validParamsetKeys[key] {
		return fmt.Errorf("paramset set: invalid KEY %q (must be VALUES, MASTER, or LINK)", key)
	}

	client, err := f.client(stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	client := f.client()
	body := map[string]any{param: coerceValue(rawVal)}
	headers := map[string]string(nil)
	// MASTER and LINK writes are configuration changes the daemon gates
	// behind the strict edit lock. Open a session, present its token on
	// the write, then release it. VALUES writes are ungated.
	if key == "MASTER" || key == "LINK" {
		lockKey := "channel:" + addr + ":" + key
		token, err := client.openEditSession(ctx, lockKey)
		if err != nil {
			return fmt.Errorf("paramset set: acquire edit lock: %w", err)
		}
		defer func() { _ = client.closeEditSession(ctx, lockKey, token) }()
		headers = map[string]string{editTokenHeader: token}
	}
	if err := client.sendJSONHeaders(ctx, http.MethodPut, "/api/v1/devices/"+url.PathEscape(addr)+"/paramsets/"+url.PathEscape(key), body, nil, headers); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return nil
}
