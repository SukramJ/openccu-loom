// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strings"
)

// embedded holds the CCU-metadata archives the daemon ships with.
// Content originates from the
// docs/adr/0003-embed-occu-extracts.md):
//
//   - translation_extract.json.gz   — raw CCU stringtable extract
//   - easymode_extract.json.gz      — TCL easymode extract
//   - profiles/<RECEIVER>.json.gz   — one profile archive per receiver
//   - translation_custom/*.json     — curated overrides (MIT)
//   - NOTICE                        — attribution + license headline
//
// Licensing is split: the Python extractors in
// the extracted *data* inherits the eQ-3 HomeMatic Software License
// (free for private and non-commercial use). See NOTICE for the
// full terms.
//
//go:embed embedded/translation_extract.json.gz
//go:embed embedded/easymode_extract.json.gz
//go:embed embedded/profiles/*.json.gz
//go:embed embedded/profiles/_receiver_type_aliases.json
//go:embed embedded/translation_custom/*.json
//go:embed embedded/NOTICE
//go:embed embedded/MANIFEST.json
var embedded embed.FS

// LoadTranslationsEmbedded decodes the translation archive shipped
// inside the binary and overlays the curated `translation_custom/`
// Files so the operator sees labels out of
// the box. Returned value is never nil; on decoding error the caller
// receives [Empty] so downstream lookups gracefully fall back to the
// raw CCU strings.
func LoadTranslationsEmbedded() (*Translations, error) {
	raw, err := readEmbeddedGzipJSON("embedded/translation_extract.json.gz")
	if err != nil {
		return Empty(), fmt.Errorf("ccudata: decode embedded translations: %w", err)
	}
	t := translationsFromRaw(raw)
	if err := overlayCustomTranslations(t); err != nil {
		// A corrupt custom file is not fatal — log-worthy, but we
		// still return the base extract the daemon already has.
		return t, fmt.Errorf("ccudata: custom translations: %w", err)
	}
	return t, nil
}

// LoadEasymodeEmbedded decodes the easymode archive shipped inside
// the binary. Returns an empty struct on decoding error so the
// caller sees a non-nil value.
func LoadEasymodeEmbedded() (*Easymode, error) {
	f, err := embedded.Open("embedded/easymode_extract.json.gz")
	if err != nil {
		return EmptyEasymode(), fmt.Errorf("ccudata: open embedded easymode: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return EmptyEasymode(), fmt.Errorf("ccudata: gunzip embedded easymode: %w", err)
	}
	defer func() { _ = gz.Close() }()
	out := EmptyEasymode()
	if err := json.NewDecoder(gz).Decode(out); err != nil {
		return EmptyEasymode(), fmt.Errorf("ccudata: decode embedded easymode: %w", err)
	}
	return out, nil
}

// readEmbeddedGzipJSON is the shared gunzip + decode path used by
// the translation loader. The output map mirrors the on-disk archive
// shape translationsFromRaw knows how to split.
func readEmbeddedGzipJSON(name string) (map[string]map[string]string, error) {
	f, err := embedded.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// embed.FS does not guarantee io.Seeker on every platform; buffer
	// the file so gzip can rewind if it needs to.
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(buf)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	raw := make(map[string]map[string]string)
	if err := json.NewDecoder(gz).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// overlayCustomTranslations merges every `translation_custom/*.json`
// file on top of the matching Translations table. Filenames map 1:1
// to the extractor's category + locale naming:
//
//	parameters_de.json        → t.Parameters["de"]
//	parameter_values_en.json  → t.ParameterValues["en"]
//	ui_labels_de.json         → t.UILabels["de"]
//	channel_types_en.json     → t.ChannelTypes["en"]
//	device_models_de.json     → t.DeviceModels["de"]
//	device_icons.json         → t.DeviceIcons (locale-independent)
//
// Custom entries win over extract entries — the whole point of the
// curated folder is to patch upstream gaps.
func overlayCustomTranslations(t *Translations) error {
	entries, err := fs.ReadDir(embedded, "embedded/translation_custom")
	if err != nil {
		// Directory absent is fine — custom is optional. Only a
		// genuinely malformed embed would surface any other error
		// and we swallow it here so the caller still gets the base
		// translations.
		return nil //nolint:nilerr // optional directory
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := embedded.ReadFile(path.Join("embedded/translation_custom", name))
		if err != nil {
			return err
		}
		table := make(map[string]string)
		if err := json.Unmarshal(data, &table); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		applyCustomTable(t, strings.TrimSuffix(name, ".json"), table)
	}
	return nil
}

// applyCustomTable dispatches a single custom file onto the matching
// Translations map, keyed by the filename stem.
func applyCustomTable(t *Translations, stem string, table map[string]string) {
	switch {
	case stem == "device_icons":
		mergeInto(t.DeviceIcons, table)
	case strings.HasPrefix(stem, "parameters_"):
		mergeLocaleInto(t.Parameters, localeSuffix(stem), table)
	case strings.HasPrefix(stem, "parameter_values_"):
		mergeLocaleInto(t.ParameterValues, localeSuffix(stem), table)
	case strings.HasPrefix(stem, "parameter_help_"):
		mergeLocaleInto(t.ParameterHelp, localeSuffix(stem), table)
	case strings.HasPrefix(stem, "channel_types_"):
		mergeLocaleInto(t.ChannelTypes, localeSuffix(stem), table)
	case strings.HasPrefix(stem, "device_models_"):
		mergeLocaleInto(t.DeviceModels, localeSuffix(stem), table)
	case strings.HasPrefix(stem, "ui_labels_"):
		mergeLocaleInto(t.UILabels, localeSuffix(stem), table)
	}
}

func mergeInto(dst, src map[string]string) {
	maps.Copy(dst, src)
}

func mergeLocaleInto(dst map[string]map[string]string, locale string, src map[string]string) {
	if locale == "" {
		return
	}
	cur := dst[locale]
	if cur == nil {
		cur = make(map[string]string, len(src))
		dst[locale] = cur
	}
	mergeInto(cur, src)
}
