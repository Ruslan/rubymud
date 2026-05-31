import { describe, expect, it } from 'vitest';
import { appendReverseSearchHintContent, findCaseInsensitiveMatchSegments } from './historyHint';

describe('reverse search hint helpers', () => {
  it('finds case-insensitive Cyrillic match segments', () => {
    expect(findCaseInsensitiveMatchSegments('убить гоблина', 'ГОБЛ')).toEqual({
      before: 'убить ',
      match: 'гобл',
      after: 'ина',
    });
  });

  it('renders matched pattern with underline span using text nodes', () => {
    const target = document.createElement('div');

    appendReverseSearchHintContent(target, 'ГОБЛ', 'убить гоблина');

    expect(target.textContent).toBe('reverse-i-search: ГОБЛ → убить гоблина');
    const match = target.querySelector('.reverse-search-match');
    expect(match?.textContent).toBe('гобл');
  });

  it('renders safe no-match hint', () => {
    const target = document.createElement('div');

    appendReverseSearchHintContent(target, 'dragon', null);

    expect(target.textContent).toBe('reverse-i-search: dragon → No history match');
    expect(target.querySelector('.reverse-search-empty')?.textContent).toBe('No history match');
  });
});
