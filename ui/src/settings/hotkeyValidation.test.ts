import { describe, expect, it } from 'vitest';
import { duplicateHotkeyShortcutError } from './hotkeyValidation';
import type { Hotkey } from './types';

const hotkeys: Hotkey[] = [
  { id: 1, position: 1, shortcut: 'ctrl+k', command: '#echo one', mobile_row: 0, mobile_order: 0 },
  { id: 2, position: 2, shortcut: 'Alt+Up', command: '#echo two', mobile_row: 0, mobile_order: 0 },
];

describe('duplicateHotkeyShortcutError', () => {
  it('detects duplicates using runtime shortcut normalization', () => {
    expect(duplicateHotkeyShortcutError(hotkeys, { shortcut: ' ctrl + k ', command: '#echo other', mobile_row: 0, mobile_order: 0 })).toBe('Shortcut is already bound in this profile.');
  });

  it('allows duplicate commands on different shortcuts', () => {
    expect(duplicateHotkeyShortcutError(hotkeys, { shortcut: 'ctrl+j', command: '#echo one', mobile_row: 0, mobile_order: 0 })).toBe('');
  });

  it('ignores the currently edited hotkey by id', () => {
    expect(duplicateHotkeyShortcutError(hotkeys, { id: 1, shortcut: 'CTRL+K', command: '#echo edited', mobile_row: 0, mobile_order: 0 }, 1)).toBe('');
  });

  it('still catches another row when ignore id differs', () => {
    expect(duplicateHotkeyShortcutError(hotkeys, { id: 1, shortcut: 'alt+up', command: '#echo edited', mobile_row: 0, mobile_order: 0 }, 1)).toBe('Shortcut is already bound in this profile.');
  });
});
