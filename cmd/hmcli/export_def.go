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
	"os"
	"strings"
	"time"
)

// cmdExportDef downloads a device-definition zip from a running daemon's REST
// API and writes it to a file or stdout. The archive is byte-compatible with
// the Python reference's export_device_definition, so it can be dropped into
// godevccu as a device fixture.
//
//	hmcli export-def -host http://localhost:8119 -address 00021BE9957782 \
//	    -token <api-token> -out HM-Dev.zip
func cmdExportDef(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export-def", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", defaultHost, "daemon REST base URL")
	address := fs.String("address", "", "device address to export (required)")
	out := fs.String("out", "", `output file (default: "<model>.zip"; "-" writes to stdout)`)
	token := fs.String("token", "", "API token (sent as Authorization: Bearer; or set "+envToken+")")
	user := fs.String("user", "", "basic-auth username (alternative to -token)")
	password := fs.String("password", "", "basic-auth password (or set "+envPassword+")")
	cacert := fs.String("cacert", "", "path to a PEM CA bundle to trust for TLS")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification (dangerous; off by default)")
	timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *address == "" {
		return errors.New("export-def: -address is required")
	}

	// Fill credentials from the environment / a prompt so a token never has to
	// appear on the command line, then warn if they would cross a plaintext link.
	authToken, authUser, authPassword := resolveCredentials(*token, *user, *password, os.Stdin, stderr)
	warnIfPlaintextCredentials(*host, authToken, authUser, stderr)

	tlsCfg, err := buildTLSConfig(*cacert, *insecure)
	if err != nil {
		return fmt.Errorf("export-def: %w", err)
	}

	endpoint := strings.TrimRight(*host, "/") + "/api/v1/devices/" + url.PathEscape(*address) + "/export-definition"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("export-def: build request: %w", err)
	}
	switch {
	case authToken != "":
		req.Header.Set("Authorization", "Bearer "+authToken)
	case authUser != "":
		req.SetBasicAuth(authUser, authPassword)
	}

	client := &http.Client{Timeout: *timeout}
	if tlsCfg != nil {
		client.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("export-def: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("export-def: %s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}

	// Resolve the output target: explicit -out, the filename advertised by the
	// server's Content-Disposition, or "<address>.zip" as a last resort.
	target := *out
	if target == "" {
		target = filenameFromDisposition(resp.Header.Get("Content-Disposition"))
		if target == "" {
			target = *address + ".zip"
		}
	}

	w := stdout
	if target != "-" {
		f, err := os.Create(target)
		if err != nil {
			return fmt.Errorf("export-def: create %s: %w", target, err)
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("export-def: write output: %w", err)
	}
	if target != "-" {
		_, _ = fmt.Fprintf(stderr, "wrote %s\n", target)
	}
	return nil
}

// filenameFromDisposition extracts the filename from a
// `Content-Disposition: attachment; filename="X.zip"` header, or "".
func filenameFromDisposition(header string) string {
	const marker = `filename="`
	i := strings.Index(header, marker)
	if i < 0 {
		return ""
	}
	rest := header[i+len(marker):]
	if j := strings.IndexByte(rest, '"'); j >= 0 {
		return rest[:j]
	}
	return ""
}
