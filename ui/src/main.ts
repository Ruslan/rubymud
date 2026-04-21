import './styles/main.css';
import { AnsiUp } from 'ansi_up';

interface Hotkey {
  shortcut: string;
  command: string;
}

interface Variable {
  key: string;
  value: string;
}

interface LogEntry {
  text?: string;
  commands?: string[];
  buttons?: Array<{ label: string; command: string }>;
}

interface ServerMessage {
  type: string;
  status?: string;
  history?: string[];
  hotkeys?: Hotkey[];
  variables?: Variable[];
  entries?: LogEntry[];
  settings?: { domain: string };
}

class InputHistory {
  private history: string[] = [];
  private historyIndex: number = -1;
  private historyViewed: string[] = [];
  private historyDirection: number | null = null;
  private lastResult: string | null = null;
  private arrowQuery: string = '';

  constructor() {
    this.loadHistory();
  }

  push(value: string) {
    if (!value) return;
    this.history.push(value);
    this.historyIndex = this.history.length;
    this.historyViewed = [];
    this.saveHistory();
  }

  merge(remoteHistory: string[]) {
    this.history = [...this.history, ...(remoteHistory || [])].filter((value, index, self) => {
      return self.indexOf(value) === index;
    });
    this.historyIndex = this.history.length;
    this.historyViewed = [];
    this.saveHistory();
  }

  private loadHistory() {
    const storedHistory = localStorage.getItem('commandHistory');
    if (storedHistory) {
      try {
        this.history = JSON.parse(storedHistory);
        this.historyIndex = this.history.length;
      } catch (e) {
        console.error('Failed to parse history', e);
      }
    }
  }

  private saveHistory() {
    localStorage.setItem('commandHistory', JSON.stringify(this.history));
  }

  up(currentValue: string): string {
    if (currentValue === this.lastResult) {
      currentValue = this.arrowQuery;
    } else {
      this.arrowQuery = currentValue;
    }

    if (this.historyDirection !== -1) {
      this.historyViewed = [];
      this.historyDirection = -1;
    }

    for (let i = this.historyIndex - 1; i >= 0; i--) {
      const value = this.history[i];
      if (value && value.startsWith(currentValue) && !this.historyViewed.includes(value)) {
        this.historyIndex = i;
        this.historyViewed.push(value);
        this.lastResult = value;
        return value;
      }
    }

    this.historyIndex = 0;
    return currentValue;
  }

  down(currentValue: string): string {
    if (currentValue === this.lastResult) {
      currentValue = this.arrowQuery;
    } else {
      this.arrowQuery = currentValue;
    }

    if (this.historyDirection !== 1) {
      this.historyViewed = [];
      this.historyDirection = 1;
    }

    for (let i = this.historyIndex + 1; i < this.history.length; i++) {
      const value = this.history[i];
      if (value && value.startsWith(currentValue) && !this.historyViewed.includes(value)) {
        this.historyIndex = i;
        this.historyViewed.push(value);
        this.lastResult = value;
        return value;
      }
    }

    this.historyIndex = this.history.length;
    return currentValue;
  }
}

// Elements
const output = document.getElementById('output') as HTMLDivElement;
const bottomPanels = document.getElementById('bottom-panels') as HTMLDivElement;
const bottomToolbarHint = document.getElementById('bottom-toolbar-hint') as HTMLSpanElement;
const hotkeysBox = document.getElementById('hotkeys') as HTMLDivElement;
const variablesList = document.getElementById('variables-list') as HTMLDivElement;
const keyboardPanel = document.getElementById('panel-keyboard') as HTMLDivElement;
const variablesPanel = document.getElementById('panel-variables') as HTMLDivElement;
const keyboardToggle = document.getElementById('panel-toggle-keyboard') as HTMLButtonElement;
const variablesToggle = document.getElementById('panel-toggle-variables') as HTMLButtonElement;
const connectionStatus = document.getElementById('connection-status') as HTMLSpanElement;
const input = document.getElementById('input') as HTMLInputElement;

const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const socket = new WebSocket(`${protocol}//${window.location.host}/ws`);
const history = new InputHistory();

const ansiUp = new AnsiUp();
ansiUp.use_classes = true;

