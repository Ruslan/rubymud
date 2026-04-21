import type { Hotkey } from './types';

export function normalizeShortcut(shortcut: string): string {
  return shortcut
    .toLowerCase()
    .split('+')
    .map((part) => part.trim())
    .filter(Boolean)
    .join('+');
}

export function eventToShortcut(event: KeyboardEvent): string {
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

export function matchHotkey(event: KeyboardEvent, items: Hotkey[]): Hotkey | undefined {
  const shortcut = normalizeShortcut(eventToShortcut(event));
  if (!shortcut) return undefined;
  return items.find((item) => normalizeShortcut(item.shortcut) === shortcut);
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
    if (suffix === 'add') return 'num_+';
    if (suffix === 'subtract') return 'num_-';
    if (suffix === 'multiply') return 'num_*';
    if (suffix === 'divide') return 'num_/';
    if (suffix === 'decimal') return 'num_.';
    if (suffix === 'enter') return 'enter';
  }

  if (key.length === 1) return key;
  return key;
}
