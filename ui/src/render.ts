import { AnsiUp } from 'ansi_up';

import { currentAnsiTheme, renderAnsiHtml } from './ansi';
import type { AppElements } from './dom';
import type { Hotkey, LogEntry, ResolvedVariable, Variable, ButtonOverlay } from './types';
import { fetchWithToken } from './dom';

interface RendererState {
  activePanel: 'keyboard' | 'variables' | 'groups' | null;
  restoreInProgress: boolean;
}

interface RendererDeps {
  elements: AppElements;
  ansiUp: AnsiUp;
  fontSizeControls?: HTMLElement;
  sendCommand: (value: string, source: string) => boolean;
  requestVariables: () => void;
  requestGroups: () => void;
  toggleGroup: (name: string, enabled: boolean) => void;
  onButtonRendered?: (btn: ButtonOverlay, el: HTMLButtonElement) => void;
  state: RendererState;
}

const maxRenderedLines = 5000;
const pruneRenderedLines = 500;
const httpsUrlPattern = /https:\/\/[^\s<>"']+/g;
const trailingUrlPunctuation = /[.,;:!?]+$/;
const ansiEscapePattern = /\x1b\[[0-?]*[ -/]*[@-~]/g;

interface PaneNode {
  id: string;
  buffer: string;
  isTemporaryLive?: boolean;
  temporaryLiveFor?: string;
}

interface ColumnNode {
  id: string;
  panes: PaneNode[];
  rowSizes: number[];
}

interface Layout {
  columns: ColumnNode[];
  colSizes: number[];
}

interface SearchState {
  open: boolean;
  query: string;
  current: number;
  total: number;
}

interface RenderedPane {
  node: PaneNode;
  el: HTMLElement;
  outputEl: HTMLElement;
  scrollButtonEl: HTMLButtonElement;
  selectEl?: HTMLSelectElement;
  searchInputEl?: HTMLInputElement;
  searchCountEl?: HTMLElement;
  ansiUp: AnsiUp;
}

export function createRenderer({ elements, ansiUp, fontSizeControls, sendCommand, requestVariables, requestGroups, toggleGroup, onButtonRendered, state }: RendererDeps) {
  let nextId = 0;
  const generateId = (prefix: string) => `${prefix}-${++nextId}`;

  let layout: Layout = {
    columns: [{ id: generateId('col'), panes: [{ id: generateId('pane'), buffer: 'main' }], rowSizes: [100] }],
    colSizes: [100]
  };

  const knownBuffers = new Set<string>(['main']);
  const bufferData = new Map<string, LogEntry[]>();
  const pendingCommandHints = new Map<string, { buffer: string; entry: LogEntry; source: string; commandIndex: number }>();
  const renderedPanes = new Map<string, RenderedPane>();
  const searchStates = new Map<string, SearchState>();

  function getBufferData(name: string): LogEntry[] {
    if (!bufferData.has(name)) {
      bufferData.set(name, []);
    }
    return bufferData.get(name)!;
  }

  function registerBuffer(name: string) {
    if (!knownBuffers.has(name)) {
      knownBuffers.add(name);
      updateAllSelects();
    }
  }

  function addCurrentPaneBuffers(target: Set<string>) {
    for (const col of layout.columns) {
      for (const pane of col.panes) {
        if (pane.buffer) {
          target.add(pane.buffer);
        }
      }
    }
  }

  function setAvailableBuffers(names: string[]) {
    knownBuffers.clear();
    knownBuffers.add('main');
    for (const name of names) {
      if (name) {
        knownBuffers.add(name);
      }
    }
    addCurrentPaneBuffers(knownBuffers);
    updateAllSelects();
  }

  function persistentLayoutSnapshot(): Layout {
    return {
      columns: layout.columns.map((col) => {
        const panes: PaneNode[] = [];
        const rowSizes: number[] = [];

        col.panes.forEach((pane, idx) => {
          const size = col.rowSizes[idx] ?? 0;
          if (pane.isTemporaryLive) {
            const sourceIdx = panes.findIndex((candidate) => candidate.id === pane.temporaryLiveFor);
            if (sourceIdx >= 0) {
              rowSizes[sourceIdx] = (rowSizes[sourceIdx] ?? 0) + size;
            }
            return;
          }

          panes.push({ id: pane.id, buffer: pane.buffer });
          rowSizes.push(size);
        });

        if (panes.length === 0) {
          return { id: col.id, panes: [{ id: generateId('pane'), buffer: 'main' }], rowSizes: [100] };
        }

        return { id: col.id, panes, rowSizes };
      }),
      colSizes: [...layout.colSizes],
    };
  }

  function saveLayout() {
    localStorage.setItem('pane-layout', JSON.stringify(persistentLayoutSnapshot()));
  }

  function loadLayout() {
    try {
      const stored = localStorage.getItem('pane-layout');
      if (stored) {
        layout = JSON.parse(stored);
      }
    } catch (err) {
      console.warn('Failed to load layout', err);
    }
    rebuildDOM();
  }

  function rebuildDOM() {
    elements.panesContainer.innerHTML = '';
    renderedPanes.clear();

    layout.columns.forEach((col, colIdx) => {
      const colEl = document.createElement('div');
      colEl.className = 'column';
      colEl.style.flexBasis = `${layout.colSizes[colIdx]}%`;

      col.panes.forEach((pane, rowIdx) => {
        const paneEl = createPaneDOM(pane);
        paneEl.style.flexBasis = `${col.rowSizes[rowIdx]}%`;
        colEl.appendChild(paneEl);

        if (rowIdx < col.panes.length - 1) {
          colEl.appendChild(makeDragHandle('row', col, rowIdx));
        }
      });

      elements.panesContainer.appendChild(colEl);

      if (colIdx < layout.columns.length - 1) {
        elements.panesContainer.appendChild(makeDragHandle('col', layout, colIdx));
      }
    });

    updateAllSelects();
  }

  function createPaneDOM(node: PaneNode): HTMLElement {
    const el = document.createElement('div');
    el.className = 'pane';
    let selectEl: HTMLSelectElement | undefined;
    let searchInputEl: HTMLInputElement | undefined;
    let searchCountEl: HTMLElement | undefined;

    if (!node.isTemporaryLive) {
      const headerEl = document.createElement('div');
      headerEl.className = 'pane-header';

      selectEl = document.createElement('select');
      selectEl.className = 'pane-select';
      selectEl.addEventListener('change', () => {
        node.buffer = selectEl!.value;
        const collapsed = collapseTemporaryLiveSplit(node.id, 'source');
        saveLayout();
        updateAllSelects();
        renderPaneBuffer(node.id);
        setTimeout(() => scrollPaneToBottom(node.id), 0);
        if (collapsed) return;
      });
      headerEl.appendChild(selectEl);

      const liveSplitBtn = document.createElement('button');
      liveSplitBtn.className = 'pane-btn pane-live-split-btn';
      liveSplitBtn.type = 'button';
      liveSplitBtn.textContent = '⇵';
      liveSplitBtn.title = 'Toggle live split';
      liveSplitBtn.setAttribute('aria-label', 'Toggle live split');
      liveSplitBtn.dataset['paneId'] = node.id;
      const hasLiveSplit = hasTemporaryLiveSplit(node.id);
      if (hasLiveSplit) {
        liveSplitBtn.classList.add('active');
      }
      liveSplitBtn.addEventListener('click', () => toggleTemporaryLiveSplit(node.id));
      headerEl.appendChild(liveSplitBtn);

      const searchButton = document.createElement('button');
      searchButton.className = 'pane-btn pane-search-toggle';
      searchButton.type = 'button';
      searchButton.textContent = '⌕';
      searchButton.title = 'Search this buffer';
      searchButton.setAttribute('aria-label', 'Search this buffer');
      headerEl.appendChild(searchButton);

      const searchControls = document.createElement('div');
      searchControls.className = 'pane-search-controls';
      searchInputEl = document.createElement('input');
      searchInputEl.className = 'pane-search-input';
      searchInputEl.type = 'search';
      searchInputEl.placeholder = 'Search buffer';
      const olderBtn = document.createElement('button');
      olderBtn.className = 'pane-btn pane-search-prev';
      olderBtn.type = 'button';
      olderBtn.textContent = '↑';
      olderBtn.title = 'Older match';
      const newerBtn = document.createElement('button');
      newerBtn.className = 'pane-btn pane-search-next';
      newerBtn.type = 'button';
      newerBtn.textContent = '↓';
      newerBtn.title = 'Newer match';
      searchCountEl = document.createElement('span');
      searchCountEl.className = 'pane-search-count';
      const closeSearchBtn = document.createElement('button');
      closeSearchBtn.className = 'pane-btn pane-search-close';
      closeSearchBtn.type = 'button';
      closeSearchBtn.textContent = '×';
      closeSearchBtn.title = 'Close search';
      searchControls.appendChild(searchInputEl);
      searchControls.appendChild(olderBtn);
      searchControls.appendChild(newerBtn);
      searchControls.appendChild(searchCountEl);
      searchControls.appendChild(closeSearchBtn);
      headerEl.appendChild(searchControls);

      const searchState = searchStates.get(node.id);
      searchInputEl.value = searchState?.query || '';
      searchControls.hidden = !searchState?.open;
      searchButton.classList.toggle('active', !!searchState?.open);
      searchButton.addEventListener('click', () => openPaneSearch(node.id));
      searchInputEl.addEventListener('input', () => updatePaneSearchQuery(node.id, searchInputEl!.value));
      olderBtn.addEventListener('click', () => navigatePaneSearch(node.id, -1));
      newerBtn.addEventListener('click', () => navigatePaneSearch(node.id, 1));
      closeSearchBtn.addEventListener('click', () => closePaneSearch(node.id));

      const actionsEl = document.createElement('div');
      actionsEl.className = 'pane-actions';

      const isTopLeftPane = layout.columns[0]?.panes[0]?.id === node.id;
      if (fontSizeControls && isTopLeftPane) {
        actionsEl.appendChild(fontSizeControls);
      }

      // Dropdown for split/close
      const menuBtn = document.createElement('button');
      menuBtn.className = 'pane-btn';
      menuBtn.innerHTML = '⋮';
      
      const menuEl = document.createElement('div');
      menuEl.className = 'dropdown-menu';
      menuEl.style.display = 'none';

      const splitDownBtn = document.createElement('button');
      splitDownBtn.className = 'dropdown-item';
      splitDownBtn.textContent = 'Split Down';
      splitDownBtn.disabled = hasLiveSplit;
      splitDownBtn.addEventListener('click', () => {
        if (hasTemporaryLiveSplit(node.id)) return;
        menuEl.style.display = 'none';
        addPaneBelow(node.id);
      });

      const splitRightBtn = document.createElement('button');
      splitRightBtn.className = 'dropdown-item';
      splitRightBtn.textContent = 'Split Right';
      splitRightBtn.disabled = hasLiveSplit;
      splitRightBtn.addEventListener('click', () => {
        if (hasTemporaryLiveSplit(node.id)) return;
        menuEl.style.display = 'none';
        addColumnRight(node.id);
      });

      const closeBtn = document.createElement('button');
      closeBtn.className = 'dropdown-item danger';
      closeBtn.textContent = 'Close';
      closeBtn.disabled = hasLiveSplit;
      closeBtn.addEventListener('click', () => {
        if (hasTemporaryLiveSplit(node.id)) return;
        menuEl.style.display = 'none';
        removePane(node.id);
      });

      menuEl.appendChild(splitDownBtn);
      menuEl.appendChild(splitRightBtn);
      menuEl.appendChild(closeBtn);

      menuBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        const isVisible = menuEl.style.display === 'block';
        document.querySelectorAll('.dropdown-menu').forEach((el: any) => el.style.display = 'none');
        menuEl.style.display = isVisible ? 'none' : 'block';
      });

      // Close menu on outside click
      document.addEventListener('click', () => {
        menuEl.style.display = 'none';
      });

      // We only show Close if it's not the absolutely last pane
      const totalPanes = layout.columns.reduce((sum, col) => sum + col.panes.length, 0);
      if (totalPanes <= 1) {
        closeBtn.style.display = 'none';
      }

      const menuWrapper = document.createElement('div');
      menuWrapper.style.position = 'relative';
      menuWrapper.appendChild(menuBtn);
      menuWrapper.appendChild(menuEl);
      actionsEl.appendChild(menuWrapper);
      headerEl.appendChild(actionsEl);
      el.appendChild(headerEl);
    }

    const bodyEl = document.createElement('div');
    bodyEl.className = 'pane-body';

    const outputEl = document.createElement('div');
    outputEl.className = 'pane-output';
    outputEl.id = `output-${node.id}`;

    const scrollButtonEl = document.createElement('button');
    scrollButtonEl.className = 'pane-scroll-bottom';
    scrollButtonEl.type = 'button';
    scrollButtonEl.textContent = '↓';
    scrollButtonEl.title = 'Scroll to bottom';
    scrollButtonEl.hidden = true;
    scrollButtonEl.addEventListener('click', () => {
      outputEl.scrollTop = outputEl.scrollHeight;
      updateScrollButtonVisibility(renderedPanes.get(node.id));
      elements.input.focus();
    });
    outputEl.addEventListener('scroll', () => {
      updateScrollButtonVisibility(renderedPanes.get(node.id));
    });

    bodyEl.appendChild(outputEl);
    bodyEl.appendChild(scrollButtonEl);
    el.appendChild(bodyEl);

    const paneAnsiUp = new AnsiUp();
    paneAnsiUp.use_classes = true;

    if (node.isTemporaryLive) {
      el.classList.add('pane_live-temporary');
      outputEl.classList.add('pane-output_live-temporary');
    }

    renderedPanes.set(node.id, { node, el, outputEl, scrollButtonEl, selectEl, searchInputEl, searchCountEl, ansiUp: paneAnsiUp });

    // Initial render
    // Defer slight to ensure it's in DOM
    setTimeout(() => renderPaneBuffer(node.id), 0);

    return el;
  }

  function addColumnRight(sourcePaneId?: string) {
    let colIdx = layout.columns.length - 1;
    if (sourcePaneId) {
      colIdx = layout.columns.findIndex(c => c.panes.some(p => p.id === sourcePaneId));
      if (colIdx === -1) colIdx = layout.columns.length - 1;
    }

    const oldColSize = layout.colSizes[colIdx]!;
    layout.colSizes[colIdx] = oldColSize / 2;

    const newCol: ColumnNode = {
      id: generateId('col'),
      panes: [{ id: generateId('pane'), buffer: 'main' }],
      rowSizes: [100]
    };

    layout.columns.splice(colIdx + 1, 0, newCol);
    layout.colSizes.splice(colIdx + 1, 0, oldColSize / 2);

    saveLayout();
    rebuildDOM();
  }

  function findPanePosition(paneId: string): { col: ColumnNode; colIdx: number; paneIdx: number } | null {
    const colIdx = layout.columns.findIndex(c => c.panes.some(p => p.id === paneId));
    if (colIdx === -1) return null;
    const col = layout.columns[colIdx]!;
    const paneIdx = col.panes.findIndex(p => p.id === paneId);
    return { col, colIdx, paneIdx };
  }

  function liveSplitRatioKey(buffer: string): string {
    return `liveSplitRatio:v1:${buffer || 'main'}`;
  }

  function readLiveSplitRatio(buffer: string): number {
    const raw = localStorage.getItem(liveSplitRatioKey(buffer));
    const parsed = raw ? Number(raw) : NaN;
    if (!Number.isFinite(parsed)) return 50;
    return Math.min(90, Math.max(10, parsed));
  }

  function saveLiveSplitRatio(buffer: string, liveRatio: number) {
    const clamped = Math.min(90, Math.max(10, liveRatio));
    localStorage.setItem(liveSplitRatioKey(buffer), String(clamped));
  }

  function persistLiveSplitRatioForRow(col: ColumnNode, index: number) {
    const first = col.panes[index];
    const second = col.panes[index + 1];
    if (!first || !second) return;

    let source: PaneNode | undefined;
    let live: PaneNode | undefined;
    let liveSize = 0;
    const firstSize = col.rowSizes[index] ?? 0;
    const secondSize = col.rowSizes[index + 1] ?? 0;

    if (second.isTemporaryLive && second.temporaryLiveFor === first.id) {
      source = first;
      live = second;
      liveSize = secondSize;
    } else if (first.isTemporaryLive && first.temporaryLiveFor === second.id) {
      source = second;
      live = first;
      liveSize = firstSize;
    }

    if (!source || !live) return;
    const total = firstSize + secondSize;
    if (total <= 0) return;
    saveLiveSplitRatio(source.buffer, (liveSize / total) * 100);
  }

  function hasTemporaryLiveSplit(sourcePaneId: string): boolean {
    return layout.columns.some(col => col.panes.some(pane => pane.isTemporaryLive && pane.temporaryLiveFor === sourcePaneId));
  }

  function findTemporaryLivePane(sourcePaneId: string): { col: ColumnNode; pane: PaneNode; paneIdx: number } | null {
    for (const col of layout.columns) {
      const paneIdx = col.panes.findIndex(pane => pane.isTemporaryLive && pane.temporaryLiveFor === sourcePaneId);
      if (paneIdx !== -1) {
        return { col, pane: col.panes[paneIdx]!, paneIdx };
      }
    }
    return null;
  }

  function collapseTemporaryLiveSplit(sourcePaneId: string, survivor: 'source' | 'live'): boolean {
    const temporaryLive = findTemporaryLivePane(sourcePaneId);
    if (!temporaryLive) return false;

    const col = temporaryLive.col;
    const livePaneID = temporaryLive.pane.id;
    const sourceIdx = col.panes.findIndex(pane => pane.id === sourcePaneId);
    const liveIdx = col.panes.findIndex(pane => pane.id === livePaneID);
    if (liveIdx === -1) return false;

    const sourceSize = sourceIdx !== -1 ? col.rowSizes[sourceIdx] ?? 0 : 0;
    const liveSize = col.rowSizes[liveIdx] ?? 0;
    const combinedSize = sourceSize + liveSize || 100;

    if (survivor === 'source' && sourceIdx !== -1) {
      col.panes.splice(liveIdx, 1);
      col.rowSizes.splice(liveIdx, 1);
      const adjustedSourceIdx = col.panes.findIndex(pane => pane.id === sourcePaneId);
      if (adjustedSourceIdx !== -1) {
        col.rowSizes[adjustedSourceIdx] = combinedSize;
      }
      saveLayout();
      rebuildDOM();
      return true;
    }

    if (sourceIdx !== -1) {
      col.panes.splice(sourceIdx, 1);
      col.rowSizes.splice(sourceIdx, 1);
    }

    const adjustedLiveIdx = col.panes.findIndex(pane => pane.id === livePaneID);
    if (adjustedLiveIdx !== -1) {
      const livePane = col.panes[adjustedLiveIdx]!;
      delete livePane.isTemporaryLive;
      delete livePane.temporaryLiveFor;
      col.rowSizes[adjustedLiveIdx] = combinedSize;
    }

    saveLayout();
    rebuildDOM();
    setTimeout(() => scrollPaneToBottom(livePaneID), 0);
    return true;
  }

  function toggleTemporaryLiveSplit(sourcePaneId: string) {
    const pos = findPanePosition(sourcePaneId);
    if (!pos) return;
    const sourcePane = pos.col.panes[pos.paneIdx]!;
    if (sourcePane.isTemporaryLive) return;

    if (collapseTemporaryLiveSplit(sourcePaneId, 'live')) {
      return;
    }

    const oldSize = pos.col.rowSizes[pos.paneIdx]!;
    const liveRatio = readLiveSplitRatio(sourcePane.buffer);
    pos.col.rowSizes[pos.paneIdx] = oldSize * ((100 - liveRatio) / 100);
    const livePane: PaneNode = {
      id: generateId('pane'),
      buffer: sourcePane.buffer,
      isTemporaryLive: true,
      temporaryLiveFor: sourcePaneId,
    };
    pos.col.panes.splice(pos.paneIdx + 1, 0, livePane);
    pos.col.rowSizes.splice(pos.paneIdx + 1, 0, oldSize * (liveRatio / 100));

    rebuildDOM();
    setTimeout(() => scrollPaneToBottom(livePane.id), 0);
  }

  function addPaneBelow(sourcePaneId: string) {
    const col = layout.columns.find(c => c.panes.some(p => p.id === sourcePaneId));
    if (!col) return;

    const paneIdx = col.panes.findIndex(p => p.id === sourcePaneId);
    const oldSize = col.rowSizes[paneIdx]!;
    col.rowSizes[paneIdx] = oldSize / 2;

    const newPane: PaneNode = { id: generateId('pane'), buffer: 'main' };
    col.panes.splice(paneIdx + 1, 0, newPane);
    col.rowSizes.splice(paneIdx + 1, 0, oldSize / 2);

    saveLayout();
    rebuildDOM();
  }

  function removePane(paneId: string) {
    const colIdx = layout.columns.findIndex(c => c.panes.some(p => p.id === paneId));
    if (colIdx === -1) return;
    const col = layout.columns[colIdx]!;

    const paneIdx = col.panes.findIndex(p => p.id === paneId);
    const pane = col.panes[paneIdx]!;

    if (pane.isTemporaryLive && pane.temporaryLiveFor) {
      collapseTemporaryLiveSplit(pane.temporaryLiveFor, 'source');
      return;
    }

    if (collapseTemporaryLiveSplit(paneId, 'live')) {
      return;
    }

    // Distribute size to the previous pane (or next if first)
    const sizeToDistribute = col.rowSizes[paneIdx]!;
    col.panes.splice(paneIdx, 1);
    col.rowSizes.splice(paneIdx, 1);

    if (col.panes.length > 0) {
      const targetIdx = Math.max(0, paneIdx - 1);
      col.rowSizes[targetIdx]! += sizeToDistribute;
    } else {
      // Column is empty, remove column
      const colSizeToDistribute = layout.colSizes[colIdx]!;
      layout.columns.splice(colIdx, 1);
      layout.colSizes.splice(colIdx, 1);
      const targetColIdx = Math.max(0, colIdx - 1);
      if (layout.columns.length > 0) {
        layout.colSizes[targetColIdx]! += colSizeToDistribute;
      } else {
        // Fallback if we accidentally removed the last column
        layout = {
          columns: [{ id: generateId('col'), panes: [{ id: generateId('pane'), buffer: 'main' }], rowSizes: [100] }],
          colSizes: [100]
        };
      }
    }

    saveLayout();
    rebuildDOM();
  }

  function makeDragHandle(kind: 'col' | 'row', parent: Layout | ColumnNode, index: number): HTMLElement {
    const handle = document.createElement('div');
    handle.className = kind === 'col' ? 'col-resize-handle' : 'row-resize-handle';
    
    handle.addEventListener('pointerdown', (e) => {
      handle.setPointerCapture(e.pointerId);
      const sizes = kind === 'col' ? (parent as Layout).colSizes : (parent as ColumnNode).rowSizes;
      
      const startSizeA = sizes[index]!;
      const startSizeB = sizes[index + 1]!;
      const totalPixels = kind === 'col' ? elements.panesContainer.clientWidth : (handle.parentElement!.clientHeight);
      const startPos = kind === 'col' ? e.clientX : e.clientY;

      const onMove = (moveEvent: PointerEvent) => {
        const currentPos = kind === 'col' ? moveEvent.clientX : moveEvent.clientY;
        const deltaPixels = currentPos - startPos;
        const deltaPercent = (deltaPixels / totalPixels) * 100;

        let newA = startSizeA + deltaPercent;
        let newB = startSizeB - deltaPercent;

        if (newA < 10) {
          const diff = 10 - newA;
          newA = 10;
          newB -= diff;
        } else if (newB < 10) {
          const diff = 10 - newB;
          newB = 10;
          newA -= diff;
        }

        sizes[index] = newA;
        sizes[index + 1] = newB;

        // Apply immediately
        if (kind === 'col') {
          const l = parent as Layout;
          (elements.panesContainer.children[index * 2] as HTMLElement).style.flexBasis = `${newA}%`;
          (elements.panesContainer.children[(index + 1) * 2] as HTMLElement).style.flexBasis = `${newB}%`;
        } else {
          const c = parent as ColumnNode;
          (handle.parentElement!.children[index * 2] as HTMLElement).style.flexBasis = `${newA}%`;
          (handle.parentElement!.children[(index + 1) * 2] as HTMLElement).style.flexBasis = `${newB}%`;
        }
      };

      const onUp = () => {
        handle.releasePointerCapture(e.pointerId);
        handle.removeEventListener('pointermove', onMove);
        handle.removeEventListener('pointerup', onUp);
        if (kind === 'row') {
          persistLiveSplitRatioForRow(parent as ColumnNode, index);
        }
        saveLayout();
      };

      handle.addEventListener('pointermove', onMove);
      handle.addEventListener('pointerup', onUp);
    });

    return handle;
  }

  function updateSelectOptions(select: HTMLSelectElement, selectedValue: string) {
    select.innerHTML = '';
    const options = new Set(knownBuffers);
    if (selectedValue) {
      options.add(selectedValue);
    }
    const sortedBuffers = Array.from(options).sort();
    for (const buf of sortedBuffers) {
      const opt = document.createElement('option');
      opt.value = buf;
      opt.textContent = buf;
      if (buf === selectedValue) {
        opt.selected = true;
      }
      select.appendChild(opt);
    }
  }

  function updateAllSelects() {
    renderedPanes.forEach(pane => {
      if (pane.selectEl) {
        updateSelectOptions(pane.selectEl, pane.node.buffer);
      }
    });
  }

  function plainOutputText(text: string): string {
    return text.replace(ansiEscapePattern, '');
  }

  function normalizedSearchText(text: string): string {
    return text.toLocaleLowerCase();
  }

  function countQueryMatches(text: string, query: string): number {
    if (!query) return 0;
    const haystack = normalizedSearchText(text);
    const needle = normalizedSearchText(query);
    if (!needle) return 0;
    let count = 0;
    let index = haystack.indexOf(needle);
    while (index !== -1) {
      count++;
      index = haystack.indexOf(needle, index + needle.length);
    }
    return count;
  }

  function entryMatchCounts(data: LogEntry[], query: string): number[] {
    return data.map(entry => countQueryMatches(plainOutputText(entry.text || ''), query));
  }

  function ensureSearchState(paneId: string): SearchState {
    const existing = searchStates.get(paneId);
    if (existing) return existing;
    const state: SearchState = { open: false, query: '', current: 0, total: 0 };
    searchStates.set(paneId, state);
    return state;
  }

  function refreshSearchStateForPane(pane: RenderedPane, counts?: number[]) {
    const search = searchStates.get(pane.node.id);
    if (!search) return;
    const data = getBufferData(pane.node.buffer);
    const matchCounts = counts || entryMatchCounts(data, search.query.trim());
    search.total = matchCounts.reduce((sum, count) => sum + count, 0);
    if (!search.query.trim() || search.total === 0) {
      search.current = 0;
    } else if (search.current < 1 || search.current > search.total) {
      search.current = search.total;
    }
    updateSearchControls(pane);
  }

  function updateSearchControls(pane: RenderedPane) {
    const search = searchStates.get(pane.node.id);
    if (!pane.searchInputEl || !pane.searchCountEl || !search) return;
    pane.searchInputEl.value = search.query;
    const controls = pane.searchInputEl.closest<HTMLElement>('.pane-search-controls');
    const toggle = pane.el.querySelector<HTMLElement>('.pane-search-toggle');
    if (controls) controls.hidden = !search.open;
    toggle?.classList.toggle('active', search.open);
    pane.searchCountEl.textContent = search.query.trim() ? `${search.current}/${search.total}` : '0/0';
  }

  function maybeAutoEnableSearchSplit(paneId: string) {
    const pane = renderedPanes.get(paneId);
    const search = searchStates.get(paneId);
    if (!pane || !search) return;
    if (pane.node.buffer !== 'main') return;
    if (!search.query.trim()) return;
    if (search.total <= 0) return;
    if (hasTemporaryLiveSplit(paneId)) return;

    setTimeout(() => {
      const currentPane = renderedPanes.get(paneId);
      const currentSearch = searchStates.get(paneId);
      if (!currentPane || !currentSearch) return;
      if (currentPane.node.buffer !== 'main') return;
      if (!currentSearch.query.trim() || currentSearch.total <= 0) return;
      if (hasTemporaryLiveSplit(paneId)) return;

      toggleTemporaryLiveSplit(paneId);
      setTimeout(() => {
        const rebuilt = renderedPanes.get(paneId);
        if (rebuilt) {
          refreshSearchStateForPane(rebuilt);
          rebuilt.searchInputEl?.focus();
          rebuilt.searchInputEl?.setSelectionRange(rebuilt.searchInputEl.value.length, rebuilt.searchInputEl.value.length);
        }
      }, 0);
    }, 0);
  }

  function openPaneSearch(paneId: string) {
    const pane = renderedPanes.get(paneId);
    if (!pane || pane.node.isTemporaryLive) return;
    const search = ensureSearchState(paneId);
    search.open = true;
    refreshSearchStateForPane(pane);
    updateSearchControls(pane);
    pane.searchInputEl?.focus();
  }

  function closePaneSearch(paneId: string) {
    const search = ensureSearchState(paneId);
    search.open = false;
    const pane = renderedPanes.get(paneId);
    if (pane) {
      updateSearchControls(pane);
      renderPaneBuffer(paneId);
    }
  }

  function updatePaneSearchQuery(paneId: string, query: string) {
    const search = ensureSearchState(paneId);
    search.open = true;
    search.query = query;
    search.current = 0;
    renderPaneBuffer(paneId);
    maybeAutoEnableSearchSplit(paneId);
  }

  function navigatePaneSearch(paneId: string, delta: -1 | 1) {
    const pane = renderedPanes.get(paneId);
    const search = ensureSearchState(paneId);
    if (!pane || search.total === 0) return;
    search.current = ((search.current - 1 + delta + search.total) % search.total) + 1;
    renderPaneBuffer(paneId);
  }

  function highlightSearchMatches(root: HTMLElement, query: string, currentOrdinal: number, ordinalStart: number): number {
    const needle = normalizedSearchText(query);
    if (!needle) return 0;

    const textNodes: Array<{ node: Text; start: number; end: number }> = [];
    let fullText = '';
    const collect = (node: Node) => {
      node.childNodes.forEach((child) => {
        if (child.nodeType === Node.TEXT_NODE) {
          const text = child.textContent || '';
          const start = fullText.length;
          fullText += text;
          textNodes.push({ node: child as Text, start, end: fullText.length });
        } else if (child.nodeType === Node.ELEMENT_NODE) {
          collect(child);
        }
      });
    };
    collect(root);

    const normalized = normalizedSearchText(fullText);
    const ranges: Array<{ start: number; end: number; ordinal: number }> = [];
    let ordinal = ordinalStart;
    let index = normalized.indexOf(needle);
    while (index !== -1) {
      ordinal++;
      ranges.push({ start: index, end: index + query.length, ordinal });
      index = normalized.indexOf(needle, index + needle.length);
    }
    if (ranges.length === 0) return 0;

    textNodes.forEach(({ node, start, end }) => {
      const overlapping = ranges.filter(range => range.start < end && range.end > start);
      if (overlapping.length === 0) return;

      const text = node.textContent || '';
      const fragment = document.createDocumentFragment();
      let cursor = 0;
      overlapping.forEach((range) => {
        const localStart = Math.max(0, range.start - start);
        const localEnd = Math.min(text.length, range.end - start);
        if (localStart > cursor) {
          fragment.appendChild(document.createTextNode(text.slice(cursor, localStart)));
        }
        const mark = document.createElement('mark');
        mark.className = range.ordinal === currentOrdinal ? 'buffer-search-match buffer-search-current' : 'buffer-search-match';
        mark.textContent = text.slice(localStart, localEnd);
        fragment.appendChild(mark);
        cursor = localEnd;
      });
      if (cursor < text.length) {
        fragment.appendChild(document.createTextNode(text.slice(cursor)));
      }
      node.parentNode?.replaceChild(fragment, node);
    });

    return ranges.length;
  }

  function scrollCurrentSearchMatchIntoView(pane: RenderedPane) {
    const current = pane.outputEl.querySelector<HTMLElement>('.buffer-search-current');
    if (current && typeof current.scrollIntoView === 'function') {
      current.scrollIntoView({ block: 'center' });
    }
  }

  function createEntryDOM(entry: LogEntry, pane: RenderedPane, searchContext?: { query: string; current: number; ordinalStart: number }): HTMLElement {
    const line = document.createElement('div');
    line.className = 'output-line';
    if (entry.id) {
      line.dataset['entryId'] = String(entry.id);
    }

    const span = document.createElement('span');
    span.className = 'output-text';
    span.innerHTML = entry.text ? renderANSI(pane, entry.text) : '&nbsp;';
    linkifyHttpsUrls(span);
    if (searchContext?.query) {
      highlightSearchMatches(span, searchContext.query, searchContext.current, searchContext.ordinalStart);
    }
    line.appendChild(span);

    const assignedPendingCommandIDs = new Set<string>();
    (entry.commands || []).forEach((command) => {
      line.appendChild(createCommandHintElement(command, pendingCommandIDFor(entry, command, assignedPendingCommandIDs)));
    });

    if (entry.buttons && entry.buttons.length > 0) {
      const wrapper = document.createElement('span');
      wrapper.className = 'trigger-buttons';

      entry.buttons.forEach((btn) => {
        const button = document.createElement('button');
        button.className = 'trigger-button';
        button.type = 'button';
        button.textContent = btn.label;
        button.addEventListener('click', () => {
          sendCommand(btn.command, 'button');
          scrollOutputToBottom();
        });

        if (onButtonRendered) {
          onButtonRendered(btn, button);
        }

        wrapper.appendChild(button);
      });

      line.appendChild(wrapper);
    }
    
    return line;
  }

  function scrollPaneToBottom(paneId: string) {
    const pane = renderedPanes.get(paneId);
    if (!pane) return;
    pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
    updateScrollButtonVisibility(pane);
  }

  function renderPaneBuffer(paneId: string) {
    const pane = renderedPanes.get(paneId);
    if (!pane) return;
    pane.outputEl.innerHTML = '';
    pane.ansiUp = new AnsiUp();
    pane.ansiUp.use_classes = true;
    const data = getBufferData(pane.node.buffer);
    const search = searchStates.get(pane.node.id);
    const activeQuery = search?.open ? search.query.trim() : '';
    const counts = activeQuery ? entryMatchCounts(data, activeQuery) : [];
    if (search) {
      refreshSearchStateForPane(pane, counts);
    }
    
    const fragment = document.createDocumentFragment();
    const startIdx = activeQuery ? 0 : Math.max(0, data.length - maxRenderedLines);
    let ordinal = counts.slice(0, startIdx).reduce((sum, count) => sum + count, 0);
    for (let i = startIdx; i < data.length; i++) {
      const count = counts[i] || 0;
      fragment.appendChild(createEntryDOM(data[i]!, pane, activeQuery ? { query: activeQuery, current: search?.current || 0, ordinalStart: ordinal } : undefined));
      ordinal += count;
    }
    pane.outputEl.appendChild(fragment);
    if (activeQuery) {
      scrollCurrentSearchMatchIntoView(pane);
    }
    
    if (!activeQuery && (!state.restoreInProgress || pane.node.isTemporaryLive)) {
      pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
    }
    updateScrollButtonVisibility(pane);
  }

  function triggerVisualBell(element: HTMLElement) {
    element.classList.remove('visual-bell');
    // Force animation restart for repeated BEL entries in the same pane.
    void element.offsetWidth;
    element.classList.add('visual-bell');
  }

  function appendEntries(entries: LogEntry[]) {
    if (entries.length === 0) return;

    // Group entries by buffer for efficient processing
    const byBuffer = new Map<string, LogEntry[]>();
    for (const entry of entries) {
      const buf = entry.buffer || 'main';
      registerBuffer(buf);
      if (!byBuffer.has(buf)) byBuffer.set(buf, []);
      byBuffer.get(buf)!.push(entry);
    }

    byBuffer.forEach((bufferEntries, bufferName) => {
      const data = getBufferData(bufferName);
      const seenIDs = new Set(data.map(entry => entry.id).filter((id): id is number => !!id));
      const newEntries = bufferEntries.filter((entry) => {
        if (!entry.id) return true;
        if (seenIDs.has(entry.id)) return false;
        seenIDs.add(entry.id);
        return true;
      });
      if (newEntries.length === 0) return;

      data.push(...newEntries);

      if (data.length > maxRenderedLines + pruneRenderedLines) {
        data.splice(0, pruneRenderedLines);
      }

      renderedPanes.forEach(pane => {
        if (pane.node.buffer === bufferName) {
          const activeSearch = searchStates.get(pane.node.id);
          if (activeSearch?.open && activeSearch.query.trim()) {
            renderPaneBuffer(pane.node.id);
            if (!state.restoreInProgress && newEntries.some(entry => (entry.bell_positions || []).length > 0)) {
              triggerVisualBell(pane.outputEl);
            }
            return;
          }

          const shouldScroll = pane.node.isTemporaryLive || shouldStickToBottom(pane);
          const fragment = document.createDocumentFragment();

          for (const entry of newEntries) {
            fragment.appendChild(createEntryDOM(entry, pane));
          }
          pane.outputEl.appendChild(fragment);
          if (!state.restoreInProgress && newEntries.some(entry => (entry.bell_positions || []).length > 0)) {
            triggerVisualBell(pane.outputEl);
          }

          if (pane.outputEl.children.length > maxRenderedLines) {
            const excess = pane.outputEl.children.length - maxRenderedLines;
            if (excess > 0) {
              const toRemove = Math.max(excess, pruneRenderedLines);
              for (let i = 0; i < toRemove && pane.outputEl.firstChild; i++) {
                pane.outputEl.removeChild(pane.outputEl.firstChild);
              }
            }
          }

          if (shouldScroll && (!state.restoreInProgress || pane.node.isTemporaryLive)) {
            pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
          }

          updateScrollButtonVisibility(pane);
        }
      });
    });
  }

  function latestEntryID(): number {
    let latest = 0;
    bufferData.forEach((entries) => {
      for (const entry of entries) {
        if (entry.id && entry.id > latest) {
          latest = entry.id;
        }
      }
    });
    return latest;
  }

  function appendEntry(entry: LogEntry) {
    appendEntries([entry]);
  }

  function addCommandHint(entryId: number, buffer: string, command: string) {
    // 1. Persist in memory so it survives pane re-renders
    const data = getBufferData(buffer);
    const entry = data.find(e => e.id === entryId);
    if (entry) {
      entry.commands = entry.commands || [];
      entry.commands.push(command);
    }

    // 2. Update existing DOM if present
    for (const pane of renderedPanes.values()) {
      if (pane.node.buffer === buffer) {
        const line = pane.outputEl.querySelector<HTMLElement>(`[data-entry-id="${entryId}"]`);
        if (line) {
          line.appendChild(createCommandHintElement(command));
        }
      }
    }
  }

  function appendCommandHint(command: string, clientCommandID?: string) {
    const buffer = 'main';
    const data = getBufferData(buffer);
    let pendingEntry: LogEntry | null = null;
    let commandIndex = -1;
    if (data.length > 0) {
      const lastEntry = data[data.length - 1]!;
      lastEntry.commands = lastEntry.commands || [];
      lastEntry.commands.push(command);
      commandIndex = lastEntry.commands.length - 1;
      pendingEntry = lastEntry;
    }
    if (clientCommandID && pendingEntry) {
      pendingCommandHints.set(clientCommandID, { buffer, entry: pendingEntry, source: command, commandIndex });
    }

    renderedPanes.forEach(pane => {
      if (pane.node.buffer === buffer) {
        const line = pane.outputEl.lastElementChild;
        if (line) {
          line.appendChild(createCommandHintElement(command, clientCommandID));
        }
      }
    });
  }

  function createCommandHintElement(command: string, clientCommandID?: string): HTMLSpanElement {
    const hint = document.createElement('span');
    hint.className = 'output-hint';
    if (clientCommandID) {
      hint.dataset['clientCommandId'] = clientCommandID;
    }
    hint.textContent = `-> ${command}`;
    return hint;
  }

  function pendingCommandIDFor(entry: LogEntry, command: string, assignedIDs: Set<string>): string | undefined {
    for (const [id, pending] of pendingCommandHints) {
      if (assignedIDs.has(id)) continue;
      if (pending.entry === entry && pending.source === command) {
        assignedIDs.add(id);
        return id;
      }
    }
    return undefined;
  }

  function replacePendingCommandHintInDOM(clientCommandID: string, buffer: string, commands: string[]): boolean {
    let replaced = false;

    renderedPanes.forEach(pane => {
      if (pane.node.buffer !== buffer) {
        return;
      }

      const hints = pane.outputEl.querySelectorAll<HTMLElement>(`[data-client-command-id="${clientCommandID}"]`);
      if (!hints.length) {
        return;
      }

      const shouldScroll = shouldStickToBottom(pane);
      hints.forEach((hint) => {
        const fragment = document.createDocumentFragment();
        commands.forEach((command) => {
          fragment.appendChild(createCommandHintElement(command));
        });
        hint.replaceWith(fragment);
        replaced = true;
      });

      if (shouldScroll && !state.restoreInProgress) {
        pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
      }
      updateScrollButtonVisibility(pane);
    });

    return replaced;
  }

  function resolveCommandTrace(clientCommandID: string, commands: string[]) {
    const pending = pendingCommandHints.get(clientCommandID);
    if (!pending) return;
    pendingCommandHints.delete(clientCommandID);

    pending.entry.commands = pending.entry.commands || [];
    const sourceIndex = pending.entry.commands[pending.commandIndex] === pending.source
      ? pending.commandIndex
      : pending.entry.commands.indexOf(pending.source);
    const canonicalCommands = commands.filter((command) => command.trim() !== '');
    if (sourceIndex >= 0) {
      pending.entry.commands.splice(sourceIndex, 1, ...canonicalCommands);
    } else {
      pending.entry.commands.push(...canonicalCommands);
    }

    if (replacePendingCommandHintInDOM(clientCommandID, pending.buffer, canonicalCommands)) {
      return;
    }

    renderedPanes.forEach(pane => {
      if (pane.node.buffer === pending.buffer) {
        renderPaneBuffer(pane.node.id);
      }
    });
  }

  function clearOutput() {
    bufferData.clear();
    renderedPanes.forEach(pane => {
      pane.outputEl.innerHTML = '';
      updateScrollButtonVisibility(pane);
    });
    updateAllSelects();
  }

  function scrollOutputToBottom() {
    renderedPanes.forEach(pane => {
      pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
      updateScrollButtonVisibility(pane);
    });
  }

  function shouldStickToBottom(pane: RenderedPane): boolean {
    const threshold = 100; // pixels from bottom
    const distanceToBottom = pane.outputEl.scrollHeight - pane.outputEl.scrollTop - pane.outputEl.clientHeight;
    return distanceToBottom <= threshold;
  }

  function updateScrollButtonVisibility(pane: RenderedPane | undefined) {
    if (!pane) {
      return;
    }

    const hasOverflow = pane.outputEl.scrollHeight > pane.outputEl.clientHeight + 8;
    pane.scrollButtonEl.hidden = !hasOverflow || shouldStickToBottom(pane);
  }

  function setActivePanel(panel: 'keyboard' | 'variables' | 'groups' | null) {
    state.activePanel = state.activePanel === panel ? null : panel;

    elements.keyboardToggle.classList.toggle('panel-tab_active', state.activePanel === 'keyboard');
    elements.variablesToggle.classList.toggle('panel-tab_active', state.activePanel === 'variables');
    elements.groupsToggle.classList.toggle('panel-tab_active', state.activePanel === 'groups');
    
    elements.keyboardPanel.classList.toggle('bottom-panel_active', state.activePanel === 'keyboard');
    elements.variablesPanel.classList.toggle('bottom-panel_active', state.activePanel === 'variables');
    elements.groupsPanel.classList.toggle('bottom-panel_active', state.activePanel === 'groups');
    
    elements.bottomPanels.classList.toggle('bottom-panels_visible', Boolean(state.activePanel));

    if (state.activePanel === 'variables') {
      requestVariables();
    }
    if (state.activePanel === 'groups') {
      requestGroups();
    }
  }

  function updateConnectionStatus(status: string) {
    elements.connectionStatus.textContent = status;
    elements.connectionStatus.classList.toggle('status-connected', status === 'connected');
    elements.connectionStatus.classList.toggle('status-disconnected', status === 'disconnected');
    elements.connectionStatus.classList.toggle('status-actionable', status === 'disconnected');
    elements.connectionStatus.title = status === 'disconnected' ? 'Reconnect' : '';
  }

  type ActiveTimerSnapshot = TimerSnapshot & { received_at_ms: number };

  let activeTimers: ActiveTimerSnapshot[] = [];
  let tickerInterval: any = null;

  function renderTimers(timers: TimerSnapshot[]) {
    const receivedAt = performance.now();
    activeTimers = (timers || []).map((timer) => ({ ...timer, received_at_ms: receivedAt }));
    updateTickerUI();

    if (!tickerInterval && activeTimers.some(t => t.enabled)) {
      tickerInterval = setInterval(updateTickerUI, 1000);
    } else if (tickerInterval && activeTimers.every(t => !t.enabled)) {
      clearInterval(tickerInterval);
      tickerInterval = null;
    }
  }

  function formatRemaining(sec: number): string {
    if (sec >= 60) {
      const m = Math.floor(sec / 60);
      const s = sec % 60;
      return `${m}:${String(s).padStart(2, '0')}`;
    }
    return String(sec);
  }

  function getRemainingSeconds(timer: ActiveTimerSnapshot): number {
    const cycleSeconds = timer.cycle_ms > 0 ? Math.ceil(timer.cycle_ms / 1000) : 0;
    const snapshotRemaining = Number.isFinite(timer.remaining_ms)
      ? Math.max(0, timer.remaining_ms)
      : Math.max(0, new Date(timer.next_tick_at).getTime() - Date.now());
    const elapsed = Math.max(0, performance.now() - timer.received_at_ms);
    const remaining = Math.ceil(Math.max(0, snapshotRemaining - elapsed) / 1000);

    if (remaining === 0 && cycleSeconds > 0 && timer.repeat_mode !== 'one_shot') {
      return cycleSeconds;
    }
    if (cycleSeconds > 0 && remaining > cycleSeconds) {
      return cycleSeconds;
    }
    return remaining;
  }

  function updateTickerUI() {
    const primary = activeTimers.find(t => t.name === 'ticker');
    const secondary = activeTimers.filter(t => t.name !== 'ticker' && t.enabled);

    // Primary ticker
    if (!primary || !primary.enabled) {
      elements.ticker.textContent = 'tick off';
      elements.ticker.classList.remove('ticker_active', 'ticker_low');
    } else {
      const remaining = getRemainingSeconds(primary);

      const icon = primary.icon || '🕒';
      elements.ticker.textContent = `${icon} tick ${formatRemaining(remaining)}`;
      elements.ticker.classList.add('ticker_active');
      if (remaining > 0 && remaining < 5) {
        elements.ticker.classList.add('ticker_low');
      } else {
        elements.ticker.classList.remove('ticker_low');
      }
    }

    // Secondary timers
    elements.secondaryTimers.innerHTML = '';
    secondary.forEach(t => {
      const remaining = getRemainingSeconds(t);

      const pill = document.createElement('span');
      pill.className = 'timer-pill';
      if (remaining > 0 && remaining < 5) {
        pill.classList.add('timer-pill_low');
      }

      if (t.icon) {
        pill.textContent = `${t.icon} ${formatRemaining(remaining)}`;
      } else {
        pill.textContent = `${t.name} ${formatRemaining(remaining)}`;
      }
      pill.title = t.name;
      elements.secondaryTimers.appendChild(pill);
    });
  }

  function createHotkeyChip(item: Hotkey, hideShortcut = false): HTMLElement {
    const chip = document.createElement('span');
    chip.className = 'hotkey-chip';

    const button = document.createElement('button');
    button.type = 'button';
    button.addEventListener('click', () => {
      sendCommand(item.command, 'key');
      scrollOutputToBottom();
    });

    if (!hideShortcut) {
      const key = document.createElement('kbd');
      key.textContent = item.shortcut;
      button.appendChild(key);
    }

    const label = document.createElement('span');
    label.textContent = item.command;
    button.appendChild(label);

    chip.appendChild(button);
    return chip;
  }

  function renderHotkeys(items: Hotkey[]) {
    elements.hotkeysBox.innerHTML = '';
    const hotkeys = items || [];
    const isMobile = window.innerWidth <= 640;

    if (!isMobile) {
      hotkeys.forEach((item) => {
        elements.hotkeysBox.appendChild(createHotkeyChip(item));
      });
    } else {
      const positioned = hotkeys.filter(h => h.mobile_row && h.mobile_row > 0);
      const free = [...hotkeys.filter(h => !h.mobile_row || h.mobile_row <= 0)];

      const rowsMap = new Map<number, HTMLElement>();
      const sortedRowNums = Array.from(new Set(positioned.map(h => h.mobile_row!))).sort((a, b) => a - b);
      
      sortedRowNums.forEach(rowNum => {
        const rowEl = document.createElement('div');
        rowEl.className = 'hotkeys-row';
        rowEl.dataset.row = String(rowNum);
        elements.hotkeysBox.appendChild(rowEl);
        rowsMap.set(rowNum, rowEl);
      });

      // Add positioned hotkeys
      positioned.sort((a, b) => (a.mobile_order || 0) - (b.mobile_order || 0)).forEach(hk => {
        rowsMap.get(hk.mobile_row!)!.appendChild(createHotkeyChip(hk, true));
      });

      // Best-effort auto-fill
      if (free.length > 0) {
        const containerWidth = elements.hotkeysBox.clientWidth || (window.innerWidth - 24);
        const gap = 6;

        free.forEach(hk => {
          const chip = createHotkeyChip(hk, true);
          elements.hotkeysBox.appendChild(chip);
          const chipWidth = chip.offsetWidth || (hk.command.length * 9 + 24);
          chip.remove();

          let placed = false;
          for (const rowNum of sortedRowNums) {
            const rowEl = rowsMap.get(rowNum)!;
            let currentRowWidth = 0;
            for (const child of Array.from(rowEl.children)) {
              currentRowWidth += ((child as HTMLElement).offsetWidth || (child.textContent?.length || 0) * 9 + 24) + gap;
            }

            if (currentRowWidth + chipWidth < containerWidth - 12) {
              rowEl.appendChild(chip);
              placed = true;
              break;
            }
          }

          if (!placed) {
            const freeRows = elements.hotkeysBox.querySelectorAll('.hotkeys-row_free');
            let lastFreeRow = freeRows[freeRows.length - 1] as HTMLElement;
            
            if (lastFreeRow) {
              let currentRowWidth = 0;
              for (const child of Array.from(lastFreeRow.children)) {
                currentRowWidth += ((child as HTMLElement).offsetWidth || (child.textContent?.length || 0) * 9 + 24) + gap;
              }
              if (currentRowWidth + chipWidth < containerWidth - 12) {
                lastFreeRow.appendChild(chip);
                placed = true;
              }
            }
            
            if (!placed) {
              lastFreeRow = document.createElement('div');
              lastFreeRow.className = 'hotkeys-row hotkeys-row_free';
              lastFreeRow.appendChild(chip);
              elements.hotkeysBox.appendChild(lastFreeRow);
            }
          }
        });
      }
    }

    elements.keyboardToggle.style.display = hotkeys.length ? 'inline-flex' : 'none';
    if (!hotkeys.length && state.activePanel === 'keyboard') {
      setActivePanel('keyboard');
    }
  }

  function renderGroups(items: RuleGroup[]) {
    elements.groupsList.innerHTML = '';
    if (!items || !items.length) {
      const empty = document.createElement('div');
      empty.className = 'variables-empty';
      empty.textContent = 'No groups defined in active profiles.';
      elements.groupsList.appendChild(empty);
      return;
    }

    const chipsContainer = document.createElement('div');
    chipsContainer.className = 'group-chips-container';

    // Sort by name for stability
    items.sort((a, b) => a.group_name.localeCompare(b.group_name));

    items.forEach(g => {
      const chip = document.createElement('label');
      chip.className = 'group-chip';
      
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.className = 'group-checkbox';
      const allEnabled = g.enabled_count === g.total_count;
      const noneEnabled = g.enabled_count === 0;
      checkbox.checked = allEnabled;
      checkbox.indeterminate = !allEnabled && !noneEnabled;
      checkbox.addEventListener('change', () => {
        toggleGroup(g.group_name, checkbox.checked);
      });

      const label = document.createElement('span');
      label.className = 'group-label';
      label.textContent = g.group_name;

      const count = document.createElement('span');
      count.className = 'group-badge';
      count.textContent = String(g.total_count);

      chip.appendChild(checkbox);
      chip.appendChild(label);
      chip.appendChild(count);
      chipsContainer.appendChild(chip);
    });

    elements.groupsList.appendChild(chipsContainer);
  }

  function renderVariables(items: (Variable | ResolvedVariable)[]) {
    const focused = document.activeElement as HTMLInputElement | null;
    const focusedKey = focused?.dataset['varKey'] ?? null;
    const focusedValue = focusedKey != null && focused!.value !== focused!.defaultValue ? focused!.value : null;

    elements.variablesList.innerHTML = '';

    if (!items || !items.length) {
      const empty = document.createElement('div');
      empty.className = 'variables-empty';
      empty.textContent = 'No variables defined yet.';
      elements.variablesList.appendChild(empty);
      return;
    }

    const params = new URLSearchParams(window.location.search);
    const sessionID = params.get('session_id');

    items.forEach((item) => {
      const isResolved = 'name' in item;
      const keyStr = isResolved ? (item as ResolvedVariable).name : (item as Variable).key;
      const valStr = item.value || '';
      const usesDefault = isResolved ? (item as ResolvedVariable).uses_default : false;
      const isDeclared = isResolved ? (item as ResolvedVariable).declared : true;

      const row = document.createElement('div');
      row.className = 'variable-row';
      if (usesDefault) row.classList.add('variable-row_default');
      if (!isDeclared) row.classList.add('variable-row_undeclared');

      const key = document.createElement('div');
      key.className = 'variable-key';
      key.textContent = `$${keyStr}`;
      row.appendChild(key);

      const valueContainer = document.createElement('div');
      valueContainer.className = 'variable-value-container';

      const input = document.createElement('input');
      input.type = 'text';
      input.className = 'variable-input';
      input.dataset['varKey'] = keyStr;
      input.value = valStr;
      input.defaultValue = valStr;
      if (usesDefault) {
        input.placeholder = (item as ResolvedVariable).default_value;
        input.value = '';
        input.defaultValue = '';
      }
      if (keyStr === focusedKey && focusedValue !== null) {
        input.value = focusedValue;
      }
      
      input.addEventListener('keydown', async (e) => {
        if (e.key === 'Enter') {
          const newVal = input.value.trim();
          if (!newVal) return;
          if (sessionID) {
            await fetchWithToken(`/api/sessions/${sessionID}/variables`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ key: keyStr, value: newVal })
            });
            requestVariables();
          }
        }
      });
      valueContainer.appendChild(input);

      if (isResolved && (item as ResolvedVariable).has_value) {
        const clearBtn = document.createElement('button');
        clearBtn.className = 'btn-clear';
        clearBtn.innerHTML = '×';
        clearBtn.title = 'Clear override';
        clearBtn.addEventListener('click', async () => {
          if (sessionID) {
            await fetchWithToken(`/api/sessions/${sessionID}/variables/${encodeURIComponent(keyStr)}`, {
              method: 'DELETE'
            });
            requestVariables();
          }
        });
        valueContainer.appendChild(clearBtn);
      }

      row.appendChild(valueContainer);
      elements.variablesList.appendChild(row);
    });

    if (focusedKey != null) {
      const restored = Array.from(elements.variablesList.querySelectorAll<HTMLInputElement>('[data-var-key]'))
        .find((input) => input.dataset['varKey'] === focusedKey);
      if (restored) {
        restored.focus();
        const len = restored.value.length;
        restored.setSelectionRange(len, len);
      }
    }
  }

  function refreshAnsiTheme() {
    renderedPanes.forEach((pane) => renderPaneBuffer(pane.node.id));
  }

  // Expose API
  return {
    appendCommandHint,
    addCommandHint,
    resolveCommandTrace,
    appendEntry,
    appendEntries,
    latestEntryID,
    clearOutput,
    setAvailableBuffers,
    renderHotkeys,
    renderVariables,
    renderGroups,
    renderTimers,
    scrollOutputToBottom,
    setActivePanel,
    updateConnectionStatus,
    loadLayout,
    addColumnRight,
    refreshAnsiTheme,
  };
}
function renderANSI(pane: RenderedPane, text: string): string {
  return renderAnsiHtml(pane.ansiUp, text, currentAnsiTheme());
}

