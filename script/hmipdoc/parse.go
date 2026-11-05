// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// parameterLookahead bounds the search for the table row of a parameter. A
// parameter whose row is not found within this many lines counts as unread and
// fails the self-check.
const parameterLookahead = 13

var (
	deviceHeader    = regexp.MustCompile(`^\s*(\d+)\. Device (\S+) \((.*)$`)
	deviceHeaderRaw = regexp.MustCompile(`(?m)^\f?\s*\d+\. Device `)
	supportedType   = regexp.MustCompile(`^\s*•\s*(\S+) \(ID: (\d+), from version ([\d.]+)(?: to version ([\d.]+))?\)`)
	supportedRaw    = regexp.MustCompile(`•\s*\S+ \(ID: \d+`)
	channelRow      = regexp.MustCompile(`^\s*(\d+)\s{3,}(\S+)\s*$`)
	parameterHead   = regexp.MustCompile(`^\s*[\d.]+\. Parameter (\S+)`)
	parameterRow    = regexp.MustCompile(`^\s*(BOOLEAN|INTEGER|LONG|FLOAT|ENUM|STRING|ACTION)\s+\S+\s+(read|write|event)\b`)
	nextHeading     = regexp.MustCompile(`^\s*[\d.]+\. `)
)

// sectionKinds names the numbered subsections of a channel. Only "state" and
// "config" carry parameters the check reads.
var sectionKinds = map[string]string{"1": "state", "2": "config", "3": "roles", "4": "link"}

// supportedModel is one entry of a section's "Supported device types" list.
// An empty until marks an open-ended firmware range.
type supportedModel struct {
	name  string
	until string
}

// section is one device section of the documentation. The PDF gives every
// firmware range of a model its own section.
type section struct {
	number          int
	name            string
	description     string
	page            int
	models          []supportedModel
	channels        map[int]string
	stateParameters map[int]map[string]struct{}
	configParams    map[int]map[string]struct{}
	channelSections map[int]struct{}
}

func newSection(number int, name, description string, page int) *section {
	return &section{
		number:          number,
		name:            name,
		description:     description,
		page:            page,
		channels:        map[int]string{},
		stateParameters: map[int]map[string]struct{}{},
		configParams:    map[int]map[string]struct{}{},
		channelSections: map[int]struct{}{},
	}
}

// reference returns the citation of this section.
func (s *section) reference() string {
	return fmt.Sprintf("§%d %s, p. %d", s.number, s.name, s.page)
}

// hasParameter reports whether the parameter is documented on the channel, as
// state or as configuration parameter.
func (s *section) hasParameter(channel int, parameter string) bool {
	if _, ok := s.stateParameters[channel][parameter]; ok {
		return true
	}
	_, ok := s.configParams[channel][parameter]
	return ok
}

// sectionPatterns holds the headings that carry the section number.
type sectionPatterns struct {
	types, table, channels, channelHead, kindHead *regexp.Regexp
}

func newSectionPatterns(number int) sectionPatterns {
	n := strconv.Itoa(number)
	return sectionPatterns{
		types:       regexp.MustCompile(`^\s*` + n + `\.1\. Supported device types`),
		table:       regexp.MustCompile(`^\s*` + n + `\.2\. Channel types`),
		channels:    regexp.MustCompile(`^\s*` + n + `\.3\. Channels`),
		channelHead: regexp.MustCompile(`^\s*` + n + `\.3\.\d+\. Channel (\d+) - (\S+)`),
		kindHead:    regexp.MustCompile(`^\s*` + n + `\.3\.\d+\.(\d)\. `),
	}
}

// parser walks the extracted lines of the document.
type parser struct {
	lines    []string
	sections []*section
	current  *section
	patterns sectionPatterns
	block    string
	channel  int
	kind     string
	unread   int
}

// parse reads the extracted text and returns the sections plus every reason the
// result cannot be trusted. The counts are checked against the raw text, so a
// changed PDF layout surfaces as an error instead of an incomplete report.
func parse(text string) (sections []*section, errs []string) {
	p := &parser{channel: -1}
	var pages []int
	for no, page := range strings.Split(text, "\f") {
		for line := range strings.SplitSeq(page, "\n") {
			p.lines = append(p.lines, strings.TrimRight(line, "\r"))
			pages = append(pages, no+1)
		}
	}
	for index, line := range p.lines {
		p.readLine(index, line, pages[index])
	}
	return p.sections, p.selfCheck(text)
}

