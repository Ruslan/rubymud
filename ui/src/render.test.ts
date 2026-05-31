import { afterEach, describe, expect, it, vi } from 'vitest';
import { AnsiUp } from 'ansi_up';

import { applyAnsiTheme } from './ansi';
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

function createTestRenderer(state = { activePanel: null as const, restoreInProgress: false }) {
  setupDOM();

  return createRenderer({
    elements: getAppElements(),
    ansiUp: new AnsiUp(),
    sendCommand: vi.fn(() => true),
    requestVariables: vi.fn(),
    requestGroups: vi.fn(),
    toggleGroup: vi.fn(),
    state,
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

describe('renderer ANSI theme refresh', () => {
  it('re-renders buffered entries when ANSI theme changes', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    applyAnsiTheme('classic');

    renderer.appendEntries([{ id: 1, text: '\x1b[1;31mbold red\x1b[0m', buffer: 'main' }]);
    const output = document.querySelector<HTMLElement>('.pane-output');
    expect(output?.innerHTML).toContain('ansi-red-fg');
    expect(output?.innerHTML).not.toContain('ansi-bright-red-fg');

    applyAnsiTheme('high-contrast');
    renderer.refreshAnsiTheme();

    expect(output?.innerHTML).toContain('ansi-bright-red-fg');
    expect(output?.innerHTML).not.toContain('ansi-red-fg');
    expect(output?.textContent).toContain('bold red');
    expect(output?.querySelector('[data-entry-id="1"]')).not.toBeNull();
  });
});

describe('renderer output links', () => {
  it('renders https URLs as safe external links', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    renderer.appendEntries([{ id: 1, text: 'Read https://example.com/path?x=1#top now.', buffer: 'main' }]);

    const link = document.querySelector<HTMLAnchorElement>('.output-line a');
    expect(link?.textContent).toBe('https://example.com/path?x=1#top');
    expect(link?.classList.contains('ansi-bright-cyan-fg')).toBe(true);
    expect(link?.href).toBe('https://example.com/path?x=1#top');
    expect(link?.target).toBe('_blank');
    expect(link?.rel).toBe('noopener noreferrer');
    expect(document.querySelector('.output-line')?.textContent).toContain('now.');
  });

  it('does not linkify non-https URLs and leaves trailing punctuation outside links', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    renderer.appendEntries([{ id: 1, text: 'Old http://example.com new https://secure.example/path.', buffer: 'main' }]);

    const links = Array.from(document.querySelectorAll<HTMLAnchorElement>('.output-line a'));
    expect(links).toHaveLength(1);
    expect(links[0]?.textContent).toBe('https://secure.example/path');
    expect(document.querySelector('.output-line')?.textContent).toContain('http://example.com');
    expect(document.querySelector('.output-line')?.textContent).toContain('https://secure.example/path.');
  });
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

describe('renderer command trace reconciliation', () => {
  it('replaces a pending source hint with canonical commands', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'A goblin is here.', buffer: 'main' }]);

    renderer.appendCommandHint('bash $t1', 'cmd-1');
    expect(document.querySelector('.output-line')?.textContent).toContain('-> bash $t1');

    renderer.resolveCommandTrace('cmd-1', ['bash orc']);

    const text = document.querySelector('.output-line')?.textContent || '';
    expect(text).toContain('-> bash orc');
    expect(text).not.toContain('bash $t1');
  });

  it('removes a pending source hint when trace resolves to local-only empty commands', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'A goblin is here.', buffer: 'main' }]);

    renderer.appendCommandHint('#showme {hi}', 'cmd-2');
    renderer.resolveCommandTrace('cmd-2', []);

    expect(document.querySelector('.output-line')?.textContent).not.toContain('#showme');
    expect(document.querySelector('.output-hint')).toBeNull();
  });

  it('resolves a pending source hint without re-rendering the whole pane', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([
      { id: 1, text: 'A goblin is here.', buffer: 'main' },
      { id: 2, text: 'A troll is here.', buffer: 'main' },
    ]);

    const lineBefore = document.querySelector<HTMLElement>('[data-entry-id="2"]');
    renderer.appendCommandHint('bash $t1', 'cmd-3');
    expect(lineBefore?.querySelector('[data-client-command-id="cmd-3"]')).not.toBeNull();

    renderer.resolveCommandTrace('cmd-3', ['bash troll']);

    const lineAfter = document.querySelector<HTMLElement>('[data-entry-id="2"]');
    expect(lineAfter).toBe(lineBefore);
    expect(lineAfter?.textContent).toContain('-> bash troll');
    expect(lineAfter?.textContent).not.toContain('bash $t1');
    expect(lineAfter?.querySelector('[data-client-command-id="cmd-3"]')).toBeNull();
  });

  it('deduplicates overlapping catch-up entries and exposes the latest entry id', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    renderer.appendEntries([{ id: 1, text: 'first', buffer: 'main' }]);
    renderer.appendEntries([
      { id: 1, text: 'first duplicate', buffer: 'main' },
      { id: 2, text: 'second', buffer: 'main' },
    ]);

    const lines = Array.from(document.querySelectorAll('.output-line')).map((line) => line.textContent || '');
    expect(lines).toEqual(['first', 'second']);
    expect(renderer.latestEntryID()).toBe(2);
  });
});

describe('renderer BEL metadata', () => {
  it('triggers visual bell for live entries with bell metadata', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    renderer.appendEntries([{ id: 1, text: '[BEL] alert', buffer: 'main', bell_positions: [0] }]);

    expect(document.querySelector('.pane-output')?.classList.contains('visual-bell')).toBe(true);
  });

  it('does not trigger visual bell for literal backslash text without metadata', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    renderer.appendEntries([{ id: 1, text: '[\\x07] literal', buffer: 'main' }]);

    expect(document.querySelector('.pane-output')?.classList.contains('visual-bell')).toBe(false);
  });

  it('does not replay visual bell while restoring entries', () => {
    const renderer = createTestRenderer({ activePanel: null, restoreInProgress: true });
    renderer.loadLayout();

    renderer.appendEntries([{ id: 1, text: '[BEL] alert', buffer: 'main', bell_positions: [0] }]);

    expect(document.querySelector('.pane-output')?.classList.contains('visual-bell')).toBe(false);
  });
});

describe('renderer variables panel', () => {
  it('updates a focused variable input when the user has not edited it', () => {
    const renderer = createTestRenderer();

    renderer.renderVariables([{ name: 'target', value: 'old', default_value: '', declared: true, has_value: true, uses_default: false }]);
    const input = document.querySelector<HTMLInputElement>('[data-var-key="target"]');
    input?.focus();

    renderer.renderVariables([{ name: 'target', value: 'new', default_value: '', declared: true, has_value: true, uses_default: false }]);

    expect(document.querySelector<HTMLInputElement>('[data-var-key="target"]')?.value).toBe('new');
  });

  it('preserves a dirty focused variable input while rendering fresh data', () => {
    const renderer = createTestRenderer();

    renderer.renderVariables([{ name: 'target', value: 'old', default_value: '', declared: true, has_value: true, uses_default: false }]);
    const input = document.querySelector<HTMLInputElement>('[data-var-key="target"]');
    input?.focus();
    if (input) input.value = 'unsaved';

    renderer.renderVariables([{ name: 'target', value: 'new', default_value: '', declared: true, has_value: true, uses_default: false }]);

    expect(document.querySelector<HTMLInputElement>('[data-var-key="target"]')?.value).toBe('unsaved');
  });
});
