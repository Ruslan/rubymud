import './styles/main.css';
import { AnsiUp } from 'ansi_up';

import { getAppElements, fetchWithToken } from './dom';
import { InputHistory } from './history';
import { matchHotkey } from './hotkeys';
import { createRenderer } from './render';
import { createSocket, requestSocketVariables, sendSocketCommand } from './socket';
import type { Hotkey, ServerMessage } from './types';

function logBoot(stage: string, extra?: unknown) {
  const time = Math.round(performance.now());
  if (extra === undefined) {
    console.log(`[boot ${time}ms] ${stage}`);
    return;
  }

  console.log(`[boot ${time}ms] ${stage}`, extra);
}

logBoot('module start');

const elements = getAppElements();
logBoot('dom elements resolved');

const socket = createSocket();
logBoot('socket created', { readyState: socket.readyState, url: socket.url });

const history = new InputHistory();
logBoot('history initialized');

const ansiUp = new AnsiUp();
ansiUp.use_classes = true;
logBoot('ansi initialized');

const state = {
  activePanel: null as 'keyboard' | 'variables' | null,
  restoreInProgress: false,
};

let configuredHotkeys: Hotkey[] = [];

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
  if (socket.readyState !== WebSocket.OPEN) return;
  requestSocketVariables(socket);

  fetchWithToken('/api/variables')
    .then(res => res.json())
    .then(data => renderer.renderVariables(data))
    .catch(err => console.error('Failed to sync variables:', err));
}

function sendCommand(value: string, source = 'input'): boolean {
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
  requestVariables,
  sendCommand,
  state,
});
logBoot('renderer initialized');

window.addEventListener('keydown', (event) => {
  if (focusInputForTyping(event)) {
    return;
  }

  const match = matchHotkey(event, configuredHotkeys);
  if (!match) return;

  logBoot('hotkey matched', { shortcut: match.shortcut, command: match.command });
  event.preventDefault();
  renderer.appendCommandHint(match.command);
  renderer.scrollOutputToBottom();
  sendCommand(match.command, 'key');
});

elements.keyboardToggle.addEventListener('click', () => renderer.setActivePanel('keyboard'));
elements.variablesToggle.addEventListener('click', () => renderer.setActivePanel('variables'));
elements.settingsToggle.addEventListener('click', () => window.open('/settings', 'settings'));
elements.output.addEventListener('click', () => elements.input.focus());
logBoot('event listeners attached');

socket.onopen = () => {
  logBoot('socket open');
  renderer.updateConnectionStatus('connected');
  elements.input.focus();
};

socket.onmessage = (event) => {
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
    });
    state.restoreInProgress = true;
    renderer.clearOutput();
    history.merge(message.history || []);
    configuredHotkeys = message.hotkeys || [];
    renderer.renderHotkeys(configuredHotkeys);
    renderer.renderVariables(message.variables || []);
  }

  if (message.type === 'restore_chunk') {
    logBoot('restore chunk', { entries: message.entries?.length || 0 });
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
    logBoot('variables message', { variables: message.variables?.length || 0 });
    renderer.renderVariables(message.variables || []);
  }

  if (message.type === 'settings.changed' && message.settings?.domain === 'variables') {
    logBoot('settings changed -> request variables');
    requestVariables();
  }
};

socket.onerror = (event) => {
  logBoot('socket error', event);
};

socket.onclose = () => {
  logBoot('socket close', { readyState: socket.readyState });
  renderer.updateConnectionStatus('disconnected');
  renderer.appendEntry({ text: '[disconnected]' });
};

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
  if (!value) return;

  logBoot('input enter', { value, readyState: socket.readyState });
  history.push(value);
  renderer.appendCommandHint(value);
  renderer.scrollOutputToBottom();
  if (sendCommand(value, 'input')) {
    elements.input.value = '';
  }
});

logBoot('main ready');
