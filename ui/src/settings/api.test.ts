import { describe, expect, it, vi, afterEach } from 'vitest';
import {
  buildLogDownloadURL,
  createMapperProfile,
  deleteMapperProfile,
  fetchMapperProfiles,
  fetchMCPSettings,
  fetchWebAuthSettings,
  fetchSessionHistory,
  saveAppSettingRequest,
  rotateMCPTokenRequest,
  saveMCPSettingsRequest,
  saveWebAuthSettingsRequest,
  saveSessionMapperProfile,
  toggleProfileGroup,
  replayMapperProfile,
  validateMapperProfile,
  importMapSet,
  deleteMapSet,
} from './api';
import type { MapperSpec } from './types';

const sampleSpec: MapperSpec = {
  name: 'x',
  exits: { guard: '[ Exits:', pattern: '^\\[ Exits:(.*)\\]$', none_token: 'none' },
  block: { stops: ['blank', 'max_lines'], max_lines: 32 },
  strip: { mode: 'greedy', glyphs: '[]' },
  header: { rule: 'first' },
};

describe('settings api', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('saves only the changed app setting so stale tabs cannot overwrite siblings', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await saveAppSettingRequest('language', 'ru');

    expect(fetchMock).toHaveBeenCalledWith('/api/app/settings', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-Token': '',
      },
      body: JSON.stringify({ language: 'ru' }),
    });

    await saveAppSettingRequest('allow_exec_command', true);
    expect(fetchMock).toHaveBeenLastCalledWith('/api/app/settings', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-Token': '',
      },
      body: JSON.stringify({ allow_exec_command: true }),
    });
  });

  it('updates and rotates MCP settings through authenticated admin endpoints', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await saveMCPSettingsRequest(true);
    expect(fetchMock).toHaveBeenLastCalledWith('/api/mcp/settings', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ enabled: true }),
    });

    await rotateMCPTokenRequest();
    expect(fetchMock).toHaveBeenLastCalledWith('/api/mcp/settings/token', {
      method: 'POST',
      headers: { 'X-Session-Token': '' },
    });
  });

  it('reads the exact MCP status shape without a credential field', async () => {
    const payload = {
      enabled: false,
      requested_enabled: true,
      effective_enabled: false,
      token_configured: false,
      externally_managed: true,
      externally_managed_reason: 'MCP is inactive.',
      enabled_externally_managed: true,
      token_externally_managed: false,
      listener_exposure: 'network' as const,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(fetchMCPSettings()).resolves.toEqual(payload);
    expect(fetchMock).toHaveBeenCalledWith('/api/mcp/settings', {
      headers: { 'X-Session-Token': '' },
    });
    expect(payload).not.toHaveProperty('token');
  });

  it('reads and updates web access settings without retaining a password', async () => {
    const status = {
      enabled: false,
      requested_enabled: false,
      effective_enabled: false,
      username: 'operator',
      credential_configured: true,
      externally_managed: false,
      current_listener: '127.0.0.1:8080',
      current_exposure: 'loopback' as const,
      configured_listener: '127.0.0.1:8080',
      configured_exposure: 'loopback' as const,
      listener_externally_managed: false,
      restart_required: false,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(status), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(fetchWebAuthSettings()).resolves.toEqual(status);
    expect(fetchMock).toHaveBeenLastCalledWith('/api/web-auth/settings', {
      headers: { 'X-Session-Token': '' },
    });

    await saveWebAuthSettingsRequest({
      enabled: true,
      username: 'operator',
      password: 'correct horse battery staple',
    });
    expect(fetchMock).toHaveBeenLastCalledWith('/api/web-auth/settings', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({
        enabled: true,
        username: 'operator',
        password: 'correct horse battery staple',
      }),
    });
    expect(status).not.toHaveProperty('password');
    expect(status).not.toHaveProperty('password_hash');
  });

  it('saves the next-start listener through the same protected endpoint', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await saveWebAuthSettingsRequest({ enabled: true, listen_addr: '0.0.0.0:8080' });
    expect(fetchMock).toHaveBeenCalledWith('/api/web-auth/settings', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ enabled: true, listen_addr: '0.0.0.0:8080' }),
    });
  });

  it('uploads archives as multipart and deletes map sets through authenticated endpoints', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const file = new File(['zip'], 'world.zip', { type: 'application/zip' });

    await importMapSet(file);
    const [uploadURL, uploadRequest] = fetchMock.mock.calls[0]!;
    expect(uploadURL).toBe('/api/map-sets/import');
    expect(uploadRequest.method).toBe('POST');
    expect(uploadRequest.headers).toEqual({ 'X-Session-Token': '' });
    expect(uploadRequest.body).toBeInstanceOf(FormData);
    expect((uploadRequest.body as FormData).get('archive')).toBe(file);

    await deleteMapSet(42);
    expect(fetchMock).toHaveBeenLastCalledWith('/api/map-sets/42', {
      method: 'DELETE',
      headers: { 'X-Session-Token': '' },
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

  it('builds a log download URL carrying the browser timezone', () => {
    vi.spyOn(Intl, 'DateTimeFormat').mockReturnValue({
      resolvedOptions: () => ({ timeZone: 'Europe/Kyiv' }),
    } as unknown as Intl.DateTimeFormat);

    const url = buildLogDownloadURL(5, '2026-07-01T00:00:00Z', '2026-07-02T00:00:00Z');

    const params = new URL(url, 'http://localhost').searchParams;
    expect(url.startsWith('/api/sessions/5/logs/download?')).toBe(true);
    expect(params.get('tz')).toBe('Europe/Kyiv');
    expect(params.get('from')).toBe('2026-07-01T00:00:00Z');
    expect(params.get('to')).toBe('2026-07-02T00:00:00Z');
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

  // --- room formats ("mapper profiles") ------------------------------------
  // These hit /api/mapper-profiles, NOT /api/profiles (alias/trigger layering).

  it('lists room formats from the mapper-profiles endpoint', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('[]', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await fetchMapperProfiles();

    expect(fetchMock).toHaveBeenCalledWith('/api/mapper-profiles', {
      headers: { 'X-Session-Token': '' },
    });
  });

  it('creates a room format with the name alongside the spec', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await createMapperProfile('tortilla', sampleSpec);

    expect(fetchMock).toHaveBeenCalledWith('/api/mapper-profiles', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ name: 'tortilla', spec: sampleSpec }),
    });
  });

  it('validates a draft room format without writing it', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"ok":true,"error":""}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await validateMapperProfile('draft', sampleSpec);

    expect(fetchMock).toHaveBeenCalledWith('/api/mapper-profiles/validate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ name: 'draft', spec: sampleSpec }),
    });
  });

  it('replays a draft room format against recent session output', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"ok":true,"samples":[]}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await replayMapperProfile('draft', sampleSpec, 7, 1200);

    expect(fetchMock).toHaveBeenCalledWith('/api/mapper-profiles/replay', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ name: 'draft', spec: sampleSpec, session_id: 7, limit: 1200 }),
    });
  });

  it('deletes a room format by id', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await deleteMapperProfile(9);

    expect(fetchMock).toHaveBeenCalledWith('/api/mapper-profiles/9', {
      method: 'DELETE',
      headers: { 'X-Session-Token': '' },
    });
  });

  // The assignment is a REFERENCE, not a row id — that is what lets a
  // session select a compiled-in detector, which has no row to point at.
  it('assigns a room format to a session by reference', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await saveSessionMapperProfile(4, 'db:7');
    expect(fetchMock).toHaveBeenCalledWith('/api/sessions/4/mapper-profile', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ format: 'db:7' }),
    });

    // A builtin — the case the old integer column could not express at all.
    await saveSessionMapperProfile(4, 'builtin:diku-en');
    expect(fetchMock).toHaveBeenLastCalledWith('/api/sessions/4/mapper-profile', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ format: 'builtin:diku-en' }),
    });

    // '' is the default, stored as NULL.
    await saveSessionMapperProfile(4, '');
    expect(fetchMock).toHaveBeenLastCalledWith('/api/sessions/4/mapper-profile', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Session-Token': '' },
      body: JSON.stringify({ format: '' }),
    });
  });
});
