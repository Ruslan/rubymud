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

function flush(): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, 0));
}

function toggleRegion() {
  document.querySelector<HTMLButtonElement>('.pane-live-split-btn')?.click();
}

function openSearch(root: ParentNode = document) {
  root.querySelector<HTMLButtonElement>('.pane-search-toggle')?.click();
}

function setSearchQuery(value: string, root: ParentNode = document): HTMLInputElement {
  const input = root.querySelector<HTMLInputElement>('.pane-search-input');
  if (!input) throw new Error('Missing search input');
  input.value = value;
  input.dispatchEvent(new Event('input'));
  return input;
}

function pressSearchKey(key: string, shiftKey = false, root: ParentNode = document) {
  const input = root.querySelector<HTMLInputElement>('.pane-search-input');
  if (!input) throw new Error('Missing search input');
  input.dispatchEvent(new KeyboardEvent('keydown', { key, shiftKey }));
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

describe('renderer scrollback region', () => {
  it('opens a scrollback region above the live output inside the same pane', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    toggleRegion();

    expect(document.querySelectorAll('.pane')).toHaveLength(1);
    const region = document.querySelector<HTMLElement>('.pane-scrollback');
    expect(region).not.toBeNull();
    expect(region?.previousElementSibling?.classList.contains('pane-header')).toBe(true);
    expect(region?.nextElementSibling?.classList.contains('pane-scrollback-divider')).toBe(true);
    expect(region?.nextElementSibling?.nextElementSibling?.classList.contains('pane-body')).toBe(true);
    expect(document.querySelectorAll('.pane-select')).toHaveLength(1);
    expect(document.querySelector('.pane-live-split-btn')?.classList.contains('active')).toBe(true);
  });

  it('never rebuilds the live output DOM when the region opens and closes', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'first', buffer: 'main' }]);

    const liveOutputBefore = document.querySelector<HTMLElement>('.pane-body .pane-output');
    const lineBefore = document.querySelector<HTMLElement>('.pane-body [data-entry-id="1"]');

    toggleRegion();
    expect(document.querySelector<HTMLElement>('.pane-body .pane-output')).toBe(liveOutputBefore);
    expect(document.querySelector<HTMLElement>('.pane-body [data-entry-id="1"]')).toBe(lineBefore);

    toggleRegion();
    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector<HTMLElement>('.pane-body .pane-output')).toBe(liveOutputBefore);
    expect(document.querySelector<HTMLElement>('.pane-body [data-entry-id="1"]')).toBe(lineBefore);
  });

  it('renders the same buffer content in the scrollback region', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'history line', buffer: 'main' }]);

    toggleRegion();

    expect(document.querySelector('.pane-scrollback .pane-output')?.textContent).toContain('history line');
    expect(document.querySelector('.pane-body .pane-output')?.textContent).toContain('history line');
  });

  it('does not persist the region into pane-layout', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    toggleRegion();

    expect(document.querySelector('.pane-scrollback')).not.toBeNull();
    expect(localStorage.getItem('pane-layout')).toBeNull();

    const rendererAfterRefresh = createTestRenderer();
    rendererAfterRefresh.loadLayout();
    expect(document.querySelectorAll('.pane')).toHaveLength(1);
    expect(document.querySelector('.pane-scrollback')).toBeNull();
  });

  it('close removes the region, keeps live content, and forces scroll to bottom', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'first', buffer: 'main' }]);

    const scrollHeightSpy = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(321);
    toggleRegion();
    toggleRegion();

    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector('.pane-live-split-btn')?.classList.contains('active')).toBe(false);
    const output = document.querySelector<HTMLElement>('.pane-body .pane-output');
    expect(output?.textContent).toContain('first');
    expect(output?.scrollTop).toBe(321);
    scrollHeightSpy.mockRestore();
  });

  it('appends new entries to both the live and scrollback outputs', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    toggleRegion();
    renderer.appendEntries([{ id: 1, text: 'shared line', buffer: 'main' }]);

    expect(document.querySelector('.pane-body .pane-output')?.textContent).toContain('shared line');
    expect(document.querySelector('.pane-scrollback .pane-output')?.textContent).toContain('shared line');
  });

  it('reuses the persisted live split ratio for the same buffer', () => {
    localStorage.setItem('liveSplitRatio:v1:main', '30');
    const renderer = createTestRenderer();
    renderer.loadLayout();

    toggleRegion();

    expect(document.querySelector<HTMLElement>('.pane-scrollback')?.style.flexBasis).toBe('70%');
  });

  it('persists the live split ratio for the buffer after divider resize', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    toggleRegion();

    const clientHeightSpy = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(100);
    const setPointerCapture = vi.fn();
    const releasePointerCapture = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', { configurable: true, value: setPointerCapture });
    Object.defineProperty(HTMLElement.prototype, 'releasePointerCapture', { configurable: true, value: releasePointerCapture });

    const divider = document.querySelector<HTMLElement>('.pane-scrollback-divider');
    if (!divider) throw new Error('Missing scrollback divider');
    const pointerDown = new Event('pointerdown') as PointerEvent;
    Object.defineProperties(pointerDown, { pointerId: { value: 1 }, clientY: { value: 0 } });
    divider.dispatchEvent(pointerDown);
    const pointerMove = new Event('pointermove') as PointerEvent;
    Object.defineProperties(pointerMove, { pointerId: { value: 1 }, clientY: { value: 20 } });
    divider.dispatchEvent(pointerMove);
    const pointerUp = new Event('pointerup') as PointerEvent;
    Object.defineProperty(pointerUp, 'pointerId', { value: 1 });
    divider.dispatchEvent(pointerUp);

    expect(localStorage.getItem('liveSplitRatio:v1:main')).toBe('30');
    clientHeightSpy.mockRestore();
  });

  it('changing the pane buffer closes the region and resets search', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.setAvailableBuffers(['combat']);

    toggleRegion();
    expect(document.querySelector('.pane-scrollback')).not.toBeNull();

    const select = document.querySelector<HTMLSelectElement>('.pane-select');
    if (!select) throw new Error('Missing pane select');
    select.value = 'combat';
    select.dispatchEvent(new Event('change'));

    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector('.pane-live-split-btn')?.classList.contains('active')).toBe(false);
    expect(document.querySelector<HTMLElement>('.pane-search-controls')?.hidden).toBe(true);
  });

  it('keeps layout actions enabled and restores the region across a layout rebuild', async () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'first', buffer: 'main' }]);

    toggleRegion();

    const actions = Array.from(document.querySelectorAll<HTMLButtonElement>('.dropdown-item'));
    expect(actions.map(action => action.textContent)).toEqual(['Split Down', 'Split Right', 'Close']);
    expect(actions.every(action => !action.disabled)).toBe(true);

    actions[0]?.click();
    await flush();

    expect(document.querySelectorAll('.pane')).toHaveLength(2);
    expect(document.querySelectorAll('.pane-scrollback')).toHaveLength(1);
    expect(document.querySelector('.pane-scrollback .pane-output')?.textContent).toContain('first');
  });
});

