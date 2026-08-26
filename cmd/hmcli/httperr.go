// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"
	"net/http"
	"strings"
)

// httpStatusError reports a non-2xx HTTP response from the daemon. Every REST
// call site in this CLI (daemonClient, cache clear, export-def) wraps its
// failure in this type instead of a bare fmt.Errorf, so main can map the
// status to a distinct process exit code (see exitCodeFor) without
// re-parsing the error text.
type httpStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Status     string
	Body       string
}

// newHTTPStatusError builds an httpStatusError from a completed response and
// the (already size-bounded) body the caller read from it.
func newHTTPStatusError(method, url string, resp *http.Response, body []byte) *httpStatusError {
	return &httpStatusError{
		Method:     method,
		URL:        url,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       strings.TrimSpace(string(body)),
	}
}

// Error keeps the historical "<method> <url>: HTTP <status>: <body>" shape so
// existing substring-matching callers (and their tests) keep working.
func (e *httpStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s %s: HTTP %s", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %s: %s", e.Method, e.URL, e.Status, e.Body)
}
