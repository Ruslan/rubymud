import { describe, expect, it, vi, afterEach } from 'vitest';
import { fetchSessionHistory, saveAppSettingsRequest, toggleProfileGroup } from './api';

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

  it('fetches paged filtered searched session history', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"entries":[],"has_more":false}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await fetchSessionHistory(3, { kind: 'input', query: 'look north', beforeID: 42, limit: 25 });

    expect(fetchMock).toHaveBeenCalledWith('/api/sessions/3/history?limit=25&kind=input&q=look+north&before_id=42', {
      headers: {
        'X-Session-Token': '',
      },
    });
  });
});
