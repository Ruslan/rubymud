import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AnsiUp } from 'ansi_up';

import { getAppElements } from './dom';
import { createRenderer } from './render';
import { MapPaneController } from './map/pane';
import type { SlimRoom } from './map/types';

function response(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => typeof body === 'string' ? body : JSON.stringify(body),
  } as unknown as Response;
}

function setupDOM(): void {
  document.body.innerHTML = `
    <div id="panes-container"></div>
    <button id="panel-toggle-groups"></button>
    <div id="bottom-panels"></div>
    <div id="hotkeys"></div>
    <div id="variables-list"></div>
    <div id="groups-list"></div>
    <div id="panel-groups"></div>
    <div id="panel-keyboard"></div>
    <div id="panel-variables"></div>
    <button id="panel-toggle-keyboard"></button>
    <button id="panel-toggle-variables"></button>
    <button id="btn-open-settings"></button>
    <button id="history-up"></button>
    <button id="history-down"></button>
    <div id="history-suggestions"></div>
    <span id="connection-status"></span>
    <span id="ticker"></span>
    <div id="secondary-timers"></div>
    <input id="input" />
  `;
}

function stubCanvas(): void {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => new Proxy(
    { fillStyle: '', strokeStyle: '', lineWidth: 0, font: '', textAlign: '', textBaseline: '' },
    { get: (target, property) => property in target ? (target as never)[property] : () => {} },
  ) as unknown as CanvasRenderingContext2D);
}

function room(zone: string, x = 0): SlimRoom {
  return { z: zone, t: null, h: `${zone} room`, x, y: 0, l: 0, e: [], d: [], ch: 0, se: [], p: false, i: null, s: false, dx: 0, dy: 0, dl: 0, img: 0, m: [], mb: [], g: [], gf: [] };
}

function createTestRenderer() {
  localStorage.setItem('pane-layout', JSON.stringify({
    columns: [{ id: 'col-1', panes: [{ id: 'pane-map', buffer: '~map' }], rowSizes: [100] }],
    colSizes: [100],
  }));
  return createRenderer({
    elements: getAppElements(),
    ansiUp: new AnsiUp(),
    sendCommand: vi.fn(() => true),
    requestVariables: vi.fn(),
    requestGroups: vi.fn(),
    toggleGroup: vi.fn(),
    state: { activePanel: null, restoreInProgress: false },
    sessionID: 7,
    getActiveMapSetID: () => 1,
  });
}

