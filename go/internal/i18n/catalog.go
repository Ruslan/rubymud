// Package i18n provides the small, dependency-free catalog used by authored
// RubyMUD messages. Game output, map data and commands must never pass through
// this package.
package i18n

import "strings"

type Locale string

const (
	English Locale = "en"
	Russian Locale = "ru"
)

type Key string

const (
	MapperNoPosition   Key = "mapper.no_position"
	MapperNoChanges    Key = "mapper.no_changes"
	MapperRecordFailed Key = "mapper.record_failed"
)

var catalogs = map[Locale]map[Key]string{
	English: {
		MapperNoPosition:   "no position",
		MapperNoChanges:    "No changes to apply",
		MapperRecordFailed: "Failed to record room: {reason}",
	},
	Russian: {
		MapperNoPosition:   "нет позиции",
		MapperNoChanges:    "Нет изменений для применения",
		MapperRecordFailed: "Не удалось записать комнату: {reason}",
	},
}

func SupportedLocales() []Locale {
	return []Locale{English, Russian}
}

// ParseLocale validates the exact values accepted from persistent settings and
// the settings API. Browser language tags should use NormalizeLocale instead.
func ParseLocale(value string) (Locale, bool) {
	switch value {
	case string(English):
		return English, true
	case string(Russian):
		return Russian, true
	default:
		return English, false
	}
}

// NormalizeLocale accepts common browser language tags and safely falls back
// to English for absent, corrupt or unsupported values.
func NormalizeLocale(value string) Locale {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	if base, _, ok := strings.Cut(normalized, "-"); ok {
		normalized = base
	}
	if locale, ok := ParseLocale(normalized); ok {
		return locale
	}
	return English
}

func Message(locale Locale, key Key, values map[string]string) string {
	catalog, ok := catalogs[locale]
	if !ok {
		catalog = catalogs[English]
	}
	message, ok := catalog[key]
	if !ok {
		message, ok = catalogs[English][key]
	}
	if !ok {
		return string(key)
	}
	for name, value := range values {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}
