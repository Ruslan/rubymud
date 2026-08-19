import { describe, expect, it, vi } from 'vitest';

import { applyAppLanguageMessage } from './liveLocale';

describe('live application language messages', () => {
  it('updates the boot locale and active renderer for a supported locale only', () => {
    window.APP_LANGUAGE = 'en';
    const renderer = { setMapperLocale: vi.fn(() => 'ru' as const) };
    expect(applyAppLanguageMessage({ type: 'app_language_changed', app_language: 'ru' }, renderer)).toBe(true);
    expect(window.APP_LANGUAGE).toBe('ru');
    expect(renderer.setMapperLocale).toHaveBeenCalledWith('ru');

    renderer.setMapperLocale.mockClear();
    expect(applyAppLanguageMessage({ type: 'app_language_changed', app_language: 'xx' }, renderer)).toBe(false);
    expect(window.APP_LANGUAGE).toBe('ru');
    expect(renderer.setMapperLocale).not.toHaveBeenCalled();
  });
});