describe('renderer mapper event delivery', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    setupDOM();
    stubCanvas();
    localStorage.clear();
    fetchMock = vi.fn((url: string) => {
      if (url.includes('map-live-room')) return Promise.resolve(response({ seen: false }));
      if (url.includes('/zones')) return Promise.resolve(response([
        { zone: 'A', room_count: 1 }, { zone: 'B', room_count: 1 },
      ]));
      if (url.includes('zone=A')) return Promise.resolve(response([room('A')]));
      if (url.includes('zone=B')) return Promise.resolve(response([room('B')]));
      throw new Error(`unexpected ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = '';
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('does not block recording state or reconnect reset behind a hung position load', async () => {
    let resolveB!: (value: Response) => void;
    const hungB = new Promise<Response>((resolve) => { resolveB = resolve; });
    fetchMock.mockImplementation((url: string) => {
      if (url.includes('map-live-room')) return Promise.resolve(response({ seen: false }));
      if (url.includes('/zones')) return Promise.resolve(response([
        { zone: 'A', room_count: 1 }, { zone: 'B', room_count: 1 },
      ]));
      if (url.includes('zone=A')) return Promise.resolve(response([room('A')]));
      if (url.includes('zone=B')) return hungB;
      throw new Error(`unexpected ${url}`);
    });
    const renderer = createTestRenderer();
    renderer.loadLayout();
    await vi.waitFor(() => expect(document.querySelector('.map-pane__zone')).not.toBeNull());

    renderer.handleRoomPosition({
      valid: true, zone: 'B', x: 0, y: 0, l: 0, confidence: 'yellow', pendingMoves: 1,
    });
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => url.includes('zone=B'))).toBe(true));

    renderer.handleMapRecording({ mode: 'recording', pending: 1, revision: 1 });
    expect(document.querySelector('.map-pane__recording-toggle')?.textContent).toBe('STOP OBS');
    renderer.resetMapRecordingRevision();
    expect(document.querySelector('.map-pane__recording-toggle')?.textContent).toBe('OBS');
    resolveB(response([room('B')]));
  });

  it('coalesces a synchronous burst of positions to the latest value', async () => {
    const onRoomPosition = vi.spyOn(MapPaneController.prototype, 'onRoomPosition')
      .mockResolvedValue(undefined);
    const renderer = createTestRenderer();
    renderer.loadLayout();
    await vi.waitFor(() => expect(document.querySelector('.map-pane__zone')).not.toBeNull());
    onRoomPosition.mockClear();

    for (let i = 0; i < 100; i++) {
      renderer.handleRoomPosition({
        valid: true, zone: 'A', x: i, y: 0, l: 0, confidence: 'green', pendingMoves: 0,
      });
    }
    await Promise.resolve();
    expect(onRoomPosition).toHaveBeenCalledTimes(1);
    expect(onRoomPosition.mock.calls[0]?.[0].x).toBe(99);
  });

  it('delivers scan hints only for the current coordinate and clears them on movement', async () => {
    const onRoomScan = vi.spyOn(MapPaneController.prototype, 'onRoomScan');
    const renderer = createTestRenderer();
    renderer.loadLayout();
    await vi.waitFor(() => expect(document.querySelector('.map-pane__zone')).not.toBeNull());
    renderer.handleRoomPosition({ valid: true, zone: 'A', x: 0, y: 0, l: 0, confidence: 'green', pendingMoves: 0, eventDescribesCell: true });
    await Promise.resolve();
    onRoomScan.mockClear();

    const scan = { mapSetID: 1, recordingRevision: 1, zone: 'A', x: 0, y: 0, l: 0, hints: [{ dir: 'E', hint: 'Unknown path.' }] };
    renderer.handleRoomScan(scan);
    expect(onRoomScan).toHaveBeenLastCalledWith(scan);

    renderer.handleRoomPosition({ valid: true, zone: 'B', x: 0, y: 0, l: 0, confidence: 'green', pendingMoves: 0 });
    expect(onRoomScan).toHaveBeenLastCalledWith(null);

    renderer.handleRoomPosition({ valid: true, zone: 'A', x: 0, y: 0, l: 0, confidence: 'green', pendingMoves: 0, eventDescribesCell: true });
    renderer.handleMapRecording({ mode: 'off', pending: 0, revision: 2 });
    onRoomScan.mockClear();
    renderer.handleRoomScan(scan);
    expect(onRoomScan).not.toHaveBeenCalled();
  });

  it('clears scan hints when exact green evidence becomes assumed at the same coordinate', async () => {
    const onRoomScan = vi.spyOn(MapPaneController.prototype, 'onRoomScan');
    const renderer = createTestRenderer();
    renderer.loadLayout();
    await vi.waitFor(() => expect(document.querySelector('.map-pane__zone')).not.toBeNull());
    renderer.handleRoomPosition({
      valid: true, zone: 'A', x: 0, y: 0, l: 0, confidence: 'green', pendingMoves: 0,
      eventDescribesCell: true,
    });
    await Promise.resolve();
    const scan = { mapSetID: 1, recordingRevision: 1, zone: 'A', x: 0, y: 0, l: 0, hints: [{ dir: 'E', hint: 'Unknown path.' }] };
    renderer.handleRoomScan(scan);
    onRoomScan.mockClear();

    renderer.handleRoomPosition({
      valid: true, zone: 'A', x: 0, y: 0, l: 0, confidence: 'yellow', pendingMoves: 0,
      eventDescribesCell: false,
    });
    expect(onRoomScan).toHaveBeenLastCalledWith(null);
    onRoomScan.mockClear();
    renderer.handleRoomScan(scan);
    expect(onRoomScan).not.toHaveBeenCalled();
  });

  it('replays a cached scan after position when a map pane is mounted later', async () => {
    const renderer = createTestRenderer();
    renderer.handleRoomPosition({ valid: true, zone: 'A', x: 0, y: 0, l: 0, confidence: 'green', pendingMoves: 0, eventDescribesCell: true });
    const scan = { mapSetID: 1, recordingRevision: 1, zone: 'A', x: 0, y: 0, l: 0, hints: [{ dir: 'E', hint: 'Unknown path.' }] };
    renderer.handleRoomScan(scan);
    const onRoomScan = vi.spyOn(MapPaneController.prototype, 'onRoomScan');

    renderer.loadLayout();
    await Promise.resolve();
    await Promise.resolve();
    expect(onRoomScan).toHaveBeenCalledWith(scan);
  });

  it('does not revive a pre-locale position reason from a pending mapper event', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    await vi.waitFor(() => expect(document.querySelector('.map-pane__zone')).not.toBeNull());

    renderer.handleRoomPosition({
      valid: true, zone: 'A', x: 0, y: 0, l: 0, confidence: 'yellow', pendingMoves: 0,
      reason: 'old locale reason',
    });
    renderer.setMapperLocale('ru');
    await Promise.resolve();
    expect(document.querySelector('.map-pane__confidence')?.getAttribute('title')).not.toContain('old locale reason');
  });

  it('does not replay cached recording or walk reasons after locale rebuild', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    await vi.waitFor(() => expect(document.querySelector('.map-pane__zone')).not.toBeNull());

    renderer.handleMapRecording({ mode: 'conflict', pending: 0, revision: 3, reason: 'old recording locale reason' });
    renderer.handleMapWalk({ active: false, current: 0, total: 0, state: 'stopped', reason: 'old walk locale reason' });
    expect(document.querySelector('.map-pane__recording-toggle')?.getAttribute('title')).toContain('old recording locale reason');
    expect(document.querySelector('.map-pane__status')?.textContent).toContain('old walk locale reason');

    renderer.setMapperLocale('ru');
    renderer.loadLayout();
    expect(document.querySelector('.map-pane__recording-toggle')?.getAttribute('title')).not.toContain('old recording locale reason');
    expect(document.querySelector('.map-pane__status')?.textContent).not.toContain('old walk locale reason');
  });
});
