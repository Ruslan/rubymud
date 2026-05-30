import { eventToShortcut } from '../hotkeys';

export type ShortcutKeyboardEvent = Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'altKey' | 'shiftKey' | 'metaKey'>;

export function formatKeyboardShortcut(event: ShortcutKeyboardEvent): string {
  if (isBareModifier(event.key)) return '';
  return eventToShortcut(event as KeyboardEvent);
}

function isBareModifier(key: string): boolean {
  return key === 'Control' || key === 'Alt' || key === 'Shift' || key === 'Meta';
}
