import { describe, expect, it } from 'vitest';
import {
  catalogKeys,
  createTranslator,
  defaultLocale,
  messagePlaceholders,
  normalizeLocale,
  readBootLocale,
  supportedLocales,
} from './i18n';

describe('i18n catalog', () => {
  it('normalizes supported browser locales and defaults to English', () => {
    expect(defaultLocale).toBe('en');
    expect(normalizeLocale('ru')).toBe('ru');
    expect(normalizeLocale('ru-RU')).toBe('ru');
    expect(normalizeLocale('EN_us')).toBe('en');
    expect(normalizeLocale('de')).toBe('en');
    expect(normalizeLocale(undefined)).toBe('en');
  });

  it('keeps every locale complete and interpolates without changing game data', () => {
    expect(supportedLocales).toEqual(['en', 'ru']);
    expect(catalogKeys('ru')).toEqual(catalogKeys('en'));
    for (const key of catalogKeys('en')) {
      expect(messagePlaceholders('ru', key)).toEqual(messagePlaceholders('en', key));
    }
    expect(createTranslator('ru')('mapper.record_failed', { reason: 'Врата: north' }))
      .toBe('Не удалось записать комнату: Врата: north');
    expect(createTranslator('en')('mapper.record_failed', { reason: '{reason} is game data' }))
      .toBe('Failed to record room: {reason} is game data');
    expect(createTranslator('de')('mapper.no_position')).toBe('no position');
  });

  it('reads the server-injected locale without a runtime fetch', () => {
    window.APP_LANGUAGE = 'ru';
    expect(readBootLocale()).toBe('ru');
    window.APP_LANGUAGE = 'not-supported';
    expect(readBootLocale()).toBe('en');
  });
});
