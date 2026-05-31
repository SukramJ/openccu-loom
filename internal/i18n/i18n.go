// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package i18n

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed catalogs/*.json
var catalogFS embed.FS

// Catalog holds one locale's translations.
type Catalog struct {
	Locale   string
	Messages map[string]string
}

// Catalogs bundles every available locale. Lookups fall through
// DefaultLocale when the requested locale has no entry.
type Catalogs struct {
	DefaultLocale string

	mu       sync.RWMutex
	catalogs map[string]*Catalog
}

// NewCatalogs loads every embedded catalogue. Returns an error when
// a JSON file fails to parse.
func NewCatalogs() (*Catalogs, error) {
	out := &Catalogs{DefaultLocale: "en", catalogs: make(map[string]*Catalog)}
	entries, err := catalogFS.ReadDir("catalogs")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		buf, err := catalogFS.ReadFile("catalogs/" + e.Name())
		if err != nil {
			return nil, err
		}
		msgs := make(map[string]string)
		if err := json.Unmarshal(buf, &msgs); err != nil {
			return nil, err
		}
		locale := strings.TrimSuffix(e.Name(), ".json")
		out.catalogs[locale] = &Catalog{Locale: locale, Messages: msgs}
	}
	return out, nil
}

// T looks up key for locale. Falls back to [DefaultLocale] and
// ultimately returns the key itself when nothing matches.
func (c *Catalogs) T(locale, key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cat, ok := c.catalogs[locale]; ok {
		if msg, ok := cat.Messages[key]; ok {
			return msg
		}
	}
	if locale != c.DefaultLocale {
		if cat, ok := c.catalogs[c.DefaultLocale]; ok {
			if msg, ok := cat.Messages[key]; ok {
				return msg
			}
		}
	}
	return key
}

// Locales returns every loaded locale tag.
func (c *Catalogs) Locales() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.catalogs))
	for l := range c.catalogs {
		out = append(out, l)
	}
	return out
}

// PreloadLocale eagerly loads the catalogue for locale so that the
// first [Catalogs.T] call does not bear the I/O cost. It is safe to
// call from any goroutine and from tests; the implementation takes the
// write lock only when the locale is not yet loaded.
func (c *Catalogs) PreloadLocale(locale string) {
	if locale == "" {
		locale = c.DefaultLocale
	}
	c.mu.RLock()
	_, loaded := c.catalogs[locale]
	c.mu.RUnlock()
	if loaded {
		return
	}
	// Locale not yet in the map — try to load it from the embedded FS.
	// We ignore errors: if the locale file is missing we simply leave
	// the map unchanged and future lookups will fall back to DefaultLocale.
	name := locale + ".json"
	buf, err := catalogFS.ReadFile("catalogs/" + name)
	if err != nil {
		return
	}
	msgs := make(map[string]string)
	if err := json.Unmarshal(buf, &msgs); err != nil {
		return
	}
	c.mu.Lock()
	if _, exists := c.catalogs[locale]; !exists {
		c.catalogs[locale] = &Catalog{Locale: locale, Messages: msgs}
	}
	c.mu.Unlock()
}

// SchedulePreloadLocale launches a background goroutine that calls
// [Catalogs.PreloadLocale] for locale. It returns immediately; use
// this when you want to warm the cache without blocking startup.
func (c *Catalogs) SchedulePreloadLocale(locale string) {
	go c.PreloadLocale(locale)
}

// ResetForTesting discards all loaded catalogs and resets DefaultLocale
// to "en". Intended exclusively for test helpers — do not call from
// production code paths.
func (c *Catalogs) ResetForTesting() {
	c.mu.Lock()
	c.DefaultLocale = "en"
	c.catalogs = make(map[string]*Catalog)
	c.mu.Unlock()
}
