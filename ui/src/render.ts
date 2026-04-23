import type { AnsiUp } from 'ansi_up';

import type { AppElements } from './dom';
import type { Hotkey, LogEntry, ResolvedVariable, Variable, ButtonOverlay } from './types';
import { fetchWithToken } from './dom';

interface RendererState {
  activePanel: 'keyboard' | 'variables' | null;
  restoreInProgress: boolean;
}

interface RendererDeps {
  elements: AppElements;
  ansiUp: AnsiUp;
  sendCommand: (value: string, source: string) => boolean;
  requestVariables: () => void;
  onButtonRendered?: (btn: ButtonOverlay, el: HTMLButtonElement) => void;
  state: RendererState;
}

const maxRenderedLines = 2000;
const pruneRenderedLines = 500;

interface Pane {
  id: string;
  buffer: string;
  el: HTMLElement;
  outputEl: HTMLElement;
  selectEl: HTMLSelectElement;
}

export function createRenderer({ elements, ansiUp, sendCommand, requestVariables, onButtonRendered, state }: RendererDeps) {
  let paneCounter = 0;
  let panes: Pane[] = [];
  const knownBuffers = new Set<string>(['main']);
  const bufferData = new Map<string, LogEntry[]>();

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

  function createPane(initialBuffer = 'main'): Pane {
    const id = `pane-${++paneCounter}`;
    const el = document.createElement('div');
    el.className = 'pane';
    el.id = id;

    const headerEl = document.createElement('div');
    headerEl.className = 'pane-header';

    const selectEl = document.createElement('select');
    selectEl.className = 'pane-select';
    updateSelectOptions(selectEl, initialBuffer);
    
    const pane: Pane = { id, buffer: initialBuffer, el, outputEl: document.createElement('div'), selectEl };

    selectEl.addEventListener('change', () => {
      pane.buffer = selectEl.value;
      renderPaneBuffer(pane);
    });

    headerEl.appendChild(selectEl);

    if (panes.length > 0) {
      const closeBtn = document.createElement('button');
      closeBtn.className = 'pane-close';
      closeBtn.innerHTML = '×';
      closeBtn.title = 'Close Pane';
      closeBtn.addEventListener('click', () => {
        destroyPane(pane);
      });
      headerEl.appendChild(closeBtn);
    }

    const outputEl = document.createElement('div');
    outputEl.className = 'pane-output';
    outputEl.id = `output-${id}`;
    
    // Live scrolling logic per pane
    outputEl.addEventListener('scroll', () => {
      // We could track manual scroll state here if needed
    });

    pane.outputEl = outputEl;

    el.appendChild(headerEl);
    el.appendChild(outputEl);

    panes.push(pane);
    elements.panesContainer.appendChild(el);

    renderPaneBuffer(pane);
    updatePaneHeaders();

    return pane;
  }

  function destroyPane(pane: Pane) {
    if (panes.length <= 1) return;
    panes = panes.filter(p => p.id !== pane.id);
    pane.el.remove();
    updatePaneHeaders();
  }

  function updatePaneHeaders() {
    panes.forEach(pane => {
      const closeBtn = pane.el.querySelector('.pane-close');
      if (panes.length === 1) {
        closeBtn?.remove();
      } else if (!closeBtn) {
        const headerEl = pane.el.querySelector('.pane-header');
        const btn = document.createElement('button');
        btn.className = 'pane-close';
        btn.innerHTML = '×';
        btn.title = 'Close Pane';
        btn.addEventListener('click', () => destroyPane(pane));
        headerEl?.appendChild(btn);
      }
    });
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
    panes.forEach(pane => {
      updateSelectOptions(pane.selectEl, pane.buffer);
    });
  }

  function createEntryDOM(entry: LogEntry): HTMLElement {
    const line = document.createElement('div');
    line.className = 'output-line';

    const span = document.createElement('span');
    span.innerHTML = entry.text ? ansiUp.ansi_to_html(entry.text) : '&nbsp;';
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

  function renderPaneBuffer(pane: Pane) {
    pane.outputEl.innerHTML = '';
    const data = getBufferData(pane.buffer);
    
    const fragment = document.createDocumentFragment();
    const startIdx = Math.max(0, data.length - maxRenderedLines);
    for (let i = startIdx; i < data.length; i++) {
      fragment.appendChild(createEntryDOM(data[i]));
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

    panes.forEach(pane => {
      if (pane.buffer === bufferName) {
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

  function appendCommandHint(command: string) {
    // Append to the last entry of 'main' buffer
    const data = getBufferData('main');
    if (data.length > 0) {
      const lastEntry = data[data.length - 1];
      lastEntry.commands = lastEntry.commands || [];
      lastEntry.commands.push(command);
    }
    
    panes.forEach(pane => {
      if (pane.buffer === 'main') {
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
    // Preserve pane.buffer bindings — they will be re-populated as entries arrive in restore_begin.
    // Non-main buffers will re-appear in selects once registerBuffer() is called for them.
    panes.forEach(pane => {
      pane.outputEl.innerHTML = '';
    });
    updateAllSelects();
  }

  function scrollOutputToBottom() {
    panes.forEach(pane => {
      if (pane.buffer === 'main') {
        pane.outputEl.scrollTop = pane.outputEl.scrollHeight;
      }
    });
  }

  function shouldStickToBottom(pane: Pane): boolean {
    return Math.abs(pane.outputEl.scrollTop - (pane.outputEl.scrollHeight - pane.outputEl.clientHeight)) < pane.outputEl.clientHeight / 2;
  }

  function setActivePanel(panel: 'keyboard' | 'variables') {
    state.activePanel = state.activePanel === panel ? null : panel;

    elements.keyboardToggle.classList.toggle('panel-tab_active', state.activePanel === 'keyboard');
    elements.variablesToggle.classList.toggle('panel-tab_active', state.activePanel === 'variables');
    elements.keyboardPanel.classList.toggle('bottom-panel_active', state.activePanel === 'keyboard');
    elements.variablesPanel.classList.toggle('bottom-panel_active', state.activePanel === 'variables');
    elements.bottomPanels.classList.toggle('bottom-panels_visible', Boolean(state.activePanel));
    elements.bottomToolbarHint.textContent = state.activePanel ? `${state.activePanel} open` : 'Panels';

    if (state.activePanel === 'variables') {
      requestVariables();
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
    appendEntry,
    clearOutput,
    renderHotkeys,
    renderVariables,
    scrollOutputToBottom,
    setActivePanel,
    updateConnectionStatus,
    createPane,
    registerBuffer,
    getBufferData,
  };
}