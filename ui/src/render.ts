import type { AnsiUp } from 'ansi_up';

import type { AppElements } from './dom';
import type { Hotkey, LogEntry, ResolvedVariable, Variable } from './types';
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

export function createRenderer({ elements, ansiUp, sendCommand, requestVariables, state }: RendererDeps) {
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
        elements.output.scrollTop = elements.output.scrollHeight;
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
    // Preserve focused input state so in-progress typing survives re-renders
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

    // Add "Fill Missing From Defaults" button if session is selected
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
      // Restore in-progress value if this was the focused field
      if (keyStr === focusedKey && focusedValue !== null) {
        input.value = focusedValue;
      }
      
      input.addEventListener('keydown', async (e) => {
        if (e.key === 'Enter') {
          const newVal = input.value.trim();
          if (!newVal) return; // empty = no-op; use Clear button to remove override
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

    // Restore focus to the previously active input
    if (focusedKey != null) {
      const restored = elements.variablesList.querySelector<HTMLInputElement>(`[data-var-key="${CSS.escape(focusedKey)}"]`);
      if (restored) {
        restored.focus();
        // Move cursor to end
        const len = restored.value.length;
        restored.setSelectionRange(len, len);
      }
    }
  }

  function appendEntry(entry: LogEntry) {
    const shouldScroll = shouldStickToBottom();
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
          appendCommandHint(btn.command);
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

    elements.output.appendChild(line);
    trimRenderedLines();
    if (shouldScroll && !state.restoreInProgress) {
      elements.output.scrollTop = elements.output.scrollHeight;
    }
  }

  function appendCommandHint(command: string) {
    const line = elements.output.lastElementChild;
    if (!line) return;

    const hint = document.createElement('span');
    hint.className = 'output-hint';
    hint.textContent = `-> ${command}`;
    line.appendChild(hint);
  }

  function clearOutput() {
    elements.output.innerHTML = '';
  }

  function scrollOutputToBottom() {
    elements.output.scrollTop = elements.output.scrollHeight;
  }

  function shouldStickToBottom(): boolean {
    return Math.abs(elements.output.scrollTop - (elements.output.scrollHeight - elements.output.clientHeight)) < elements.output.clientHeight / 2;
  }

  function trimRenderedLines() {
    if (elements.output.children.length <= maxRenderedLines) return;
    for (let i = 0; i < pruneRenderedLines && elements.output.firstChild; i++) {
      elements.output.removeChild(elements.output.firstChild);
    }
  }

  return {
    appendCommandHint,
    appendEntry,
    clearOutput,
    renderHotkeys,
    renderVariables,
    scrollOutputToBottom,
    setActivePanel,
    updateConnectionStatus,
  };
}
