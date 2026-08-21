import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';

import MapperProfilesSection from './MapperProfilesSection.svelte';
import { emptyMapperProfileForm } from './mapperProfileForm';
import { readResponseError } from './responseError';

describe('MapperProfilesSection', () => {
  let target: HTMLDivElement;
  let component: ReturnType<typeof mount>;

  afterEach(() => {
    if (component) unmount(component);
    target?.remove();
  });

  it('stops adding alternatives at the backend cap and explains why', async () => {
    const form = emptyMapperProfileForm();
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapperProfilesSection, {
      target,
      props: {
        mapperForm: form,
        saveMapperProfile: vi.fn(),
        validateMapperProfileDraft: vi.fn(),
        startMapperProfileEdit: vi.fn(),
        copyMapperProfile: vi.fn(),
        cancelMapperProfileEdit: vi.fn(),
        deleteMapperProfileRow: vi.fn(),
        replayMapperProfileDraft: vi.fn(),
        replayMapperProfileChanged: vi.fn(),
      },
    });

    const add = Array.from(target.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Alternative exits wording'),
    )!;
    for (let i = 0; i < 8; i += 1) {
      add.click();
      await tick();
    }

    expect(form.exits_alt).toHaveLength(8);
    expect(add.disabled).toBe(true);
    expect(target.textContent).toContain('Maximum 8 alternative exits wordings reached.');
  });

  it('invalidates replay state as soon as a draft field changes', async () => {
    const form = emptyMapperProfileForm();
    const replayMapperProfileChanged = vi.fn();
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapperProfilesSection, {
      target,
      props: {
        mapperForm: form,
        saveMapperProfile: vi.fn(),
        validateMapperProfileDraft: vi.fn(),
        startMapperProfileEdit: vi.fn(),
        copyMapperProfile: vi.fn(),
        cancelMapperProfileEdit: vi.fn(),
        deleteMapperProfileRow: vi.fn(),
        replayMapperProfileDraft: vi.fn(),
        replayMapperProfileChanged,
      },
    });
    replayMapperProfileChanged.mockClear();

    const guard = target.querySelector<HTMLInputElement>('[aria-label="Exits guard"]')!;
    guard.value = 'Changed:';
    guard.dispatchEvent(new Event('input', { bubbles: true }));
    await tick();

    expect(replayMapperProfileChanged).toHaveBeenCalledTimes(1);
  });

  it('renders Russian labels and aria copy while leaving patterns and commands untouched', async () => {
    const form = emptyMapperProfileForm();
    form.pattern = '^\\[ Exits:\\s*(.*?)\\s*\\]$';
    form.door_open = 'open door {dir_en}';
    form.scan_command = 'scan';
    form.scan_prefix_u = 'Наверху:';
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapperProfilesSection, {
      target,
      props: {
        locale: 'ru', mapperForm: form,
        saveMapperProfile: vi.fn(), validateMapperProfileDraft: vi.fn(),
        startMapperProfileEdit: vi.fn(), copyMapperProfile: vi.fn(), cancelMapperProfileEdit: vi.fn(),
        deleteMapperProfileRow: vi.fn(), replayMapperProfileDraft: vi.fn(), replayMapperProfileChanged: vi.fn(),
      },
    });
    await tick();

    expect(target.textContent).toContain('Форматы комнат');
    expect(target.querySelector('em')?.textContent).toBe('внесённый в проект Go-детектор');
    expect(target.querySelector('[aria-label="Шаблон выходов"]')).not.toBeNull();
    expect(target.querySelector<HTMLInputElement>('[name="not-present"]')).toBeNull();
    expect(target.querySelector<HTMLInputElement>('[aria-label="Шаблон выходов"]')?.value).toBe(form.pattern);
    expect(target.querySelector<HTMLInputElement>('[aria-label="Команда открытия двери"]')?.value).toBe('open door {dir_en}');
    expect(target.querySelector<HTMLInputElement>('[aria-label="Команда обзора окрестностей"]')?.value).toBe('scan');
    expect(target.querySelector<HTMLInputElement>('[aria-label="Префикс обзора U"]')?.value).toBe('Наверху:');
  });

  it.each([
    ['en', 'exits pattern must contain exactly one capture group'],
    ['ru', 'шаблон выходов должен содержать ровно одну группу захвата'],
  ] as const)('shows the replay validation envelope error in %s', async (locale, error) => {
    const mapperReplayError = await readResponseError(
      new Response(JSON.stringify({ ok: false, error }), { status: 400 }),
      'fallback',
    );
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapperProfilesSection, {
      target,
      props: {
        locale, mapperForm: emptyMapperProfileForm(), mapperReplayError,
        saveMapperProfile: vi.fn(), validateMapperProfileDraft: vi.fn(),
        startMapperProfileEdit: vi.fn(), copyMapperProfile: vi.fn(), cancelMapperProfileEdit: vi.fn(),
        deleteMapperProfileRow: vi.fn(), replayMapperProfileDraft: vi.fn(), replayMapperProfileChanged: vi.fn(),
      },
    });
    await tick();

    expect(target.querySelector('.replay-status')?.textContent).toBe(error);
  });
});
