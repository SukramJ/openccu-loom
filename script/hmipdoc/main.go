// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// hmipdoc compares the eQ-3 "Homematic IP Devices - Technical Documentation" PDF
// with the device catalogue. eQ-3 updates the PDF regularly; run this against
// each new edition:
//
//	go run ./script/hmipdoc -output tmp/hmip_device_doc_report.md tmp/hmip_device_documentation.pdf
//
// Only the current firmware range of a model is evaluated: the section whose
// range has no end ("from version X", without "to version Y").
//
// The report lists candidates, not defects. A missing parameter is often
// intended, because profiles are shared between models and absent fields are
// skipped at runtime. Judge every candidate by reading the section it cites.
//
// Exit codes: 0 report written, 1 the tool could not run, 2 the PDF could not be
// parsed completely. The last one is checked against raw counts of the extracted
// text, so a changed PDF layout fails loudly instead of producing an incomplete
// report.
//
// Requires pdftotext (poppler). The PDF is not part of the repository.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/build"
)

const (
	exitOK         = 0
	exitFailed     = 1
	exitParseError = 2
)

var (
	crrfdVersion = regexp.MustCompile(`based on crRFD version: (\S+)`)
	creationDate = regexp.MustCompile(`Documentation creation date: (\S+)`)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("hmipdoc", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write the report to this file instead of stdout")
	if err := flags.Parse(args); err != nil {
		return exitFailed
	}
	// The flag package stops at the first positional argument, so a flag after
	// the path would otherwise be dropped silently.
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: hmipdoc [-output report.md] documentation.pdf") //nolint:errcheck // a failed stderr write has no recourse
		return exitFailed
	}

	text, err := extractText(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err) //nolint:errcheck // a failed stderr write has no recourse
		return exitFailed
	}
	content, errs := buildReport(text, productionCatalog())
	if len(errs) > 0 {
		fmt.Fprintln(stderr, strings.Join(errs, "\n")) //nolint:errcheck // a failed stderr write has no recourse
		return exitParseError
	}

	if *output != "" {
		err = os.WriteFile(*output, []byte(content), 0o600)
	} else {
		_, err = io.WriteString(stdout, content)
	}
	if err != nil {
		fmt.Fprintln(stderr, err) //nolint:errcheck // a failed stderr write has no recourse
		return exitFailed
	}
	return exitOK
}

// extractText returns the text of the PDF, with pages separated by form feeds.
func extractText(pdf string) (string, error) {
	pdftotext, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("pdftotext not found, install poppler")
	}
	dir, err := os.MkdirTemp("", "hmipdoc")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a leftover temp dir is harmless

	target := filepath.Join(dir, "doc.txt")
	cmd := exec.Command(pdftotext, "-layout", pdf, target) //nolint:gosec,noctx // G204: an operator tool; the path is the operator's own argument
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdftotext: %w: %s", err, out)
	}
	raw, err := os.ReadFile(target) //nolint:gosec // G304: a file this function just created in its own temp dir
	if err != nil {
		return "", fmt.Errorf("read extracted text: %w", err)
	}
	return strings.ToValidUTF8(string(raw), "�"), nil
}

// buildReport parses the extracted text and renders the report. Any error means
// the document was not read completely, and no report is produced.
func buildReport(text string, cat catalog) (content string, errs []string) {
	sections, errs := parse(text)
	latest, rangeErrs := latestSections(sections)
	errs = append(errs, rangeErrs...)
	if len(errs) > 0 {
		return "", errs
	}
	header := []string{
		"# HmIP device documentation vs. device catalogue",
		"",
		fmt.Sprintf("- PDF: crRFD %s, created %s", firstGroup(crrfdVersion, text), firstGroup(creationDate, text)),
		"- OpenCCU-Loom: " + build.Version,
		fmt.Sprintf("- Parsed: %d sections, %d models", len(sections), len(latest)),
		"",
	}
	return strings.Join(slices.Concat(header, report(latest, sections, cat)), "\n") + "\n", nil
}

func firstGroup(pattern *regexp.Regexp, text string) string {
	if m := pattern.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return "unknown"
}
