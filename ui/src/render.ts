import type { AnsiUp } from 'ansi_up';

import type { AppElements } from './dom';
import type { Hotkey, LogEntry, Variable } from './types';

interface RendererState {
  activePanel: 'keyboard' | 'variables' | null;
  restoreInProgress: boolean;
}

interface RendererDeps {
  elements: AppElements;
  ansiUp: AnsiUp;
  sendCommand: (value: string, source: string) => boolean;
  requestVariables: () => void;
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

  function renderVariables(items: Variable[]) {
    elements.variablesList.innerHTML = '';

    if (!items || !items.length) {
      const empty = document.createElement('div');
      empty.className = 'variables-empty';
      empty.textContent = 'No variables defined yet.';
      elements.variablesList.appendChild(empty);
      return;
    }

    items.forEach((item) => {
      const row = document.createElement('div');
      row.className = 'variable-row';

      const key = document.createElement('div');
      key.className = 'variable-key';
      key.textContent = `$${item.key}`;
      row.appendChild(key);

      const value = document.createElement('div');
      value.className = 'variable-value';
      value.textContent = item.value || '';
      row.appendChild(value);

      elements.variablesList.appendChild(row);
    });
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