let configuredHotkeys: Hotkey[] = [];
let restoreInProgress = false;
let activePanel: 'keyboard' | 'variables' | null = null;
const maxRenderedLines = 2000;
const pruneRenderedLines = 500;

function setActivePanel(panel: 'keyboard' | 'variables') {
  activePanel = activePanel === panel ? null : panel;

  keyboardToggle.classList.toggle('panel-tab_active', activePanel === 'keyboard');
  variablesToggle.classList.toggle('panel-tab_active', activePanel === 'variables');
  keyboardPanel.classList.toggle('bottom-panel_active', activePanel === 'keyboard');
  variablesPanel.classList.toggle('bottom-panel_active', activePanel === 'variables');
  bottomPanels.classList.toggle('bottom-panels_visible', Boolean(activePanel));
  bottomToolbarHint.textContent = activePanel ? `${activePanel} open` : 'Panels';

  if (activePanel === 'variables') {
    requestVariables();
  }
}

function sendCommand(value: string, source: string = 'input'): boolean {
  if (socket.readyState !== WebSocket.OPEN) {
    appendEntry({ text: '[disconnected]' });
    updateConnectionStatus('disconnected');
    return false;
  }
  socket.send(JSON.stringify({ method: 'send', value: value, source: source }));
  return true;
}

function updateConnectionStatus(status: string) {
  connectionStatus.textContent = status;
  connectionStatus.classList.toggle('status-connected', status === 'connected');
  connectionStatus.classList.toggle('status-disconnected', status === 'disconnected');
}

function shouldStickToBottom(): boolean {
  return Math.abs(output.scrollTop - (output.scrollHeight - output.clientHeight)) < output.clientHeight / 2;
}

function trimRenderedLines() {
  if (output.children.length <= maxRenderedLines) return;
  for (let i = 0; i < pruneRenderedLines && output.firstChild; i++) {
    output.removeChild(output.firstChild);
  }
}

function renderHotkeys(items: Hotkey[]) {
  hotkeysBox.innerHTML = '';
  (items || []).forEach((item) => {
    const chip = document.createElement('span');
    chip.className = 'hotkey-chip';

    const button = document.createElement('button');
    button.type = 'button';
    button.addEventListener('click', () => {
      appendCommandHint(item.command);
      output.scrollTop = output.scrollHeight;
      sendCommand(item.command, 'key');
    });

    const key = document.createElement('kbd');
    key.textContent = item.shortcut;
    button.appendChild(key);

    const label = document.createElement('span');
    label.textContent = item.command;
    button.appendChild(label);

    chip.appendChild(button);
    hotkeysBox.appendChild(chip);
  });

  keyboardToggle.style.display = items && items.length ? 'inline-flex' : 'none';
  if ((!items || !items.length) && activePanel === 'keyboard') {
    setActivePanel('keyboard');
  }
}

function renderVariables(items: Variable[]) {
  variablesList.innerHTML = '';

  if (!items || !items.length) {
    const empty = document.createElement('div');
    empty.className = 'variables-empty';
    empty.textContent = 'No variables defined yet.';
    variablesList.appendChild(empty);
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

    variablesList.appendChild(row);
  });
}

function requestVariables() {
  if (socket.readyState !== WebSocket.OPEN) return;
  socket.send(JSON.stringify({ method: 'variables' }));
}

function normalizeShortcut(shortcut: string): string {
  return shortcut
    .toLowerCase()
    .split('+')
    .map((part) => part.trim())
    .filter(Boolean)
    .join('+');
}

function eventToShortcut(event: KeyboardEvent): string {
  const parts = [];
  if (event.ctrlKey) parts.push('ctrl');
  if (event.altKey) parts.push('alt');
  if (event.metaKey) parts.push('command');
  if (event.shiftKey) parts.push('shift');

  const key = normalizeEventKey(event);
  if (!key) return '';
  parts.push(key);
  return parts.join('+');
}

