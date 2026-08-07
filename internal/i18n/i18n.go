// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package i18n

import (
	"embed"
	"encoding/json"
	"slices"
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

// TF looks up key for locale and substitutes named placeholders.
//
// A placeholder is written {name} in the catalogue and replaced by
// args["name"]. An unknown placeholder is left standing rather than
// blanked: a message reading "3 detectors in {zone}" makes the missing
// argument obvious, while silently dropping it produces a sentence that
// looks complete and is wrong.
//
// Substitution is single-pass, so a value that itself contains braces
// cannot inject a further placeholder — device names come from the CCU
// and are not trusted to be free of them.
func (c *Catalogs) TF(locale, key string, args map[string]string) string {
	msg := c.T(locale, key)
	if len(args) == 0 || !strings.ContainsRune(msg, '{') {
		return msg
	}
	var b strings.Builder
	b.Grow(len(msg))
	for i := 0; i < len(msg); {
		open := strings.IndexByte(msg[i:], '{')
		if open < 0 {
			b.WriteString(msg[i:])
			break
		}
		open += i
		closeIdx := strings.IndexByte(msg[open:], '}')
		if closeIdx < 0 {
			b.WriteString(msg[i:])
			break
		}
		closeIdx += open
		b.WriteString(msg[i:open])
		name := msg[open+1 : closeIdx]
		if v, ok := args[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(msg[open : closeIdx+1])
		}
		i = closeIdx + 1
	}
	return b.String()
}

// ResolveLocale returns the locale that will actually answer lookups
// for the requested tag: the tag itself when a catalogue is loaded for
// it, [Catalogs.DefaultLocale] otherwise. Callers echo it back so a
// consumer can tell "you got German" from "you got the fallback".
func (c *Catalogs) ResolveLocale(locale string) string {
	if locale == "" {
		return c.DefaultLocale
	}
	c.PreloadLocale(locale)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.catalogs[locale]; ok {
		return locale
	}
	return c.DefaultLocale
}

// Prefixed returns every message whose key starts with one of prefixes,
// resolved for locale with the same fallback chain as [Catalogs.T].
//
// The union of the requested and the default catalogue is returned, not
// the intersection: a key present only in the fallback still answers,
// which is what keeps a partially translated locale from silently
// dropping entries a consumer depends on.
//
// Values are returned as authored, placeholders included — a name like
// `Connectivity {iface}` is a template only the caller can complete.
func (c *Catalogs) Prefixed(locale string, prefixes ...string) map[string]string {
	if len(prefixes) == 0 {
		return map[string]string{}
	}
	resolved := c.ResolveLocale(locale)
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string]string{}
	collect := func(cat *Catalog) {
		if cat == nil {
			return
		}
		for key, msg := range cat.Messages {
			for _, prefix := range prefixes {
				if strings.HasPrefix(key, prefix) {
					out[key] = msg
					break
				}
			}
		}
	}
	// Default first, requested second: the requested locale overwrites
	// the fallback rather than the other way round.
	if resolved != c.DefaultLocale {
		collect(c.catalogs[c.DefaultLocale])
	}
	collect(c.catalogs[resolved])
	return out
}

// AvailableLocales lists every locale tag the embedded catalogues ship,
// sorted. It answers the question "is this a locale the daemon can
// actually render?" without loading a [Catalogs] first, which is what
// config validation needs: an unknown locale otherwise passes validation
// and then silently renders every message in the fallback language.
//
// The result is computed once; the catalogue set is compiled in and
// cannot change at runtime.
var AvailableLocales = sync.OnceValue(func() []string {
	entries, err := catalogFS.ReadDir("catalogs")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".json"))
	}
	slices.Sort(out)
	return out
})

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
