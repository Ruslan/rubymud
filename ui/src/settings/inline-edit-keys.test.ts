import { describe, expect, it, vi } from 'vitest';
import { inlineEditKeys } from './inline-edit-keys';

function dispatchKey(target: HTMLElement, key: string, mods: Partial<KeyboardEventInit> = {}) {
  target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...mods }));
}

describe('inlineEditKeys action', () => {
  function setup() {
    const panel = document.createElement('div');
    const input = document.createElement('input');
    const textarea = document.createElement('textarea');
    panel.append(input, textarea);
    document.body.append(panel);
    const save = vi.fn();
    const cancel = vi.fn();
    const action = inlineEditKeys(panel, { save, cancel });
    return { panel, input, textarea, save, cancel, action };
  }

  it('Escape cancels the edit', () => {
    const { input, save, cancel } = setup();
    dispatchKey(input, 'Escape');
    expect(cancel).toHaveBeenCalledOnce();
    expect(save).not.toHaveBeenCalled();
  });

  it('Ctrl+Enter and Cmd+Enter save', () => {
    const { input, textarea, save } = setup();
    dispatchKey(input, 'Enter', { ctrlKey: true });
    dispatchKey(textarea, 'Enter', { metaKey: true });
    expect(save).toHaveBeenCalledTimes(2);
  });

  it('plain Enter saves from a single-line input', () => {
    const { input, save } = setup();
    dispatchKey(input, 'Enter');
    expect(save).toHaveBeenCalledOnce();
  });

  it('plain Enter in a textarea does not save (preserves newline)', () => {
    const { textarea, save } = setup();
    dispatchKey(textarea, 'Enter');
    expect(save).not.toHaveBeenCalled();
  });

  it('update() swaps the callbacks and destroy() detaches the listener', () => {
    const { input, panel, action, cancel } = setup();
    const newCancel = vi.fn();
    action.update({ save: vi.fn(), cancel: newCancel });
    dispatchKey(input, 'Escape');
    expect(cancel).not.toHaveBeenCalled();
    expect(newCancel).toHaveBeenCalledOnce();

    action.destroy();
    dispatchKey(panel, 'Escape');
    expect(newCancel).toHaveBeenCalledOnce();
  });
});
