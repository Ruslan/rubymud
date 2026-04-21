import './styles/main.css';
import { AnsiUp } from 'ansi_up';

import { getAppElements } from './dom';
import { InputHistory } from './history';
import { matchHotkey } from './hotkeys';
import { createRenderer } from './render';
import { createSocket, requestSocketVariables, sendSocketCommand } from './socket';
import type { Hotkey, ServerMessage } from './types';

const elements = getAppElements();
const socket = createSocket();
const history = new InputHistory();
const ansiUp = new AnsiUp();
ansiUp.use_classes = true;

const state = {
  activePanel: null as 'keyboard' | 'variables' | null,
  restoreInProgress: false,
};

let configuredHotkeys: Hotkey[] = [];

function requestVariables() {
  if (socket.readyState !== WebSocket.OPEN) return;
  requestSocketVariables(socket);
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

window.addEventListener('keydown', (event) => {
  const match = matchHotkey(event, configuredHotkeys);
  if (!match) return;

  event.preventDefault();
  renderer.appendCommandHint(match.command);
  renderer.scrollOutputToBottom();
  sendCommand(match.command, 'key');
});

elements.keyboardToggle.addEventListener('click', () => renderer.setActivePanel('keyboard'));
elements.variablesToggle.addEventListener('click', () => renderer.setActivePanel('variables'));

socket.onopen = () => {
  renderer.updateConnectionStatus('connected');
};

socket.onmessage = (event) => {
  const message: ServerMessage = JSON.parse(event.data);

  if (message.type === 'status') {
    renderer.updateConnectionStatus(message.status || 'connected');
  }

  if (message.type === 'restore_begin') {
    state.restoreInProgress = true;
    renderer.clearOutput();
    history.merge(message.history || []);
    configuredHotkeys = message.hotkeys || [];
    renderer.renderHotkeys(configuredHotkeys);
    renderer.renderVariables(message.variables || []);
  }

  if (message.type === 'restore_chunk') {
    (message.entries || []).forEach(renderer.appendEntry);
  }

  if (message.type === 'restore_end') {
    state.restoreInProgress = false;
    renderer.scrollOutputToBottom();
  }

  if (message.type === 'output') {
    (message.entries || []).forEach(renderer.appendEntry);
  }

  if (message.type === 'variables') {
    renderer.renderVariables(message.variables || []);
  }

  if (message.type === 'settings.changed' && message.settings?.domain === 'variables') {
    requestVariables();
  }
};

socket.onclose = () => {
  renderer.updateConnectionStatus('disconnected');
  renderer.appendEntry({ text: '[disconnected]' });
};

window.addEventListener('focus', requestVariables);
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) requestVariables();
});
window.setInterval(() => {
  if (!document.hidden) requestVariables();
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

  history.push(value);
  renderer.appendCommandHint(value);
  renderer.scrollOutputToBottom();
  if (sendCommand(value, 'input')) {
    elements.input.value = '';
  }
});
