import { describe, expect, it } from 'vitest';

import { createSettingsMapperI18n } from './mapperI18n';
import { readResponseError } from './responseError';

describe('readResponseError', () => {
  it.each([
    ['en', 'exits pattern must contain exactly one capture group'],
    ['ru', 'шаблон выходов должен содержать ровно одну группу захвата'],
  ] as const)('extracts the localized JSON validation error in %s', async (locale, error) => {
    const response = new Response(JSON.stringify({ ok: false, error }), {
      status: 400,
      headers: { 'Content-Type': 'application/json' },
    });
    const fallback = createSettingsMapperI18n(locale).t('replayFailed', { status: response.status });

    expect(await readResponseError(response, fallback)).toBe(error);
    expect(response.bodyUsed).toBe(true);
  });

  it('uses plain or malformed response text, then the localized fallback for an empty body', async () => {
    expect(await readResponseError(new Response('gateway unavailable', { status: 502 }), 'fallback'))
      .toBe('gateway unavailable');
    expect(await readResponseError(new Response('{"error":', { status: 400 }), 'fallback'))
      .toBe('{"error":');
    expect(await readResponseError(new Response('', { status: 503 }), 'Повтор не удался (HTTP 503).'))
      .toBe('Повтор не удался (HTTP 503).');
  });
});
