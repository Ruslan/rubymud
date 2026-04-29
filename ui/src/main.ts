import './styles/main.css';
import { AnsiUp } from 'ansi_up';

import { getAppElements, fetchWithToken } from './dom';
import { InputHistory } from './history';
import { matchHotkey } from './hotkeys';
import { createRenderer } from './render';
import { createSocket, sendSocketCommand } from './socket';
import type { Hotkey, ServerMessage } from './types';

type WakeLockSentinelLike = {
  release(): Promise<void>;
  addEventListener(type: 'release', listener: () => void): void;
};

type WakeLockNavigator = Navigator & {
  wakeLock?: {
    request(type: 'screen'): Promise<WakeLockSentinelLike>;
  };
};

const wakeLockEnabledStorageKey = 'mudhost.wakeLockEnabled';
const fontSizeStorageKey = 'mudhost.fontSizePx';
const defaultFontSize = 16;
const minFontSize = 6;
const maxFontSize = 24;
let currentFontSize = defaultFontSize;
const fontSizeControls = document.createElement('div');
fontSizeControls.className = 'font-size-controls';
fontSizeControls.setAttribute('aria-label', 'Font size controls');

const fontSizeDecreaseButton = document.createElement('button');
fontSizeDecreaseButton.className = 'font-size-btn';
fontSizeDecreaseButton.type = 'button';
fontSizeDecreaseButton.title = 'Decrease font size';
fontSizeDecreaseButton.textContent = 'A-';

const fontSizeValue = document.createElement('span');
fontSizeValue.className = 'font-size-value';
fontSizeValue.textContent = `${defaultFontSize}px`;

const fontSizeIncreaseButton = document.createElement('button');
fontSizeIncreaseButton.className = 'font-size-btn';
fontSizeIncreaseButton.type = 'button';
fontSizeIncreaseButton.title = 'Increase font size';
fontSizeIncreaseButton.textContent = 'A+';

fontSizeControls.appendChild(fontSizeDecreaseButton);
fontSizeControls.appendChild(fontSizeValue);
fontSizeControls.appendChild(fontSizeIncreaseButton);

function isWakeLockEnabled(): boolean {
  return localStorage.getItem(wakeLockEnabledStorageKey) !== 'false';
}

function clampFontSize(value: number): number {
  return Math.max(minFontSize, Math.min(maxFontSize, Math.round(value)));
}

function readFontSize(): number {
  const stored = Number(localStorage.getItem(fontSizeStorageKey));
  if (!Number.isFinite(stored)) {
    return defaultFontSize;
  }

  return clampFontSize(stored);
}

function applyFontSize(value: number) {
  const next = clampFontSize(value);
  currentFontSize = next;
  document.documentElement.style.setProperty('--app-font-size', `${next}px`);
  localStorage.setItem(fontSizeStorageKey, String(next));
}

function renderFontSizeControls() {
  fontSizeValue.textContent = `${currentFontSize}px`;
  fontSizeDecreaseButton.disabled = currentFontSize <= minFontSize;
  fontSizeIncreaseButton.disabled = currentFontSize >= maxFontSize;
}

async function releaseWakeLock() {
  if (!wakeLock) {
    return;
  }

  const activeWakeLock = wakeLock;
  wakeLock = null;
  try {
    await activeWakeLock.release();
  } catch (err) {
    console.error('Failed to release wake lock:', err);
  }
}

function logBoot(stage: string, extra?: unknown) {
  const time = Math.round(performance.now());
  if (extra === undefined) {
    console.log(`[boot ${time}ms] ${stage}`);
    return;
  }

  console.log(`[boot ${time}ms] ${stage}`, extra);
}

logBoot('module start');
applyFontSize(readFontSize());

const wakeLockNavigator = navigator as WakeLockNavigator;
let wakeLock: WakeLockSentinelLike | null = null;

async function requestWakeLock() {
  if (!isWakeLockEnabled()) {
    await releaseWakeLock();
    return;
  }

  if (!wakeLockNavigator.wakeLock || document.visibilityState !== 'visible' || wakeLock) {
    return;
  }

  try {
    wakeLock = await wakeLockNavigator.wakeLock.request('screen');
    wakeLock.addEventListener('release', () => {
      wakeLock = null;
    });
    logBoot('wake lock acquired');
  } catch (err) {
    console.error('Failed to acquire wake lock:', err);
  }
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') {
    void requestWakeLock();
  }
});

