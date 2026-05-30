import { describe, expect, it, vi, afterEach } from 'vitest';
import { saveAppSettingsRequest, toggleProfileGroup } from './api';

describe('settings api', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('saves app local command toggles', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await saveAppSettingsRequest({ api_token: 'keep', allow_exec_command: true, allow_webfetch_command: false });

    expect(fetchMock).toHaveBeenCalledWith('/api/app/settings', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-Token': '',
      },
      body: JSON.stringify({ api_token: 'keep', allow_exec_command: true, allow_webfetch_command: false }),
    });
  });

  it('toggles unified profile groups without obsolete domain field', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await toggleProfileGroup(7, 'combat', false);

    expect(fetchMock).toHaveBeenCalledWith('/api/profiles/7/groups/toggle', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-Token': '',
      },
      body: JSON.stringify({ group_name: 'combat', enabled: false }),
    });
  });
});