function linkifyHttpsUrls(root: HTMLElement) {
  const textNodes: Text[] = [];
  const collectTextNodes = (node: Node) => {
    node.childNodes.forEach((child) => {
      if (child.nodeType === Node.TEXT_NODE) {
        textNodes.push(child as Text);
      } else if (child.nodeType === Node.ELEMENT_NODE && (child as Element).tagName !== 'A') {
        collectTextNodes(child);
      }
    });
  };
  collectTextNodes(root);

  textNodes.forEach((textNode) => {
    const text = textNode.textContent || '';
    httpsUrlPattern.lastIndex = 0;
    if (!httpsUrlPattern.test(text)) return;

    httpsUrlPattern.lastIndex = 0;
    const fragment = document.createDocumentFragment();
    let lastIndex = 0;
    for (const match of text.matchAll(httpsUrlPattern)) {
      const rawUrl = match[0];
      const start = match.index || 0;
      const url = rawUrl.replace(trailingUrlPunctuation, '');
      const trailing = rawUrl.slice(url.length);
      if (start > lastIndex) {
        fragment.appendChild(document.createTextNode(text.slice(lastIndex, start)));
      }

      const anchor = document.createElement('a');
      anchor.className = 'ansi-bright-cyan-fg';
      anchor.href = url;
      anchor.target = '_blank';
      anchor.rel = 'noopener noreferrer';
      anchor.textContent = url;
      fragment.appendChild(anchor);
      if (trailing) {
        fragment.appendChild(document.createTextNode(trailing));
      }
      lastIndex = start + rawUrl.length;
    }
    if (lastIndex < text.length) {
      fragment.appendChild(document.createTextNode(text.slice(lastIndex)));
    }
    textNode.replaceWith(fragment);
  });
}