function normalizeEventKey(event: KeyboardEvent): string {
  const key = (event.key || '').toLowerCase();
  const code = event.code || '';

  if (/^Key[A-Z]$/.test(code)) return code.slice(3).toLowerCase();
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);

  if (/^f\d{1,2}$/.test(key)) return key;
  if (key === 'pageup') return 'pageup';
  if (key === 'pagedown') return 'pagedown';
  if (key === 'home') return 'home';
  if (key === 'end') return 'end';
  if (key === 'arrowup') return 'up';
  if (key === 'arrowdown') return 'down';
  if (key === 'arrowleft') return 'left';
  if (key === 'arrowright') return 'right';

  if (event.code && event.code.startsWith('Numpad')) {
    const suffix = event.code.replace('Numpad', '').toLowerCase();
    if (/^[0-9]$/.test(suffix)) return `num_${suffix}`;
    if (suffix === 'add') return 'num_+' ;
    if (suffix === 'subtract') return 'num_-';
    if (suffix === 'multiply') return 'num_*';
    if (suffix === 'divide') return 'num_/';
    if (suffix === 'decimal') return 'num_.';
    if (suffix === 'enter') return 'enter';
  }

  if (key.length === 1) return key;
  return key;
}

function handleHotkey(event: KeyboardEvent) {
  const shortcut = normalizeShortcut(eventToShortcut(event));
  if (!shortcut) return;

  const match = configuredHotkeys.find((item) => normalizeShortcut(item.shortcut) === shortcut);
  if (!match) return;

  event.preventDefault();
  appendCommandHint(match.command);
  output.scrollTop = output.scrollHeight;
  sendCommand(match.command, 'key');
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
    const btns = document.createElement('span');
    btns.className = 'trigger-buttons';
    entry.buttons.forEach((btn) => {
      const b = document.createElement('button');
      b.className = 'trigger-button';
      b.type = 'button';
      b.textContent = btn.label;
      b.addEventListener('click', () => {
        sendCommand(btn.command, 'button');
      });
      btns.appendChild(b);
    });
    line.appendChild(btns);
  }

  output.appendChild(line);
  trimRenderedLines();
  if (shouldScroll && !restoreInProgress) {
    output.scrollTop = output.scrollHeight;
  }
}

function appendCommandHint(command: string) {
  const line = output.lastElementChild;
  if (!line) return;

  const hint = document.createElement('span');
  hint.className = 'output-hint';
  hint.textContent = `-> ${command}`;
  line.appendChild(hint);
}

window.addEventListener('keydown', handleHotkey);
keyboardToggle.addEventListener('click', () => setActivePanel('keyboard'));
variablesToggle.addEventListener('click', () => setActivePanel('variables'));

socket.onopen = () => {
  updateConnectionStatus('connected');
};

socket.onmessage = (event) => {
  const message: ServerMessage = JSON.parse(event.data);

  if (message.type === 'status') {
    updateConnectionStatus(message.status || 'connected');
  }

  if (message.type === 'restore_begin') {
    restoreInProgress = true;
    output.innerHTML = '';
    history.merge(message.history || []);
    configuredHotkeys = message.hotkeys || [];
    renderHotkeys(configuredHotkeys);
    renderVariables(message.variables || []);
  }

  if (message.type === 'restore_chunk') {
    (message.entries || []).forEach(appendEntry);
  }

  if (message.type === 'restore_end') {
    restoreInProgress = false;
    output.scrollTop = output.scrollHeight;
  }

  if (message.type === 'output') {
    (message.entries || []).forEach(appendEntry);
  }

  if (message.type === 'variables') {
    renderVariables(message.variables || []);
  }

  if (message.type === 'settings.changed' && message.settings?.domain === 'variables') {
    requestVariables();
  }
};

socket.onclose = () => {
  updateConnectionStatus('disconnected');
  appendEntry({ text: '[disconnected]' });
};

window.addEventListener('focus', () => requestVariables());
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) requestVariables();
});
window.setInterval(() => {
  if (!document.hidden) requestVariables();
}, 3000);

input.addEventListener('keydown', (event) => {
  if (event.key === 'ArrowUp') {
    event.preventDefault();
    input.value = history.up(input.value);
    const length = input.value.length;
    input.setSelectionRange(length, length);
    return;
  }

  if (event.key === 'ArrowDown') {
    event.preventDefault();
    input.value = history.down(input.value);
    const length = input.value.length;
    input.setSelectionRange(length, length);
    return;
  }

  if (event.key !== 'Enter') return;

  const value = input.value.trim();
  if (!value) return;

  history.push(value);
  appendCommandHint(value);
  output.scrollTop = output.scrollHeight;
  if (sendCommand(value, 'input')) {
    input.value = '';
  }
});
