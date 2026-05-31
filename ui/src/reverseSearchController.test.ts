import { beforeEach, describe, expect, it } from 'vitest';
import { InputHistory } from './history';
import { ReverseSearchController } from './reverseSearchController';

function keydown(key: string, options: Partial<KeyboardEventInit> = {}): KeyboardEvent {
  return new KeyboardEvent('keydown', {
    key,
    code: options.code,
    ctrlKey: options.ctrlKey,
    altKey: options.altKey,
    metaKey: options.metaKey,
    shiftKey: options.shiftKey,
    bubbles: true,
    cancelable: true,
  });
}

function setup(values: string[], draft = '') {
  const history = new InputHistory();
  values.forEach((value) => history.push(value));
  const wrap = document.createElement('div');
  const input = document.createElement('input');
  input.value = draft;
  wrap.appendChild(input);
  document.body.appendChild(wrap);
  const controller = new ReverseSearchController({ input, inputWrap: wrap, searcher: history });
  return { controller, input, wrap };
}

describe('ReverseSearchController', () => {
  beforeEach(() => {
    localStorage.clear();
    document.body.replaceChildren();
  });

  it('edits an independent search query and leaves command draft unchanged until Enter accepts', () => {
    const { controller, input, wrap } = setup(['пнуть орк', 'пнуть гоблин'], 'look');

    const start = keydown('к', { code: 'KeyR', ctrlKey: true });
    expect(controller.handleInputKeydown(start)).toBe(true);
    expect(start.defaultPrevented).toBe(true);
    expect(input.value).toBe('look');

    for (const char of 'пнуть г') {
      expect(controller.handleInputKeydown(keydown(char))).toBe(true);
    }

    expect(input.value).toBe('look');
    expect(wrap.textContent).toContain('reverse-i-search: пнуть г → пнуть гоблин');

    expect(controller.handleInputKeydown(keydown('Enter'))).toBe(true);
    expect(input.value).toBe('пнуть гоблин');
    expect(wrap.classList.contains('input-wrap_reverse-search-active')).toBe(false);
  });

  it('Escape restores the original draft', () => {
    const { controller, input } = setup(['kill goblin'], 'draft');

    controller.handleInputKeydown(keydown('r', { code: 'KeyR', ctrlKey: true }));
    for (const char of 'gob') controller.handleInputKeydown(keydown(char));
    expect(controller.handleInputKeydown(keydown('Escape'))).toBe(true);

    expect(input.value).toBe('draft');
  });

  it('Enter in no-match state closes search without accepting a stale command', () => {
    const { controller, input, wrap } = setup(['kill goblin'], 'draft');

    controller.handleInputKeydown(keydown('r', { code: 'KeyR', ctrlKey: true }));
    for (const char of 'dragon') controller.handleInputKeydown(keydown(char));
    expect(wrap.textContent).toContain('No history match');

    expect(controller.handleInputKeydown(keydown('Enter'))).toBe(true);
    expect(input.value).toBe('draft');
    expect(wrap.classList.contains('input-wrap_reverse-search-active')).toBe(false);
  });

  it('ArrowUp cancels search and lets caller continue with arrow navigation', () => {
    const { controller, input } = setup(['kill goblin'], 'draft');

    controller.handleInputKeydown(keydown('r', { code: 'KeyR', ctrlKey: true }));
    controller.handleInputKeydown(keydown('g'));

    const event = keydown('ArrowUp');
    expect(controller.handleInputKeydown(event)).toBe(false);
    expect(event.defaultPrevented).toBe(true);
    expect(event.cancelBubble).toBe(true);
    expect(input.value).toBe('draft');
  });

  it('stops propagation for Enter while search is active', () => {
    const { controller } = setup(['kill goblin'], 'draft');

    controller.handleInputKeydown(keydown('r', { code: 'KeyR', ctrlKey: true }));
    controller.handleInputKeydown(keydown('g'));

    const event = keydown('Enter');
    expect(controller.handleInputKeydown(event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(event.cancelBubble).toBe(true);
  });

  it('stops propagation for printable query keys while search is active', () => {
    const { controller } = setup(['kill goblin'], 'draft');

    controller.handleInputKeydown(keydown('r', { code: 'KeyR', ctrlKey: true }));

    const event = keydown('g');
    expect(controller.handleInputKeydown(event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(event.cancelBubble).toBe(true);
  });
});
