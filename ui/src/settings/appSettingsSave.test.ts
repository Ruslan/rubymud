import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AppSettings } from './api';
import { persistAppSetting } from './appSettingsSave';

const current: AppSettings = {
  api_token: 'token',
  allow_exec_command: false,
  allow_webfetch_command: true,
  language: 'en',
};

describe('persistAppSetting', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('restores the last visible settings when the request fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')));

    const result = await persistAppSetting(current, 'language', 'ru', 'Could not save language.');

    expect(result).toEqual({ settings: current, saved: false, error: 'Could not save language.' });
  });

  it('restores the last visible settings when the server rejects the update', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('rejected', { status: 500 })));

    const result = await persistAppSetting(current, 'language', 'ru', 'Could not save language.');

    expect(result).toEqual({ settings: current, saved: false, error: 'rejected' });
  });
});