window.addEventListener('focus', () => {
  void requestWakeLock();
});

window.addEventListener('storage', (event) => {
  if (event.key === wakeLockEnabledStorageKey) {
    void requestWakeLock();
  }
});

void requestWakeLock();

const elements = getAppElements();
logBoot('dom elements resolved');
renderFontSizeControls();

const params = new URLSearchParams(window.location.search);

type SessionSummary = {
  id: number;
  name: string;
  mud_host: string;
  mud_port: number;
};

function sessionTitle(session: SessionSummary): string {
  const name = session.name.trim();
  if (name) return name;
  return `${session.mud_host}:${session.mud_port}`;
}

function updateDocumentTitle(sessions: SessionSummary[]) {
  if (!sessionID) return;
  const current = sessions.find((session) => session.id === sessionID);
  if (!current) return;
  document.title = sessionTitle(current);
}

if (!params.get('session_id')) {
  fetchWithToken('/api/sessions')
    .then((res) => res.json())
    .then((sessions: SessionSummary[]) => {
      const firstSessionID = sessions[0]?.id;
      if (!firstSessionID) return;
      params.set('session_id', String(firstSessionID));
      window.location.search = params.toString();
    })
    .catch((err) => console.error('Failed to pick default session:', err));
}

const sessionID = params.get('session_id') ? parseInt(params.get('session_id')!, 10) : undefined;
let socket = createSocket(sessionID);
let reconnectTimer: number | null = null;
let reconnectAttempts = 0;
logBoot('socket created', { readyState: socket.readyState, url: socket.url, sessionID });

fetchWithToken('/api/sessions')
  .then((res) => res.json())
  .then((sessions: SessionSummary[]) => updateDocumentTitle(sessions))
  .catch((err) => console.error('Failed to load sessions for title:', err));

const history = new InputHistory();
logBoot('history initialized');

const ansiUp = new AnsiUp();
ansiUp.use_classes = true;
logBoot('ansi initialized');

const state = {
  activePanel: null as 'keyboard' | 'variables' | 'groups' | null,
  restoreInProgress: false,
};

let configuredHotkeys: Hotkey[] = [];

// Auto-buttons logic (Alt+0..9)
const autoButtons: (() => void)[] = new Array(10);
let autoButtonIndex = 0;

const onButtonRendered = (btn: any, el: HTMLButtonElement) => {
  autoButtonIndex = (autoButtonIndex + 1) % 10;
  const index = autoButtonIndex;

  const hint = document.createElement('span');
  hint.className = 'button-hotkey-hint';
  hint.textContent = `Alt+${index}`;
  el.prepend(hint);

  autoButtons[index] = () => {
    if (el.disabled) return;
    el.click();
    el.classList.add('trigger-button_active');
    setTimeout(() => el.classList.remove('trigger-button_active'), 500);
  };
};

function isEditableElement(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target instanceof HTMLInputElement) {
    const type = target.type.toLowerCase();
    return type !== 'button' && type !== 'checkbox' && type !== 'radio' && type !== 'range' && type !== 'submit';
  }
  return target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || target.isContentEditable;
}

function isPrintableKey(event: KeyboardEvent): boolean {
  return event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey;
}

function insertInputText(text: string) {
  const { input } = elements;
  const start = input.selectionStart ?? input.value.length;
  const end = input.selectionEnd ?? input.value.length;
  input.value = `${input.value.slice(0, start)}${text}${input.value.slice(end)}`;
  const caret = start + text.length;
  input.setSelectionRange(caret, caret);
}

function focusInputForTyping(event: KeyboardEvent): boolean {
  if (!isPrintableKey(event)) return false;
  if (isEditableElement(event.target)) return false;
  if (document.activeElement === elements.input) return false;

  event.preventDefault();
  elements.input.focus();
  insertInputText(event.key);
  return true;
}

function requestVariables() {
  if (!sessionID) {
    renderer.renderVariables([]);
    return;
  }
  // /resolved is the canonical source: declared vars + defaults + session overrides
  fetchWithToken(`/api/sessions/${sessionID}/variables/resolved`)
    .then(res => res.json())
    .then(data => renderer.renderVariables(data))
    .catch(err => console.error('Failed to sync variables:', err));
}

