import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';

import MapperProfilesSection from './MapperProfilesSection.svelte';
import { emptyMapperProfileForm } from './mapperProfileForm';

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
});
