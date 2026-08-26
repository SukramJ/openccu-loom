// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	openccudata "github.com/SukramJ/go-openccu-data"
)

// The CCU-metadata archives ship as the versioned data-artifact
// module (see docs/adr/0003-embed-occu-extracts.md and its
// module-consumption update):
//
//   - translation_extract.json.gz   — raw CCU stringtable extract
//   - easymode_extract.json.gz      — TCL easymode extract
//   - profiles/<RECEIVER>.json.gz   — one profile archive per receiver
//   - translation_custom/*.json     — curated overrides (MIT)
//   - device_semantics.json         — curated device classifications
//
// The module records its upstream snapshot as SnapshotVersion; go.sum
// pins the exact data stand. Licensing is split: module code is MIT,
// the extracted *data* inherits the eQ-3 HomeMatic Software License
// (free for private and non-commercial use) — see the NOTICE shipped
// with the module.

// SnapshotVersion reports the upstream data release the embedded
// artifacts were generated from (diagnostics surface).
func SnapshotVersion() string { return openccudata.SnapshotVersion }

// LoadTranslationsEmbedded decodes the translation archive shipped
// inside the binary and overlays the curated `translation_custom/`
// Files so the operator sees labels out of
// the box. Returned value is never nil; on decoding error the caller
// receives [Empty] so downstream lookups gracefully fall back to the
// raw CCU strings.
func LoadTranslationsEmbedded() (*Translations, error) {
	raw, err := readEmbeddedGzipJSON("translation_extract.json.gz")
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
	raw, err := openccudata.ReadFile("easymode_extract.json.gz")
	if err != nil {
		return EmptyEasymode(), fmt.Errorf("ccudata: open embedded easymode: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return EmptyEasymode(), fmt.Errorf("ccudata: gunzip embedded easymode: %w", err)
	}
	defer func() { _ = gz.Close() }()
	out := EmptyEasymode()
	if err := json.NewDecoder(gz).Decode(out); err != nil {
		return EmptyEasymode(), fmt.Errorf("ccudata: decode embedded easymode: %w", err)
	}
	// The archive ships subsets but no pre-computed subset_group_ids, so the
	// derivation has to run here exactly as it does for an operator-supplied
	// file. Skipping it made the default daemon — the one with no easymode
	// path configured — the only build that serves an empty subset_group_id,
	// which reads as an environment quirk rather than a missing step.
	materializeSubsetGroupIDs(out)
	return out, nil
}

// readEmbeddedGzipJSON is the shared gunzip + decode path used by
// the translation loader. The output map mirrors the on-disk archive
// shape translationsFromRaw knows how to split.
func readEmbeddedGzipJSON(name string) (map[string]map[string]string, error) {
	blob, err := openccudata.ReadFile(name)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(blob))
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
//
// Merging mutates t.ParameterValues, which the value-only reverse index
// is derived from, so the index is re-derived on every exit — including
// the error path, where some files may already have been merged. Without
// that a curated value label is unreachable through the stage-4 fallback
// in [Translations.ParameterValue], which is exactly the gap the curated
// folder exists to close.
func overlayCustomTranslations(t *Translations) error {
	defer t.rebuildValueIndices()

	names, err := openccudata.ReadDir("translation_custom")
	if err != nil {
		// Directory absent is fine — custom is optional. Only a
		// genuinely malformed embed would surface any other error
		// and we swallow it here so the caller still gets the base
		// translations.
		return nil //nolint:nilerr // optional directory
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := openccudata.ReadFile("translation_custom/" + name)
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
