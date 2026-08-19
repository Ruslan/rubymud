import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const fetchMock = vi.fn();
vi.mock('../dom', () => ({ fetchWithToken: (url: string) => fetchMock(url) }));

import { MapPaneController } from './pane';

function json(body: unknown): Response {
  return { ok: true, json: async () => body, text: async () => JSON.stringify(body) } as unknown as Response;
}

function stubCanvas(): void {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => new Proxy(
    { fillStyle: '', strokeStyle: '', lineWidth: 0, font: '', textAlign: '', textBaseline: '' },
    { get: (target, property) => property in target ? (target as never)[property] : () => {} },
  ) as unknown as CanvasRenderingContext2D);
}

describe.each([
  ['en', 'Find room', 'Zone manager', 'Select rooms', 'Display X', '2 unconfirmed moves', 'conflict', 'paused'],
  ['ru', 'Найти комнату', 'Управление зонами', 'Выбрать комнаты', 'Экранная координата X', '2 неподтверждённых шага', 'конфликт', 'приостановлен'],
] as const)('compact mapper popovers in %s', (locale, findRoom, zoneManager, selectRooms, displayX, unconfirmed, conflictReason, pausedReason) => {
  let pane: MapPaneController;

  beforeEach(() => {
    stubCanvas();
    fetchMock.mockImplementation((url: string) => {
      if (url.includes('map-live-room')) return Promise.resolve(json({ seen: false }));
      if (url.includes('/zones')) return Promise.resolve(json([{ zone: 'Игровая зона', room_count: 0 }]));
      if (url.includes('/rooms')) return Promise.resolve(json([]));
      throw new Error(`unexpected ${url}`);
    });
    pane = new MapPaneController({ sessionID: 7, getActiveMapSetID: () => 1, locale });
    document.body.appendChild(pane.root);
    vi.spyOn(pane.root, 'getBoundingClientRect').mockReturnValue({
      x: 0, y: 0, left: 0, top: 0, right: 188, bottom: 240,
      width: 188, height: 240, toJSON: () => ({}),
    });
  });

  afterEach(() => {
    pane.dispose();
    pane.root.remove();
    vi.restoreAllMocks();
  });

  it('keeps search and manager copy readable within a 188px pane', async () => {
    await vi.waitFor(() => expect(pane.root.querySelector('option')).not.toBeNull());
    pane.root.querySelector<HTMLButtonElement>(`[aria-label="${findRoom}"]`)!.click();
    const search = pane.root.querySelector<HTMLElement>('.map-pane__search-popover')!;
    expect(search.getAttribute('aria-label')).toBe(findRoom);
    expect(parseFloat(search.style.width)).toBeLessThanOrEqual(176);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

    pane.root.querySelector<HTMLButtonElement>(`[aria-label="${zoneManager}"]`)!.click();
    const manager = pane.root.querySelector<HTMLElement>('.map-pane__manager-popover')!;
    expect(manager.textContent).toContain(selectRooms);
    expect(parseFloat(manager.style.width)).toBeLessThanOrEqual(176);
  });

  it('can update static copy live, clears stale status, and returns focus to the opener', async () => {
    await vi.waitFor(() => expect(pane.root.querySelector('option')).not.toBeNull());
    pane.root.querySelector<HTMLButtonElement>(`[aria-label="${findRoom}"]`)!.click();
    const input = pane.root.querySelector<HTMLInputElement>('.map-pane__search')!;
    await Promise.resolve();
    expect(document.activeElement).toBe(input);
    (pane as unknown as { setStatus(text: string): void }).setStatus('stale localized status');
    pane.setLocale(locale === 'en' ? 'ru' : 'en');
    expect(pane.root.querySelector('.map-pane__popover')).toBeNull();
    const other = locale === 'en' ? 'Найти комнату' : 'Find room';
    const opener = pane.root.querySelector<HTMLButtonElement>(`[aria-label="${other}"]`)!;
    expect(document.activeElement).toBe(opener);
    expect((pane as unknown as { statusText: HTMLElement }).statusText.textContent).toBe('');
    const recordingLabel = locale === 'en'
      ? 'Диагностическое наблюдение за движением; комнаты не записываются'
      : 'Diagnostic movement observation; no rooms are written';
    expect(pane.root.querySelector('.map-pane__recording-toggle')?.getAttribute('aria-label')).toBe(recordingLabel);
  });

  it('ignores an unsupported live locale instead of falling back to English', async () => {
    await vi.waitFor(() => expect(pane.root.querySelector('option')).not.toBeNull());
    pane.setLocale('not-a-locale');
    expect(pane.root.querySelector(`[aria-label="${findRoom}"]`)).not.toBeNull();
  });

  it('localizes tracker and control fallback copy', async () => {
    await pane.onRoomPosition({
      valid: true, zone: 'Игровая зона', x: 0, y: 0, l: 0,
      confidence: 'yellow', pendingMoves: 2,
    });
    expect(pane.root.querySelector('.map-pane__confidence')?.getAttribute('title')).toContain(unconfirmed);

    pane.onMapRecordingState({ mode: 'conflict', pending: 0, revision: 1 });
    expect(pane.root.querySelector('.map-pane__recording-toggle')?.getAttribute('title')).toContain(conflictReason);

    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: 'paused' });
    expect(pane.root.querySelector('.map-pane__walk-progress')?.textContent).toContain(pausedReason);
  });

  it('drops a cached position reason only after a valid live locale switch', async () => {
    const staleReason = locale === 'en' ? 'assumed cell' : 'предполагаемая клетка';
    await pane.onRoomPosition({
      valid: true, zone: 'Игровая зона', x: 0, y: 0, l: 0,
      confidence: 'yellow', pendingMoves: 0, reason: staleReason,
    });
    const badge = pane.root.querySelector<HTMLElement>('.map-pane__confidence')!;
    expect(badge.title).toContain(staleReason);

    pane.setLocale('not-a-locale');
    expect(badge.title).toContain(staleReason);

    pane.setLocale(locale === 'en' ? 'ru' : 'en');
    expect(badge.title).not.toContain(staleReason);
    const expectedConfidence = locale === 'en' ? 'предположительная' : 'yellow';
    expect(badge.title).toContain(expectedConfidence);

    (pane as unknown as {
      openCellMenu(cell: { z: string; x: number; y: number; l: number }, room: null, x: number, y: number): void;
    }).openCellMenu({ z: 'Игровая зона', x: 0, y: 0, l: 0 }, null, 12, 12);
    const header = pane.root.querySelector('.map-cell-menu__header')!;
    expect(header.textContent).toContain(expectedConfidence);
    expect(header.textContent).not.toContain(staleReason);
  });

  it('does not revive cached old-locale walk or conflict reasons during a live switch', () => {
    const staleReason = locale === 'en' ? 'old English reason' : 'старая русская причина';
    pane.onMapRecordingState({ mode: 'conflict', pending: 0, revision: 1, reason: staleReason });
    pane.onMapWalk({ active: true, job_id: 4, current: 1, total: 3, state: 'paused', reason: staleReason });

    pane.setLocale(locale === 'en' ? 'ru' : 'en');
    const otherConflict = locale === 'en' ? 'конфликт' : 'conflict';
    const otherPaused = locale === 'en' ? 'приостановлен' : 'paused';
    const recording = pane.root.querySelector('.map-pane__recording-toggle')!;
    const walk = pane.root.querySelector('.map-pane__walk-progress')!;
    expect(recording.getAttribute('title')).toContain(otherConflict);
    expect(recording.getAttribute('title')).not.toContain(staleReason);
    expect(walk.textContent).toContain(otherPaused);
    expect(walk.textContent).not.toContain(staleReason);

    const terminalReason = locale === 'en' ? 'old English terminal reason' : 'старая русская причина остановки';
    pane.onMapWalk({ active: false, job_id: 4, current: 1, total: 3, state: 'stopped', reason: terminalReason });
    expect((pane as unknown as { statusText: HTMLElement }).statusText.textContent).toBe(terminalReason);
    pane.setLocale(locale);
    expect((pane as unknown as { statusText: HTMLElement }).statusText.textContent).toBe('');
  });

  it('localizes reconciliation coordinate labels and aria names', async () => {
    await vi.waitFor(() => expect(pane.root.querySelector('option')).not.toBeNull());
    const conflict = {
      id: 9,
      move: { command: 'n', direction: 'N', source: 'input', from: { zone: 'Игровая зона', x: 1, y: 0, l: 1 }, expected: { zone: 'Игровая зона', x: 0, y: 0, l: 0 } },
      expected: { zone: 'Игровая зона', x: 0, y: 0, l: 0 },
      expected_hint: 'Expected', expected_room: { hint: 'Expected', desc: '', exits: 'N' },
      observed: { hint: 'Observed', desc: '', exits: 'S' }, reason: 'mismatch',
    };
    pane.onMapRecordingState({ mode: 'conflict', pending: 0, revision: 1, conflict });
    const coordinate = pane.root.querySelector<HTMLInputElement>('input[type="number"]')!;
    expect(coordinate.closest('label')?.textContent).toContain(displayX);
    expect(coordinate.getAttribute('aria-label')).toBe(displayX);
  });
});