describe('renderer buffer-local search', () => {
  it('places search controls after the region toggle in the pane header', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    const toggle = document.querySelector<HTMLElement>('.pane-live-split-btn');
    const search = document.querySelector<HTMLElement>('.pane-search-toggle');
    expect(document.querySelector<HTMLElement>('.pane-select')?.nextElementSibling).toBe(toggle);
    expect(toggle?.nextElementSibling).toBe(search);
  });

  it('typing does not execute the search; Enter executes and opens the region', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');

    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector('.buffer-search-match')).toBeNull();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');

    pressSearchKey('Enter');

    expect(document.querySelector('.pane-scrollback')).not.toBeNull();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.pane-scrollback .buffer-search-current')?.textContent).toBe('target');
  });

  it('executes the query via the header search button when the controls are already open', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    openSearch();

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.pane-scrollback .buffer-search-current')?.textContent).toBe('target');
  });

  it('searches only the current pane buffer', () => {
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
    if (!secondPane) throw new Error('Missing second pane');
    openSearch(secondPane);
    setSearchQuery('main', secondPane);
    pressSearchKey('Enter', false, secondPane);

    expect(secondPane.querySelector('.pane-search-count')?.textContent).toBe('0/0');
    expect(secondPane.querySelector('.buffer-search-match')).toBeNull();
    expect(secondPane.querySelector('.pane-scrollback')).toBeNull();
  });

  it('search is case-insensitive with Cyrillic', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'ПрИвЕт мир', buffer: 'main' }]);

    openSearch();
    setSearchQuery('привет');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.buffer-search-current')?.textContent).toBe('ПрИвЕт');
  });

  it('searches ANSI-stripped output and highlights rendered text', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: '\x1b[31mRed target\x1b[0m', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.buffer-search-current')?.textContent).toBe('target');
    expect(document.querySelector('.pane-scrollback .output-line')?.innerHTML).toContain('ansi-red-fg');
  });

  it('matches text across ANSI style boundaries', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: '\x1b[31mtar\x1b[0mget', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    const currentText = Array.from(document.querySelectorAll('.buffer-search-current')).map(el => el.textContent).join('');
    expect(currentText).toBe('target');
    expect(document.querySelector('.pane-scrollback .output-line')?.innerHTML).toContain('ansi-red-fg');
  });

  it('ignores command hints and trigger button labels', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'plain output', buffer: 'main', buttons: [{ label: 'target button', command: 'look' }] }]);
    renderer.addCommandHint(1, 'main', 'target hint');

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');
    expect(document.querySelector('.buffer-search-match')).toBeNull();
    expect(document.querySelector('.output-line')?.textContent).toContain('target button');
    expect(document.querySelector('.output-line')?.textContent).toContain('target hint');
  });

  it('execution selects the newest match and older/newer navigation wraps', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([
      { id: 1, text: 'old target', buffer: 'main' },
      { id: 2, text: 'new target', buffer: 'main' },
    ]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

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

  it('navigates matches without re-rendering the scrollback content', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([
      { id: 1, text: 'old target', buffer: 'main' },
      { id: 2, text: 'new target', buffer: 'main' },
    ]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

    const lineBefore = document.querySelector<HTMLElement>('.pane-scrollback [data-entry-id="1"]');
    document.querySelector<HTMLButtonElement>('.pane-search-prev')?.click();

    expect(document.querySelector<HTMLElement>('.pane-scrollback [data-entry-id="1"]')).toBe(lineBefore);
    expect(document.querySelector('.buffer-search-current')?.closest('.output-line')?.textContent).toContain('old target');
  });

  it('Enter moves to the older match after execution and Shift+Enter to the newer', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([
      { id: 1, text: 'old target', buffer: 'main' },
      { id: 2, text: 'new target', buffer: 'main' },
    ]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('2/2');

    pressSearchKey('Enter');
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/2');

    pressSearchKey('Enter', true);
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('2/2');
  });

  it('marks results stale when the query is edited and re-executes on Enter', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');
    expect(document.querySelector('.pane-search-count')?.classList.contains('stale')).toBe(false);
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');

    setSearchQuery('hello');
    expect(document.querySelector('.pane-search-count')?.classList.contains('stale')).toBe(true);
    expect(document.querySelector('.pane-scrollback .buffer-search-current')?.textContent).toBe('target');

    pressSearchKey('Enter');
    expect(document.querySelector('.pane-search-count')?.classList.contains('stale')).toBe(false);
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.pane-scrollback .buffer-search-current')?.textContent).toBe('hello');
  });

  it('opening the search UI alone does not open the region', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();

    openSearch();

    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector<HTMLInputElement>('.pane-search-input')).not.toBeNull();
  });

  it('a query with no results does not open the region', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello world', buffer: 'main' }]);

    openSearch();
    setSearchQuery('missing');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');
  });

  it('a matching query on a non-main buffer opens the region too', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.setAvailableBuffers(['combat']);
    renderer.appendEntries([{ id: 1, text: 'combat target', buffer: 'combat' }]);

    const select = document.querySelector<HTMLSelectElement>('.pane-select');
    if (!select) throw new Error('Missing select');
    select.value = 'combat';
    select.dispatchEvent(new Event('change'));

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-scrollback')).not.toBeNull();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
  });

  it('changing a successful search to no results leaves the region open', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');
    expect(document.querySelector('.pane-scrollback')).not.toBeNull();

    setSearchQuery('missing');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-scrollback')).not.toBeNull();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('0/0');
    expect(document.querySelector('.buffer-search-match')).toBeNull();
  });

  it('closing search closes a search-opened region', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');
    expect(document.querySelector('.pane-scrollback')).not.toBeNull();

    document.querySelector<HTMLButtonElement>('.pane-search-close')?.click();

    expect(document.querySelector('.pane-scrollback')).toBeNull();
    expect(document.querySelector<HTMLElement>('.pane-search-controls')?.hidden).toBe(true);
  });

  it('closing search keeps a manually opened region and clears highlights', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    toggleRegion();
    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');
    expect(document.querySelector('.buffer-search-match')).not.toBeNull();

    document.querySelector<HTMLButtonElement>('.pane-search-close')?.click();

    expect(document.querySelector('.pane-scrollback')).not.toBeNull();
    expect(document.querySelector('.buffer-search-match')).toBeNull();
  });

  it('Escape closes search', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Escape');

    expect(document.querySelector<HTMLElement>('.pane-search-controls')?.hidden).toBe(true);
  });

  it('marks all matches weakly and the current match strongly', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'target and target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

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

    expect(document.querySelector('.pane-body .output-line')?.textContent).not.toContain('old hidden target');

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');

    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');
    expect(document.querySelector('.buffer-search-current')?.closest('.output-line')?.textContent).toContain('old hidden target');
  });

  it('never highlights the live output and extends totals incrementally on append', () => {
    const renderer = createTestRenderer();
    renderer.loadLayout();
    renderer.appendEntries([{ id: 1, text: 'hello target', buffer: 'main' }]);

    openSearch();
    setSearchQuery('target');
    pressSearchKey('Enter');
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/1');

    const scrollbackLineBefore = document.querySelector<HTMLElement>('.pane-scrollback [data-entry-id="1"]');
    renderer.appendEntries([{ id: 2, text: 'another target', buffer: 'main' }]);

    // Live output never carries search highlighting.
    expect(document.querySelector('.pane-body .buffer-search-match')).toBeNull();
    // Scrollback appended incrementally: existing line node untouched, new match counted.
    expect(document.querySelector<HTMLElement>('.pane-scrollback [data-entry-id="1"]')).toBe(scrollbackLineBefore);
    expect(document.querySelector('.pane-scrollback [data-entry-id="2"] .buffer-search-match')).not.toBeNull();
    expect(document.querySelector('.pane-search-count')?.textContent).toBe('1/2');
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
