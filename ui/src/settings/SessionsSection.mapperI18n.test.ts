import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';

import SessionsSection from './SessionsSection.svelte';

const api = vi.hoisted(() => ({
  fetchMapSets: vi.fn(), fetchActiveMapSet: vi.fn(), saveActiveMapSet: vi.fn(),
  fetchMapperProfiles: vi.fn(), fetchSessionMapperProfile: vi.fn(), saveSessionMapperProfile: vi.fn(),
}));
vi.mock('./api', () => api);

async function settle() {
  for (let i = 0; i < 3; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await tick();
  }
}

describe('SessionsSection mapper settings i18n', () => {
  let target: HTMLDivElement;
  let component: ReturnType<typeof mount>;

  afterEach(() => {
    if (component) unmount(component);
    target?.remove();
    vi.restoreAllMocks();
  });

  it('localizes the mapper blocks while preserving session, map, and format identities', async () => {
    api.fetchMapSets.mockResolvedValue([{ id: 4, name: 'Сырой мир', zone_count: 2, room_count: 5 }]);
    api.fetchActiveMapSet.mockResolvedValue({ active_map_set_id: 4 });
    api.fetchMapperProfiles.mockResolvedValue([{
      id: 0, name: 'diku-en', title: 'Diku English', builtin: true,
      provenance: 'Verified', go_detector: 'diku', spec: null,
    }]);
    api.fetchSessionMapperProfile.mockResolvedValue({ mapper_format: '', active_name: 'diku-en', error: '' });
    const session = { id: 9, name: 'MUD north', status: 'disconnected', mud_host: 'mud.example', mud_port: 4000 } as never;
    target = document.createElement('div');
    document.body.append(target);
    component = mount(SessionsSection, {
      target,
      props: {
        locale: 'ru', sessions: [session], currentSession: session,
        sessionEditor: {}, editingSessionDraft: {}, sessionProfiles: [], profiles: [],
        addSession: vi.fn(), sessionAction: vi.fn(), viewSession: vi.fn(), startInlineSessionEdit: vi.fn(),
        deleteSession: vi.fn(), saveInlineSessionEdit: vi.fn(), cancelInlineSessionEdit: vi.fn(),
        moveSessionProfile: vi.fn(), removeProfileFromSession: vi.fn(), addProfileToSession: vi.fn(),
      },
    });
    await settle();

    expect(target.textContent).toContain('Набор карт для MUD north');
    expect(target.textContent).toContain('Сырой мир — 2 зоны, 5 комнат');
    expect(target.textContent).toContain('Формат комнат для MUD north');
    expect(target.textContent).toContain('Diku English (встроенный)');
    expect(target.textContent).toContain('Проверен');
    expect(target.textContent).toContain('diku-en');
    expect(target.querySelector('[aria-label="Формат комнаты"]')).not.toBeNull();
    expect(target.querySelector('[aria-label="Набор карт"]')).not.toBeNull();
    expect(target.querySelector('code')?.textContent).toBe('~map');
  });

  it('gives the map-set selector an English accessible name by default', async () => {
    api.fetchMapSets.mockResolvedValue([]);
    api.fetchActiveMapSet.mockResolvedValue({ active_map_set_id: null });
    api.fetchMapperProfiles.mockResolvedValue([]);
    api.fetchSessionMapperProfile.mockResolvedValue({ mapper_format: '', active_name: '', error: '' });
    const session = { id: 10, name: 'MUD south', status: 'disconnected', mud_host: 'mud.example', mud_port: 4000 } as never;
    target = document.createElement('div');
    document.body.append(target);
    component = mount(SessionsSection, {
      target,
      props: {
        locale: 'en', sessions: [session], currentSession: session,
        sessionEditor: {}, editingSessionDraft: {}, sessionProfiles: [], profiles: [],
        addSession: vi.fn(), sessionAction: vi.fn(), viewSession: vi.fn(), startInlineSessionEdit: vi.fn(),
        deleteSession: vi.fn(), saveInlineSessionEdit: vi.fn(), cancelInlineSessionEdit: vi.fn(),
        moveSessionProfile: vi.fn(), removeProfileFromSession: vi.fn(), addProfileToSession: vi.fn(),
      },
    });
    await settle();
    expect(target.querySelector('[aria-label="Map set"]')).not.toBeNull();
  });
});