func (p *parser) readLine(index int, line string, page int) {
	if m := deviceHeader.FindStringSubmatch(line); m != nil {
		number, _ := strconv.Atoi(m[1]) //nolint:errcheck // the pattern admits digits only
		p.current = newSection(number, m[2], strings.TrimRight(m[3], " )"), page)
		p.patterns = newSectionPatterns(number)
		p.sections = append(p.sections, p.current)
		p.block, p.channel, p.kind = "", -1, ""
		return
	}
	if p.current == nil {
		return
	}
	switch {
	case p.patterns.types.MatchString(line):
		p.block = "types"
	case p.patterns.table.MatchString(line):
		p.block = "table"
	case p.patterns.channels.MatchString(line):
		p.block = "channels"
	case p.block == "types":
		p.readSupportedType(line)
	case p.block == "table":
		p.readChannelRow(line)
	case p.block == "channels":
		p.readChannelLine(index, line)
	}
}

func (p *parser) readSupportedType(line string) {
	m := supportedType.FindStringSubmatch(line)
	if m == nil {
		return
	}
	p.current.models = append(p.current.models, supportedModel{name: m[1], until: m[4]})
}

func (p *parser) readChannelRow(line string) {
	if m := channelRow.FindStringSubmatch(line); m != nil {
		channel, _ := strconv.Atoi(m[1]) //nolint:errcheck // the pattern admits digits only
		p.current.channels[channel] = m[2]
	}
}

func (p *parser) readChannelLine(index int, line string) {
	if m := p.patterns.channelHead.FindStringSubmatch(line); m != nil {
		p.channel, _ = strconv.Atoi(m[1]) //nolint:errcheck // the pattern admits digits only
		p.kind = ""
		p.current.channelSections[p.channel] = struct{}{}
		return
	}
	if m := p.patterns.kindHead.FindStringSubmatch(line); m != nil {
		p.kind = sectionKinds[m[1]]
		return
	}
	if (p.kind != "state" && p.kind != "config") || p.channel < 0 {
		return
	}
	m := parameterHead.FindStringSubmatch(line)
	if m == nil {
		return
	}
	if !p.parameterRowFollows(index) {
		p.unread++
	}
	target := p.current.stateParameters
	if p.kind == "config" {
		target = p.current.configParams
	}
	if target[p.channel] == nil {
		target[p.channel] = map[string]struct{}{}
	}
	target[p.channel][m[1]] = struct{}{}
}

// parameterRowFollows reports whether the table row of the parameter whose
// heading sits at start is readable before the next heading.
func (p *parser) parameterRowFollows(start int) bool {
	end := min(start+1+parameterLookahead, len(p.lines))
	for _, line := range p.lines[start+1 : end] {
		if nextHeading.MatchString(line) {
			return false
		}
		if parameterRow.MatchString(line) {
			return true
		}
	}
	return false
}

func (p *parser) selfCheck(text string) []string {
	var errs []string
	if raw := len(deviceHeaderRaw.FindAllString(text, -1)); raw != len(p.sections) {
		errs = append(errs, fmt.Sprintf("device headers: %d in text, %d parsed", raw, len(p.sections)))
	}
	parsedModels := 0
	for _, s := range p.sections {
		parsedModels += len(s.models)
	}
	if raw := len(supportedRaw.FindAllString(text, -1)); raw != parsedModels {
		errs = append(errs, fmt.Sprintf("supported types: %d in text, %d parsed", raw, parsedModels))
	}
	for _, s := range p.sections {
		if len(s.models) == 0 || len(s.channels) == 0 {
			errs = append(errs, s.reference()+": no models or no channel table")
		}
		if !slices.Equal(slices.Sorted(maps.Keys(s.channels)), slices.Sorted(maps.Keys(s.channelSections))) {
			errs = append(errs, s.reference()+": channel table differs from channel sections")
		}
	}
	if p.unread > 0 {
		errs = append(errs, fmt.Sprintf("%d parameters without a readable table row", p.unread))
	}
	return errs
}

// latestSections returns the section of the open-ended firmware range per model.
// A model without one, or with several, is an error: the current section is then
// not determined by the document, and picking one would be a guess.
func latestSections(sections []*section) (latest map[string]*section, errs []string) {
	latest = map[string]*section{}
	all := map[string]struct{}{}
	for _, s := range sections {
		for _, m := range s.models {
			all[m.name] = struct{}{}
			if m.until != "" {
				continue
			}
			// A section may list one model twice under different IDs (HM-CCU2 in
			// crRFD 1.60.1); only a second section makes the choice ambiguous.
			first, seen := latest[m.name]
			if seen && first != s {
				errs = append(errs, fmt.Sprintf("%s: open-ended firmware range in %s and %s", m.name, first.reference(), s.reference()))
				continue
			}
			latest[m.name] = s
		}
	}
	for _, model := range slices.Sorted(maps.Keys(all)) {
		if _, ok := latest[model]; !ok {
			errs = append(errs, model+": no open-ended firmware range")
		}
	}
	return latest, errs
}
