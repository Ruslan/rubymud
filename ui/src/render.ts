import { AnsiUp } from 'ansi_up';

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
  sendCommand: (value: string, source: string) => boolean;
  requestVariables: () => void;
  requestGroups: () => void;
  toggleGroup: (name: string, enabled: boolean) => void;
  onButtonRendered?: (btn: ButtonOverlay, el: HTMLButtonElement) => void;
  state: RendererState;
}

const maxRenderedLines = 5000;
const pruneRenderedLines = 500;

interface PaneNode {
  id: string;
  buffer: string;
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

interface RenderedPane {
  node: PaneNode;
  el: HTMLElement;
  outputEl: HTMLElement;
  selectEl: HTMLSelectElement;
}

export function createRenderer({ elements, ansiUp, sendCommand, requestVariables, requestGroups, toggleGroup, onButtonRendered, state }: RendererDeps) {
  let nextId = 0;
  const generateId = (prefix: string) => `${prefix}-${++nextId}`;

  let layout: Layout = {
    columns: [{ id: generateId('col'), panes: [{ id: generateId('pane'), buffer: 'main' }], rowSizes: [100] }],
    colSizes: [100]
  };

  const knownBuffers = new Set<string>(['main']);
  const bufferData = new Map<string, LogEntry[]>();
  const renderedPanes = new Map<string, RenderedPane>();

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

  function saveLayout() {
    localStorage.setItem('pane-layout', JSON.stringify(layout));
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
    
    const headerEl = document.createElement('div');
    headerEl.className = 'pane-header';

    const selectEl = document.createElement('select');
    selectEl.className = 'pane-select';
    selectEl.addEventListener('change', () => {
      node.buffer = selectEl.value;
      saveLayout();
      renderPaneBuffer(node.id);
    });
    headerEl.appendChild(selectEl);

    const actionsEl = document.createElement('div');
    actionsEl.className = 'pane-actions';

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
    splitDownBtn.addEventListener('click', () => {
      menuEl.style.display = 'none';
      addPaneBelow(node.id);
    });

    const splitRightBtn = document.createElement('button');
    splitRightBtn.className = 'dropdown-item';
    splitRightBtn.textContent = 'Split Right';
    splitRightBtn.addEventListener('click', () => {
      menuEl.style.display = 'none';
      addColumnRight(node.id);
    });

    const closeBtn = document.createElement('button');
    closeBtn.className = 'dropdown-item danger';
    closeBtn.textContent = 'Close';
    closeBtn.addEventListener('click', () => {
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

    const outputEl = document.createElement('div');
    outputEl.className = 'pane-output';
    outputEl.id = `output-${node.id}`;

    el.appendChild(headerEl);
    el.appendChild(outputEl);

    renderedPanes.set(node.id, { node, el, outputEl, selectEl });

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
        saveLayout();
      };

      handle.addEventListener('pointermove', onMove);
      handle.addEventListener('pointerup', onUp);
    });

    return handle;
  }

