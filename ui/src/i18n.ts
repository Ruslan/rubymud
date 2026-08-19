export const supportedLocales = ['en', 'ru'] as const;
export type Locale = (typeof supportedLocales)[number];
export const defaultLocale: Locale = 'en';

declare global {
  interface Window { APP_LANGUAGE?: string }
}

export function normalizeLocale(value: unknown): Locale {
  if (typeof value !== 'string') return defaultLocale;
  const base = value.trim().toLowerCase().replace('_', '-').split('-', 1)[0];
  return supportedLocales.includes(base as Locale) ? base as Locale : defaultLocale;
}

// Strict variant for live messages. Unlike normalizeLocale, an unsupported
// broadcast must not silently switch a Russian client back to English.
export function supportedLocale(value: unknown): Locale | null {
  if (typeof value !== 'string') return null;
  const base = value.trim().toLowerCase().replace('_', '-').split('-', 1)[0];
  return supportedLocales.includes(base as Locale) ? base as Locale : null;
}

export function readBootLocale(): Locale {
  return normalizeLocale(window.APP_LANGUAGE);
}

export function extractPlaceholders(template: string): string[] {
  return Array.from(template.matchAll(/\{([a-zA-Z0-9_]+)\}/g), (match) => match[1] ?? '').sort();
}

export type MessageValues = Readonly<Record<string, string | number>>;

export function interpolate(template: string, values: MessageValues = {}): string {
  return template.replace(/\{([a-zA-Z0-9_]+)\}/g, (placeholder, name: string) => (
    Object.prototype.hasOwnProperty.call(values, name) ? String(values[name]) : placeholder
  ));
}

export type PluralForm = 'one' | 'few' | 'many';

export function selectPluralForm(localeValue: unknown, count: number): PluralForm {
  const locale = normalizeLocale(localeValue);
  if (locale === 'ru') {
    const abs = Math.abs(Math.trunc(count));
    const lastTwo = abs % 100;
    if (lastTwo >= 11 && lastTwo <= 14) return 'many';
    const last = abs % 10;
    if (last === 1) return 'one';
    if (last >= 2 && last <= 4) return 'few';
    return 'many';
  }
  return count === 1 ? 'one' : 'many';
}
