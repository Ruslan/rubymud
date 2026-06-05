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

function createTestRenderer(state = { activePanel: null as const, restoreInProgress: false }, fontSizeControls?: HTMLElement) {
  setupDOM();

  return createRenderer({
    elements: getAppElements(),
    ansiUp: new AnsiUp(),
    fontSizeControls,
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

describe('renderer temporary live split', () => {
  it('toggles a temporary live split with the same buffer', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.setAvailableBuffers(['combat']);

    const select = document.querySelector<HTMLSelectElement>('.pane-select');
    if (!select) throw new Error('Missing pane select');
    select.value = 'combat';
    select.dispatchEvent(new Event('change'));

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const panes = Array.from(document.querySelectorAll<HTMLElement>('.pane'));
    expect(panes).toHaveLength(2);
    expect(document.querySelectorAll('.pane_live-temporary')).toHaveLength(1);
    expect(document.querySelectorAll<HTMLSelectElement>('.pane-select')).toHaveLength(1);
    expect(document.querySelector<HTMLSelectElement>('.pane-select')?.value).toBe('combat');
    expect(document.querySelectorAll('.pane-live-split-btn')).toHaveLength(1);
  });

  it('temporary live pane has no header but keeps body, output, and scroll button', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const livePane = document.querySelector<HTMLElement>('.pane_live-temporary');
    expect(livePane).not.toBeNull();
    expect(livePane?.querySelector('.pane-header')).toBeNull();
    expect(livePane?.querySelector('.pane-body')).not.toBeNull();
    expect(livePane?.querySelector('.pane-output')).not.toBeNull();
    expect(livePane?.querySelector('.pane-scroll-bottom')).not.toBeNull();
  });

  it('places the toggle immediately after the source pane select', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    const select = document.querySelector<HTMLSelectElement>('.pane-header .pane-select');
    const toggle = document.querySelector<HTMLButtonElement>('.pane-header .pane-live-split-btn');
    expect(select?.nextElementSibling).toBe(toggle);
  });

  it('keeps font controls in the right action group while the toggle stays next to the select', () => {
    const fontControls = document.createElement('div');
    fontControls.className = 'font-size-controls';
    const renderer = createTestRenderer({ activePanel: null, restoreInProgress: false }, fontControls);
    renderer.loadLayout();

    const select = document.querySelector<HTMLSelectElement>('.pane-header .pane-select');
    const toggle = document.querySelector<HTMLButtonElement>('.pane-header .pane-live-split-btn');
    expect(select?.nextElementSibling).toBe(toggle);
    expect(document.querySelector('.pane-actions .font-size-controls')).toBe(fontControls);
  });

  it('does not persist the temporary split into pane-layout', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(2);
    expect(localStorage.getItem('pane-layout')).toBeNull();

    const rendererAfterRefresh = createTestRenderer();
    rendererAfterRefresh.loadLayout();
    expect(document.querySelectorAll('.pane')).toHaveLength(1);
  });

  it('toggle off leaves the live pane as the surviving view and forces scroll bottom', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'first', buffer: 'main' }]);

    const scrollHeightSpy = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(321);
    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn.active')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const panes = Array.from(document.querySelectorAll<HTMLElement>('.pane'));
    expect(panes).toHaveLength(1);
    expect(document.querySelectorAll('.pane_live-temporary')).toHaveLength(0);
    const output = document.querySelector<HTMLElement>('.pane-output');
    expect(output?.textContent).toContain('first');
    expect(output?.scrollTop).toBe(321);
    scrollHeightSpy.mockRestore();
  });

  it('renders newly appended entries in both panes through the existing append path', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    renderer.appendEntries([{ id: 1, text: 'shared line', buffer: 'main' }]);

    const outputs = Array.from(document.querySelectorAll<HTMLElement>('.pane-output'));
    expect(outputs).toHaveLength(2);
    expect(outputs.map(output => output.textContent)).toEqual(['shared line', 'shared line']);
  });

  it('disables menu close and persistent split actions while source has active mini-split', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const actions = Array.from(document.querySelectorAll<HTMLButtonElement>('.pane:not(.pane_live-temporary) .dropdown-item'));
    expect(actions.map(action => action.textContent)).toEqual(['Split Down', 'Split Right', 'Close']);
    expect(actions.every(action => action.disabled)).toBe(true);

    actions[0]?.click();
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(document.querySelectorAll('.pane')).toHaveLength(2);
  });

  it('source selector change collapses mini-split with source as survivor and selected buffer active', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.setAvailableBuffers(['combat']);

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const sourceSelect = document.querySelector<HTMLSelectElement>('.pane:not(.pane_live-temporary) .pane-select');
    if (!sourceSelect) throw new Error('Missing source select');
    sourceSelect.value = 'combat';
    sourceSelect.dispatchEvent(new Event('change'));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(1);
    expect(document.querySelectorAll('.pane_live-temporary')).toHaveLength(0);
    expect(document.querySelector<HTMLSelectElement>('.pane-select')?.value).toBe('combat');

    renderer.appendEntries([
      { id: 1, text: 'old main line', buffer: 'main' },
      { id: 2, text: 'combat line', buffer: 'combat' },
    ]);

    expect(document.querySelector<HTMLElement>('.pane-output')?.textContent).toBe('combat line');
  });

  it('reuses the persisted live split ratio for the same buffer', async () => {
    localStorage.setItem('liveSplitRatio:v1:main', '30');
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const panes = Array.from(document.querySelectorAll<HTMLElement>('.pane'));
    expect(panes.map(pane => pane.style.flexBasis)).toEqual(['70%', '30%']);
  });

  it('persists the live split ratio for the buffer after row resize', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    const clientHeightSpy = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(100);
    const setPointerCapture = vi.fn();
    const releasePointerCapture = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', { configurable: true, value: setPointerCapture });
    Object.defineProperty(HTMLElement.prototype, 'releasePointerCapture', { configurable: true, value: releasePointerCapture });

    const handle = document.querySelector<HTMLElement>('.row-resize-handle');
    if (!handle) throw new Error('Missing row resize handle');
    const pointerDown = new Event('pointerdown') as PointerEvent;
    Object.defineProperties(pointerDown, { pointerId: { value: 1 }, clientY: { value: 0 } });
    handle.dispatchEvent(pointerDown);
    const pointerMove = new Event('pointermove') as PointerEvent;
    Object.defineProperties(pointerMove, { pointerId: { value: 1 }, clientY: { value: 20 } });
    handle.dispatchEvent(pointerMove);
    const pointerUp = new Event('pointerup') as PointerEvent;
    Object.defineProperty(pointerUp, 'pointerId', { value: 1 });
    handle.dispatchEvent(pointerUp);

    expect(localStorage.getItem('liveSplitRatio:v1:main')).toBe('30');
    clientHeightSpy.mockRestore();
  });
});

