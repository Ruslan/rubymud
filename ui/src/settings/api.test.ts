import { describe, expect, it, vi, afterEach } from 'vitest';
import { toggleProfileGroup } from './api';

describe('settings api', () => {
  afterEach(() => {
    vi.restoreAllMocks();
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
