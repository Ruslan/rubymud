import { afterEach, describe, expect, it, vi } from 'vitest';
import { AnsiUp } from 'ansi_up';

import { getAppElements } from './dom';
import { createRenderer } from './render';

function setupDOM() {
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

function createTestRenderer() {
  setupDOM();

  return createRenderer({
    elements: getAppElements(),
    ansiUp: new AnsiUp(),
    sendCommand: vi.fn(() => true),
    requestVariables: vi.fn(),
    requestGroups: vi.fn(),
    toggleGroup: vi.fn(),
    state: { activePanel: null, restoreInProgress: false },
  });
}

function paneSelectOptions(): string[] {
  const select = document.querySelector<HTMLSelectElement>('.pane-select');
  if (!select) {
    throw new Error('Missing pane select');
  }

  return Array.from(select.options).map(option => option.value);
}

afterEach(() => {
  localStorage.clear();
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

describe('renderer buffer catalog', () => {
  it('uses restore buffer names even when no entries arrive for them', () => {
    const renderer = createTestRenderer();

    renderer.loadLayout();
    renderer.clearOutput();
    renderer.setAvailableBuffers(['kills']);

    expect(paneSelectOptions()).toEqual(['kills', 'main']);
  });

  it('keeps the selected pane buffer visible when restore does not list it', () => {
    localStorage.setItem('pane-layout', JSON.stringify({
      columns: [{ id: 'col-1', panes: [{ id: 'pane-1', buffer: 'kills' }], rowSizes: [100] }],
      colSizes: [100],
    }));
    const renderer = createTestRenderer();

    renderer.loadLayout();
    renderer.clearOutput();
    renderer.setAvailableBuffers(['main']);

    const select = document.querySelector<HTMLSelectElement>('.pane-select');
    expect(select?.value).toBe('kills');
    expect(paneSelectOptions()).toEqual(['kills', 'main']);
  });
});
