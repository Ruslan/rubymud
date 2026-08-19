export const supportedLocales = ['en', 'ru'] as const;
export type Locale = (typeof supportedLocales)[number];
export const defaultLocale: Locale = 'en';

const englishCatalog = {
  'mapper.no_position': 'no position',
  'mapper.no_changes': 'No changes to apply',
  'mapper.record_failed': 'Failed to record room: {reason}',
} as const;

export type MessageKey = keyof typeof englishCatalog;
type Catalog = Readonly<Record<MessageKey, string>>;

const russianCatalog = {
  'mapper.no_position': 'нет позиции',
  'mapper.no_changes': 'Нет изменений для применения',
  'mapper.record_failed': 'Не удалось записать комнату: {reason}',
} as const satisfies Catalog;

const catalogs: Readonly<Record<Locale, Catalog>> = {
  en: englishCatalog,
  ru: russianCatalog,
};

declare global {
  interface Window {
    APP_LANGUAGE?: string;
  }
}

export function normalizeLocale(value: unknown): Locale {
  if (typeof value !== 'string') return defaultLocale;
  const base = value.trim().toLowerCase().replace('_', '-').split('-', 1)[0];
  return supportedLocales.includes(base as Locale) ? base as Locale : defaultLocale;
}

export function readBootLocale(): Locale {
  return normalizeLocale(window.APP_LANGUAGE);
}

export function catalogKeys(locale: Locale): MessageKey[] {
  return (Object.keys(catalogs[locale]) as MessageKey[]).sort();
}

export function messagePlaceholders(locale: Locale, key: MessageKey): string[] {
  return Array.from(catalogs[locale][key].matchAll(/\{([a-zA-Z0-9_]+)\}/g), (match) => match[1] ?? '').sort();
}

export type MessageValues = Readonly<Record<string, string | number>>;
export type Translator = (key: MessageKey, values?: MessageValues) => string;

export function createTranslator(localeValue: unknown): Translator {
  const locale = normalizeLocale(localeValue);
  return (key, values = {}) => {
    const template = catalogs[locale][key] ?? englishCatalog[key] ?? key;
    return (template as string).replace(/\{([a-zA-Z0-9_]+)\}/g, (placeholder, name: string) => (
      Object.prototype.hasOwnProperty.call(values, name) ? String(values[name]) : placeholder
    ));
  };
}

export const t = createTranslator(readBootLocale());