  function updateSelectOptions(select: HTMLSelectElement, selectedValue: string) {
    select.innerHTML = '';
    const sortedBuffers = Array.from(knownBuffers).sort();
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
      updateSelectOptions(pane.selectEl, pane.node.buffer);
    });
  }

  function createEntryDOM(entry: LogEntry): HTMLElement {
    const line = document.createElement('div');
    line.className = 'output-line';
    if (entry.id) {
      line.dataset['entryId'] = String(entry.id);
    }

    const span = document.createElement('span');
    span.innerHTML = entry.text ? renderANSI(entry.text) : '&nbsp;';
    line.appendChild(span);

    (entry.commands || []).forEach((command) => {
      const hint = document.createElement('span');
      hint.className = 'output-hint';
      hint.textContent = `-> ${command}`;
      line.appendChild(hint);
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
          appendCommandHint(btn.command); // Append to main
          scrollOutputToBottom();
          sendCommand(btn.command, 'button');
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

  function renderPaneBuffer(paneId: string) {
    const pane = renderedPanes.get(paneId);
    if (!pane) return;
    pane.outputEl.innerHTML = '';
    const data = getBufferData(pane.node.buffer);
    
    const fragment = document.createDocumentFragment();
    const startIdx = Math.max(0, data.length - maxRenderedLines);
    for (let i = startIdx; i < data.length; i++) {
      fragment.appendChild(createEntryDOM(data[i]!));
    }
    pane.outputEl.appendChild(fragment);
    
    if (!state.restoreInProgress) {
      pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
    }
  }

  function appendEntry(entry: LogEntry) {
    const bufferName = entry.buffer || 'main';
    registerBuffer(bufferName);
    
    const data = getBufferData(bufferName);
    data.push(entry);
    
    if (data.length > maxRenderedLines + pruneRenderedLines) {
      data.splice(0, pruneRenderedLines);
    }

    renderedPanes.forEach(pane => {
      if (pane.node.buffer === bufferName) {
        const shouldScroll = shouldStickToBottom(pane);
        pane.outputEl.appendChild(createEntryDOM(entry));

        if (pane.outputEl.children.length > maxRenderedLines) {
          for (let i = 0; i < pruneRenderedLines && pane.outputEl.firstChild; i++) {
            pane.outputEl.removeChild(pane.outputEl.firstChild);
          }
        }

        if (shouldScroll && !state.restoreInProgress) {
          pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
        }
      }
    });
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
          const hint = document.createElement('span');
          hint.className = 'output-hint';
          hint.textContent = `-> ${command}`;
          line.appendChild(hint);
        }
      }
    }
  }

  function appendCommandHint(command: string) {
    // Append to the last entry of 'main' buffer
    const data = getBufferData('main');
    if (data.length > 0) {
      const lastEntry = data[data.length - 1]!;
      lastEntry.commands = lastEntry.commands || [];
      lastEntry.commands.push(command);
    }
    
    renderedPanes.forEach(pane => {
      if (pane.node.buffer === 'main') {
        const line = pane.outputEl.lastElementChild;
        if (line) {
          const hint = document.createElement('span');
          hint.className = 'output-hint';
          hint.textContent = `-> ${command}`;
          line.appendChild(hint);
        }
      }
    });
  }

  function clearOutput() {
    bufferData.clear();
    knownBuffers.clear();
    knownBuffers.add('main');
    // Preserve bindings — they will be re-populated as entries arrive in restore_begin.
    renderedPanes.forEach(pane => {
      pane.outputEl.innerHTML = '';
    });
    updateAllSelects();
  }

  function scrollOutputToBottom() {
    renderedPanes.forEach(pane => {
      pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
    });
  }

  function shouldStickToBottom(pane: RenderedPane): boolean {
    const threshold = 100; // pixels from bottom
    const distanceToBottom = pane.outputEl.scrollHeight - pane.outputEl.scrollTop - pane.outputEl.clientHeight;
    return distanceToBottom <= threshold;
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
    elements.bottomToolbarHint.textContent = state.activePanel ? `${state.activePanel} open` : 'Panels';

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
  }

  function renderHotkeys(items: Hotkey[]) {
    elements.hotkeysBox.innerHTML = '';
    (items || []).forEach((item) => {
      const chip = document.createElement('span');
      chip.className = 'hotkey-chip';

      const button = document.createElement('button');
      button.type = 'button';
      button.addEventListener('click', () => {
        appendCommandHint(item.command);
        scrollOutputToBottom();
        sendCommand(item.command, 'key');
      });

      const key = document.createElement('kbd');
      key.textContent = item.shortcut;
      button.appendChild(key);

      const label = document.createElement('span');
      label.textContent = item.command;
      button.appendChild(label);

      chip.appendChild(button);
      elements.hotkeysBox.appendChild(chip);
    });

    elements.keyboardToggle.style.display = items && items.length ? 'inline-flex' : 'none';
    if ((!items || !items.length) && state.activePanel === 'keyboard') {
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
    const focusedValue = focusedKey != null ? focused!.value : null;

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

    if (sessionID) {
      const header = document.createElement('div');
      header.className = 'variables-header';
      header.style.display = 'flex';
      header.style.justifyContent = 'flex-end';
      header.style.marginBottom = '10px';

      const fillBtn = document.createElement('button');
      fillBtn.className = 'btn-small';
      fillBtn.textContent = 'Fill Defaults';
      fillBtn.addEventListener('click', async () => {
        for (const item of (items as ResolvedVariable[])) {
          if (item.declared && item.default_value && !item.has_value) {
            await fetchWithToken(`/api/sessions/${sessionID}/variables`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ key: item.name, value: item.default_value })
            });
          }
        }
        requestVariables();
      });
      header.appendChild(fillBtn);
      elements.variablesList.appendChild(header);
    }

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
      valueContainer.style.flex = '1';
      valueContainer.style.display = 'flex';
      valueContainer.style.gap = '8px';

      const input = document.createElement('input');
      input.type = 'text';
      input.className = 'variable-input';
      input.dataset['varKey'] = keyStr;
      input.value = valStr;
      if (usesDefault) {
        input.placeholder = (item as ResolvedVariable).default_value;
        input.value = '';
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
      const restored = elements.variablesList.querySelector<HTMLInputElement>(`[data-var-key="${CSS.escape(focusedKey)}"]`);
      if (restored) {
        restored.focus();
        const len = restored.value.length;
        restored.setSelectionRange(len, len);
      }
    }
  }

  // Expose API
  return {
    appendCommandHint,
    addCommandHint,
    appendEntry,
    clearOutput,
    renderHotkeys,
    renderVariables,
    renderGroups,
    scrollOutputToBottom,
    setActivePanel,
    updateConnectionStatus,
    loadLayout,
    addColumnRight,
  };
}
  function renderANSI(text: string): string {
    // ansi_up keeps color state between calls, so use a fresh parser per line
    // to prevent color bleed across unrelated entries and buffers.
    const parser = new AnsiUp();
    parser.use_classes = true;
    return parser.ansi_to_html(text);
  }
