import { describe, expect, it } from 'vitest';
import { eventToShortcut } from '../hotkeys';
import { formatKeyboardShortcut, type ShortcutKeyboardEvent } from './shortcut';

function shortcut(event: ShortcutKeyboardEvent): string {
  return formatKeyboardShortcut(event);
}

describe('formatKeyboardShortcut', () => {
  it('uses runtime-compatible canonical shortcuts', () => {
    const event: ShortcutKeyboardEvent = { key: 'k', code: 'KeyK', ctrlKey: true, altKey: false, shiftKey: true, metaKey: false };
    expect(shortcut(event)).toBe('ctrl+shift+k');
    expect(shortcut(event)).toBe(eventToShortcut(event as KeyboardEvent));
  });

  it('maps arrow keys to runtime names', () => {
    expect(shortcut({ key: 'ArrowUp', code: 'ArrowUp', ctrlKey: false, altKey: true, shiftKey: false, metaKey: false })).toBe('alt+up');
  });

  it('maps Meta/Cmd to command', () => {
    expect(shortcut({ key: 'k', code: 'KeyK', ctrlKey: false, altKey: false, shiftKey: false, metaKey: true })).toBe('command+k');
  });

  it('maps numpad digits distinctly', () => {
    expect(shortcut({ key: '1', code: 'Numpad1', ctrlKey: false, altKey: false, shiftKey: false, metaKey: false })).toBe('num_1');
  });

  it('uses physical digit for shifted symbols', () => {
    expect(shortcut({ key: '!', code: 'Digit1', ctrlKey: false, altKey: false, shiftKey: true, metaKey: false })).toBe('shift+1');
  });

  it('ignores bare modifier-only presses', () => {
    expect(shortcut({ key: 'Shift', code: 'ShiftLeft', ctrlKey: false, altKey: false, shiftKey: true, metaKey: false })).toBe('');
  });
});
