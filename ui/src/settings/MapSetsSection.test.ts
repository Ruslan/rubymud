import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import MapSetsSection from './MapSetsSection.svelte';

const api = vi.hoisted(() => ({
  fetchMapSets: vi.fn(),
  importMapSet: vi.fn(),
  deleteMapSet: vi.fn(),
}));

vi.mock('./api', () => api);

const mapSet = {
  id: 7,
  name: 'World',
  source_archive: 'world.zip',
  imported_at: null,
  zone_count: 2,
  room_count: 3,
  seam_count: 1,
  note: '',
  editable: false,
  forked_from_id: null,
};

async function settle() {
  for (let i = 0; i < 3; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await tick();
  }
}

describe('MapSetsSection', () => {
  let target: HTMLDivElement;
  let component: ReturnType<typeof mount>;

  afterEach(() => {
    if (component) unmount(component);
    target?.remove();
    vi.restoreAllMocks();
  });

  it('shows an import conflict, keeps its source labels, and blocks the button while uploading', async () => {
    api.fetchMapSets.mockResolvedValue([]);
    let resolveUpload!: (value: Response) => void;
    api.importMapSet.mockReturnValue(new Promise<Response>((resolve) => { resolveUpload = resolve; }));
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapSetsSection, { target });
    await settle();

    const input = target.querySelector<HTMLInputElement>('input[type=file]')!;
    Object.defineProperty(input, 'files', { value: [new File(['zip'], 'world.zip')], configurable: true });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await tick();
    const upload = Array.from(target.querySelectorAll('button')).find((button) => button.textContent?.includes('Import ZIP'))!;
    upload.click();
    await tick();
    expect(upload.disabled).toBe(true);
    expect(target.textContent).toContain('Importing...');

    resolveUpload(new Response(JSON.stringify({
      error: 'archive conflicts',
      conflicts: [{ kind: 'duplicate_zone', zone: 'A', x: 0, y: 0, l: 0, source: 'one.mm2', other_source: 'two.mm2' }],
      conflicts_truncated: false,
    }), { status: 400 }));
    await settle();
    expect(target.textContent).toContain('archive conflicts');
    expect(target.textContent).toContain('first: one.mm2');
    expect(target.textContent).toContain('again: two.mm2');
    expect(target.textContent).not.toContain('(0, 0, 0)');
  });

  it('deletes after confirmation, refreshes the visible list, and emits a map-set change event', async () => {
    api.fetchMapSets.mockResolvedValue([mapSet]);
    api.deleteMapSet.mockResolvedValue(new Response(JSON.stringify({ deleted: true }), { status: 200 }));
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const changed = vi.fn();
    window.addEventListener('map-sets-changed', changed);
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapSetsSection, { target });
    await settle();

    const remove = Array.from(target.querySelectorAll('button')).find((button) => button.textContent === 'Delete')!;
    remove.click();
    await settle();
    expect(api.deleteMapSet).toHaveBeenCalledWith(7);
    expect(target.textContent).toContain('No map sets imported yet.');
    expect(changed).toHaveBeenCalledTimes(1);
    window.removeEventListener('map-sets-changed', changed);
  });

  it('does not let an older list response overwrite the refresh after import', async () => {
    let resolveInitial!: (value: typeof mapSet[]) => void;
    api.fetchMapSets
      .mockReturnValueOnce(new Promise<typeof mapSet[]>((resolve) => { resolveInitial = resolve; }))
      .mockResolvedValueOnce([mapSet]);
    api.importMapSet.mockResolvedValue(new Response(JSON.stringify({
      id: 8, name: 'New World', zone_count: 1, room_count: 1, seam_count: 0, unresolved: [], conflicts: [],
    }), { status: 200 }));
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MapSetsSection, { target });

    const input = target.querySelector<HTMLInputElement>('input[type=file]')!;
    Object.defineProperty(input, 'files', { value: [new File(['zip'], 'world.zip')], configurable: true });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await tick();
    Array.from(target.querySelectorAll('button')).find((button) => button.textContent?.includes('Import ZIP'))!.click();
    await settle();
    resolveInitial([]);
    await settle();

    expect(target.textContent).toContain('World');
    expect(target.textContent).not.toContain('No map sets imported yet.');
  });
});
