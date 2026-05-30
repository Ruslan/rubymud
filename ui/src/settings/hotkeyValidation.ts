import { normalizeShortcut } from '../hotkeys';
import type { Hotkey } from './types';

export function duplicateHotkeyShortcutError(hotkeys: Hotkey[], candidate: Hotkey, ignoreID?: number): string {
  const shortcut = normalizeShortcut(candidate.shortcut || '');
  if (!shortcut) return '';
  const duplicate = hotkeys.some((hotkey) => {
    if (ignoreID !== undefined && hotkey.id === ignoreID) return false;
    return normalizeShortcut(hotkey.shortcut || '') === shortcut;
  });
  return duplicate ? 'Shortcut is already bound in this profile.' : '';
}
