import { describe, expect, it } from 'vitest';
import {
  defaultLocale,
  interpolate,
  normalizeLocale,
  readBootLocale,
  supportedLocale,
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
    expect(supportedLocale('ru-RU')).toBe('ru');
    expect(supportedLocale('de')).toBeNull();
  });

  it('interpolates without changing game data', () => {
    expect(supportedLocales).toEqual(['en', 'ru']);
    expect(interpolate('Ошибка: {reason}', { reason: 'Врата: north' })).toBe('Ошибка: Врата: north');
    expect(interpolate('Failed: {reason}', { reason: '{reason} is game data' })).toBe('Failed: {reason} is game data');
  });

  it('reads the server-injected locale without a runtime fetch', () => {
    window.APP_LANGUAGE = 'ru';
    expect(readBootLocale()).toBe('ru');
    window.APP_LANGUAGE = 'not-supported';
    expect(readBootLocale()).toBe('en');
  });
});