describe('renderer buffer-local search', () => {
  it('places search controls after the mini-split toggle in the pane header', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    const toggle = document.querySelector<HTMLElement>('.pane-live-split-btn');
    const search = document.querySelector<HTMLElement>('.pane-search-toggle');
    expect(document.querySelector<HTMLElement>('.pane-select')?.nextElementSibling).toBe(toggle);
    expect(toggle?.nextElementSibling).toBe(search);
  });

  it('searches only the current pane buffer', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.setAvailableBuffers(['combat']);
    renderer.addColumnRight();
    renderer.appendEntries([
      { id: 1, text: 'main only', buffer: 'main' },
      { id: 2, text: 'combat only', buffer: 'combat' },
    ]);

    const secondSelect = document.querySelectorAll<HTMLSelectElement>('.pane-select')[1];
    if (!secondSelect) throw new Error('Missing second select');
    secondSelect.value = 'combat';
    secondSelect.dispatchEvent(new Event('change'));

    const secondPane = document.querySelectorAll<HTMLElement>('.pane')[1];
    secondPane?.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = secondPane?.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'main';
    input.dispatchEvent(new Event('input'));

    expect(secondPane?.querySelector('.pane-search-count')?.textContent).toBe('0/0');
    expect(secondPane?.querySelector('.buffer-search-match')).toBeNull();
  });

  it('search is case-insensitive with Cyrillic', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'ПрИвЕт мир', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'привет';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.buffer-search-current')?.textContent).toBe('ПрИвЕт');
  });

  it('searches ANSI-stripped output and highlights rendered text', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: '\x1b[31mRed target\x1b[0m', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.buffer-search-current')?.textContent).toBe('target');
    expect(document.querySelector('.output-line')?.innerHTML).toContain('ansi-red-fg');
  });

  it('matches text across ANSI style boundaries', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: '\x1b[31mtar\x1b[0mget', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    const currentText = Array.from(document.querySelectorAll('.buffer-search-current')).map(el => el.textContent).join('');
    expect(currentText).toBe('target');
    expect(document.querySelector('.output-line')?.innerHTML).toContain('ansi-red-fg');
  });

  it('ignores command hints and trigger button labels', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'plain output', buffer: 'main', buttons: [{ label: 'target button', command: 'look' }] }]);
    renderer.addCommandHint(1, 'main', 'target hint');

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');
    expect(document.querySelector('.buffer-search-match')).toBeNull();
    expect(document.querySelector('.output-line')?.textContent).toContain('target button');
    expect(document.querySelector('.output-line')?.textContent).toContain('target hint');
  });

  it('query change selects newest match and older/newer navigation wraps', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([
      { id: 1, text: 'old target', buffer: 'main' },
      { id: 2, text: 'new target', buffer: 'main' },
    ]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('2/2');
    expect(document.querySelector('.buffer-search-current')?.closest('.output-line')?.textContent).toContain('new target');

    document.querySelector<HTMLButtonElement>('.pane-search-prev')?.click();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/2');
    expect(document.querySelector('.buffer-search-current')?.closest('.output-line')?.textContent).toContain('old target');

    document.querySelector<HTMLButtonElement>('.pane-search-next')?.click();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('2/2');
    document.querySelector<HTMLButtonElement>('.pane-search-next')?.click();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/2');
  });

  it('opening search on main does not create mini-split before a matching query', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(1);
    expect(document.querySelector<HTMLInputElement>('.pane-search-input')).not.toBeNull();
  });

  it('entering a query with no results on main does not create mini-split', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello world', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'missing';
    input.dispatchEvent(new Event('input'));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(1);
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');
  });

  it('entering a matching query on main creates mini-split and keeps search input usable after rebuild', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));
    await new Promise(resolve => setTimeout(resolve, 0));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(2);
    expect(document.querySelector('.pane_live-temporary .pane-header')).toBeNull();
    const rebuiltInput = document.querySelector<HTMLInputElement>('.pane:not(.pane_live-temporary) .pane-search-input');
    expect(rebuiltInput?.value).toBe('target');
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.activeElement).toBe(rebuiltInput);
  });

  it('changing a successful main search to no results leaves the existing mini-split open', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    let input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(document.querySelectorAll('.pane')).toHaveLength(2);

    input = document.querySelector<HTMLInputElement>('.pane:not(.pane_live-temporary) .pane-search-input');
    if (!input) throw new Error('Missing rebuilt search input');
    input.value = 'missing';
    input.dispatchEvent(new Event('input'));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(2);
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');
  });

  it('non-main query with results does not auto-split', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.setAvailableBuffers(['combat']);
    renderer.appendEntries([{ id: 1, text: 'combat target', buffer: 'combat' }]);

    const select = document.querySelector<HTMLSelectElement>('.pane-select');
    if (!select) throw new Error('Missing select');
    select.value = 'combat';
    select.dispatchEvent(new Event('change'));
    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(document.querySelectorAll('.pane')).toHaveLength(1);
    expect(document.querySelector<HTMLSelectElement>('.pane-select')?.value).toBe('combat');
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
  });

  it('marks all matches weakly and the current match strongly', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'target and target', buffer: 'main' }]);

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelectorAll('.buffer-search-match')).toHaveLength(2);
    expect(document.querySelectorAll('.buffer-search-current')).toHaveLength(1);
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('2/2');
  });

  it('renders in-memory matches outside the normal rendered tail while search is active', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    const entries = Array.from({ length: 5001 }, (_, index) => ({
      id: index + 1,
      text: index === 0 ? 'old hidden target' : `line ${index}`,
      buffer: 'main',
    }));
    renderer.appendEntries(entries);

    expect(document.querySelector('.output-line')?.textContent).not.toContain('old hidden target');

    document.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
    const input = document.querySelector<HTMLInputElement>('.pane-search-input');
    if (!input) throw new Error('Missing search input');
    input.value = 'target';
    input.dispatchEvent(new Event('input'));

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.buffer-search-current')?.closest('.output-line')?.textContent).toContain('old hidden target');
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