function requestGroups() {
  if (!sessionID) {
    renderer.renderGroups([]);
    return;
  }
  fetchWithToken(`/api/sessions/${sessionID}/groups`)
    .then(res => res.json())
    .then(data => renderer.renderGroups(data))
    .catch(err => console.error('Failed to sync groups:', err));
}

function toggleGroup(groupName: string, enabled: boolean) {
  if (!sessionID) return;
  fetchWithToken(`/api/sessions/${sessionID}/groups/${encodeURIComponent(groupName)}/toggle`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled })
  }).then(() => requestGroups());
}

function sendCommand(value: string, source = 'input'): boolean {
  if (!sessionID) {
    renderer.appendEntry({ text: '[no session selected]' });
    renderer.updateConnectionStatus('no session');
    return false;
  }

  if (socket.readyState !== WebSocket.OPEN) {
    renderer.appendEntry({ text: '[disconnected]' });
    renderer.updateConnectionStatus('disconnected');
    return false;
  }

  sendSocketCommand(socket, value, source);
  return true;
}

const renderer = createRenderer({
  elements,
  ansiUp,
  fontSizeControls,
  requestVariables,
  requestGroups,
  toggleGroup,
  sendCommand,
  onButtonRendered,
  state,
});
renderer.loadLayout();
logBoot('renderer initialized');

function clearReconnectTimer() {
  if (reconnectTimer === null) {
    return;
  }

  window.clearTimeout(reconnectTimer);
  reconnectTimer = null;
}

function scheduleReconnect() {
  if (reconnectTimer !== null) {
    return;
  }

  const delay = Math.min(1000 * 2 ** reconnectAttempts, 10000);
  reconnectAttempts += 1;
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null;
    connectSocket();
  }, delay);
}

function attachSocketHandlers(target: WebSocket) {
  target.onopen = () => {
    if (socket !== target) {
      return;
    }

    clearReconnectTimer();
    reconnectAttempts = 0;
    renderer.updateConnectionStatus('connected');
    logBoot('socket open');
    elements.input.focus();
  };

  target.onmessage = (event) => {
    if (socket !== target) {
      return;
    }

    logBoot('socket message received', { length: event.data.length });
    const message: ServerMessage = JSON.parse(event.data);
    logBoot('socket message parsed', { type: message.type });

    if (message.type === 'status') {
      renderer.updateConnectionStatus(message.status || 'connected');
    }

    if (message.type === 'restore_begin') {
      logBoot('restore begin', {
        history: message.history?.length || 0,
        hotkeys: message.hotkeys?.length || 0,
        variables: message.variables?.length || 0,
        buffers: Object.keys(message.buffers || {}).join(', ') || 'none',
      });
      state.restoreInProgress = true;
      renderer.clearOutput();
      history.merge(message.history || []);
      configuredHotkeys = message.hotkeys || [];
      renderer.renderHotkeys(configuredHotkeys);
      if (message.timers) {
        renderer.renderTimers(message.timers);
      }
      requestVariables();
      for (const entries of Object.values(message.buffers || {})) {
        entries.forEach(renderer.appendEntry);
      }
    }

    if (message.type === 'tick' && message.timers) {
      renderer.renderTimers(message.timers);
    }

    if (message.type === 'restore_chunk') {
      logBoot('restore chunk (legacy)', { entries: message.entries?.length || 0 });
      (message.entries || []).forEach(renderer.appendEntry);
    }

    if (message.type === 'restore_end') {
      logBoot('restore end');
      state.restoreInProgress = false;
      renderer.scrollOutputToBottom();
    }

    if (message.type === 'output') {
      logBoot('output message', { entries: message.entries?.length || 0 });
      (message.entries || []).forEach(renderer.appendEntry);
    }

    if (message.type === 'variables') {
      logBoot('variables message -> fetch resolved');
      requestVariables();
    }

    if (message.type === 'settings.changed' && message.settings?.domain === 'variables') {
      logBoot('settings changed -> request variables');
      requestVariables();
    }

    if (message.type === 'command_hint' && message.entry_id && message.command) {
      logBoot('command hint received', { id: message.entry_id, buffer: message.buffer, cmd: message.command });
      renderer.addCommandHint(message.entry_id, message.buffer || 'main', message.command);
    }
  };

  target.onerror = (event) => {
    if (socket !== target) {
      return;
    }

    logBoot('socket error', event);
  };

  target.onclose = () => {
    if (socket !== target) {
      return;
    }

    logBoot('socket close', { readyState: target.readyState, reconnectAttempts });
    renderer.updateConnectionStatus('disconnected');
    if (reconnectAttempts === 0) {
      renderer.appendEntry({ text: '[disconnected]' });
    }
    scheduleReconnect();
  };
}

