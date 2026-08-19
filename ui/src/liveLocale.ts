import { supportedLocale } from './i18n';
import type { Locale } from './i18n';
import type { ServerMessage } from './types';

export interface LiveLocaleRenderer {
  setMapperLocale(value: unknown): Locale | null;
}

export function applyAppLanguageMessage(message: ServerMessage, renderer: LiveLocaleRenderer): boolean {
  if (message.type !== 'app_language_changed') return false;
  const locale = supportedLocale(message.app_language);
  if (!locale) return false;
  if (!renderer.setMapperLocale(locale)) return false;
  window.APP_LANGUAGE = locale;
  return true;
}
