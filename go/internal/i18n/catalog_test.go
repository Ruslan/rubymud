package i18n

import (
	"reflect"
	"regexp"
	"sort"
	"testing"
)

func TestNormalizeLocaleDefaultsToEnglish(t *testing.T) {
	tests := []struct {
		input string
		want  Locale
	}{
		{"en", English},
		{"EN", English},
		{"en-US", English},
		{" ru ", Russian},
		{"ru-RU", Russian},
		{"", English},
		{"de", English},
	}
	for _, test := range tests {
		if got := NormalizeLocale(test.input); got != test.want {
			t.Errorf("NormalizeLocale(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestCatalogsAreCompleteAndInterpolate(t *testing.T) {
	wantKeys := sortedCatalogKeys(catalogs[English])
	for _, locale := range SupportedLocales() {
		if got := sortedCatalogKeys(catalogs[locale]); !reflect.DeepEqual(got, wantKeys) {
			t.Fatalf("catalog %q keys = %v, want %v", locale, got, wantKeys)
		}
		for _, key := range wantKeys {
			if got, want := messagePlaceholders(catalogs[locale][key]), messagePlaceholders(catalogs[English][key]); !reflect.DeepEqual(got, want) {
				t.Fatalf("catalog %q placeholders for %q = %v, want %v", locale, key, got, want)
			}
		}
	}

	if got := Message(Russian, MapperRecordFailed, map[string]string{"reason": "нет позиции"}); got != "Не удалось записать комнату: нет позиции" {
		t.Fatalf("interpolated Russian message = %q", got)
	}
	if got := Message(Locale("de"), MapperNoPosition, nil); got != "no position" {
		t.Fatalf("unsupported locale fallback = %q, want English", got)
	}
	if got := Message(Russian, Key("missing.key"), nil); got != "missing.key" {
		t.Fatalf("unknown key fallback = %q", got)
	}
}

var placeholderPattern = regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`)

func messagePlaceholders(message string) []string {
	values := placeholderPattern.FindAllString(message, -1)
	sort.Strings(values)
	return values
}

func TestParseLocaleOnlyAcceptsSupportedStoredValues(t *testing.T) {
	for _, input := range []string{"en", "ru"} {
		if got, ok := ParseLocale(input); !ok || string(got) != input {
			t.Errorf("ParseLocale(%q) = %q, %v", input, got, ok)
		}
	}
	for _, input := range []string{"", "EN", "en-US", "de"} {
		if _, ok := ParseLocale(input); ok {
			t.Errorf("ParseLocale(%q) unexpectedly accepted", input)
		}
	}
}

func sortedCatalogKeys(catalog map[Key]string) []Key {
	keys := make([]Key, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