function connectSocket() {
  if (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING) {
    return;
  }

  renderer.updateConnectionStatus('connecting');
  socket = createSocket(sessionID);
  logBoot('socket reconnect created', { readyState: socket.readyState, url: socket.url, sessionID });
  attachSocketHandlers(socket);
}

attachSocketHandlers(socket);

window.addEventListener('keydown', (event) => {
  if (focusInputForTyping(event)) {
    return;
  }

  // Handle Alt+0..9 for auto-buttons.
  // Use event.code (layout-independent) because on macOS event.key for Alt+digit
  // produces special characters (¡, ™, …) instead of the digit character.
  if (event.altKey && event.code >= 'Digit0' && event.code <= 'Digit9') {
    const index = parseInt(event.code.slice(5), 10); // 'Digit3' → 3
    if (autoButtons[index]) {
      event.preventDefault();
      autoButtons[index]!();
      return;
    }
  }

  const match = matchHotkey(event, configuredHotkeys);
  if (!match) return;

  logBoot('hotkey matched', { shortcut: match.shortcut, command: match.command });
  event.preventDefault();
  renderer.appendCommandHint(match.command);
  sendCommand(match.command, 'key');
});

elements.keyboardToggle.addEventListener('click', () => renderer.setActivePanel('keyboard'));
elements.variablesToggle.addEventListener('click', () => renderer.setActivePanel('variables'));
elements.groupsToggle.addEventListener('click', () => renderer.setActivePanel('groups'));
elements.settingsToggle.addEventListener('click', () => window.open('/settings', 'settings'));
fontSizeDecreaseButton.addEventListener('click', () => {
  applyFontSize(currentFontSize - 1);
  renderFontSizeControls();
});
fontSizeIncreaseButton.addEventListener('click', () => {
  applyFontSize(currentFontSize + 1);
  renderFontSizeControls();
});
elements.connectionStatus.addEventListener('click', () => {
  if (elements.connectionStatus.textContent !== 'disconnected') {
    return;
  }

  clearReconnectTimer();
  reconnectAttempts = 0;
  connectSocket();
});

elements.panesContainer.addEventListener('click', (event) => {
  const selection = window.getSelection();
  if (selection && selection.toString()) {
    return;
  }

  if ((event.target as HTMLElement | null)?.closest('button') || (event.target as HTMLElement | null)?.closest('select')) {
    return;
  }

  elements.input.focus();
});
logBoot('event listeners attached');

window.addEventListener('focus', () => {
  logBoot('window focus -> request variables');
  requestVariables();
  elements.input.focus();
});
document.addEventListener('visibilitychange', () => {
  logBoot('visibility change', { hidden: document.hidden });
  if (!document.hidden) requestVariables();
});
window.setInterval(() => {
  if (!document.hidden) {
    logBoot('interval -> request variables');
    requestVariables();
  }
}, 3000);

elements.input.addEventListener('keydown', (event) => {
  if (event.key === 'ArrowUp') {
    event.preventDefault();
    elements.input.value = history.up(elements.input.value);
    const length = elements.input.value.length;
    elements.input.setSelectionRange(length, length);
    return;
  }

  if (event.key === 'ArrowDown') {
    event.preventDefault();
    elements.input.value = history.down(elements.input.value);
    const length = elements.input.value.length;
    elements.input.setSelectionRange(length, length);
    return;
  }

  if (event.key !== 'Enter') return;

  const value = elements.input.value.trim();
  logBoot('input enter', { value, readyState: socket.readyState });

  if (value) {
    history.push(value);
    renderer.appendCommandHint(value);
  }

  if (sendCommand(value, 'input')) {
    elements.input.value = '';
  }
});

logBoot('main ready');
