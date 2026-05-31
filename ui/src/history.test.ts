import { beforeEach, describe, expect, it } from 'vitest';
import { InputHistory } from './history';

function historyWith(values: string[]): InputHistory {
  const history = new InputHistory();
  values.forEach((value) => history.push(value));
  return history;
}

describe('InputHistory', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('starts prefix search from newest after manual reset, not a stale history index', () => {
    const history = historyWith([
      'осмотреться',
      'убить гоблина старого',
      'сказать привет',
      'убить гоблина свежего',
    ]);

    expect(history.up('')).toBe('убить гоблина свежего');
    expect(history.up('')).toBe('сказать привет');
    expect(history.up('')).toBe('убить гоблина старого');

    history.resetNavigation();

    expect(history.up('убить г')).toBe('убить гоблина свежего');
  });

  it('navigates prefix matches newest to older and clamps at the oldest match', () => {
    const history = historyWith([
      'убить гоблина старого',
      'убить орка',
      'убить гоблина среднего',
      'север',
      'убить гоблина свежего',
    ]);

    expect(history.up('убить г')).toBe('убить гоблина свежего');
    expect(history.up('убить гоблина свежего')).toBe('убить гоблина среднего');
    expect(history.up('убить гоблина среднего')).toBe('убить гоблина старого');
    expect(history.up('убить гоблина старого')).toBe('убить гоблина старого');
  });

  it('navigates prefix matches down toward newer matches and then restores the draft query', () => {
    const history = historyWith([
      'убить гоблина старого',
      'убить орка',
      'убить гоблина среднего',
      'север',
      'убить гоблина свежего',
    ]);

    expect(history.up('убить г')).toBe('убить гоблина свежего');
    expect(history.up('убить гоблина свежего')).toBe('убить гоблина среднего');
    expect(history.up('убить гоблина среднего')).toBe('убить гоблина старого');
    expect(history.down('убить гоблина старого')).toBe('убить гоблина среднего');
    expect(history.down('убить гоблина среднего')).toBe('убить гоблина свежего');
    expect(history.down('убить гоблина свежего')).toBe('убить г');
  });

  it('navigates empty input across full history newest to older, then down to draft', () => {
    const history = historyWith(['look', 'north', 'kill goblin']);

    expect(history.up('')).toBe('kill goblin');
    expect(history.up('kill goblin')).toBe('north');
    expect(history.down('north')).toBe('kill goblin');
    expect(history.down('kill goblin')).toBe('');
  });

  it('keeps prefix matching case-insensitive', () => {
    const history = historyWith(['Kill Goblin', 'kill orc', 'KILL Guard']);

    expect(history.up('kill g')).toBe('KILL Guard');
    expect(history.up('KILL Guard')).toBe('Kill Goblin');
    expect(history.matches('KILL G')).toEqual(['KILL Guard', 'Kill Goblin']);
  });

  it('resetNavigation starts the next arrow navigation from the newest item', () => {
    const history = historyWith(['first', 'second', 'third']);

    expect(history.up('')).toBe('third');
    expect(history.up('third')).toBe('second');

    history.resetNavigation();

    expect(history.up('')).toBe('third');
  });

  it('begins reverse search with empty draft/query in a safe no-match state', () => {
    const history = historyWith(['look', 'kill goblin']);

    expect(history.beginReverseSearch('')).toEqual({ active: true, query: '', draft: '', match: null });
    expect(history.acceptReverseSearch()).toBeNull();
  });

  it('updates reverse search query and returns the newest substring match', () => {
    const history = historyWith([
      'осмотреться',
      'пнуть орк',
      'пнуть гоблина старого',
      'пнуть гоблина свежего',
    ]);

    history.beginReverseSearch('draft command');
    expect(history.updateReverseSearchQuery('пнуть').match).toBe('пнуть гоблина свежего');
    expect(history.updateReverseSearchQuery('пнуть г').match).toBe('пнуть гоблина свежего');
  });

  it('repeated reverse search advances to older matches and clamps at oldest', () => {
    const history = historyWith([
      'пнуть гоблина старого',
      'пнуть орка',
      'сказать про гоблина',
      'пнуть гоблина свежего',
    ]);

    history.beginReverseSearch('');
    expect(history.updateReverseSearchQuery('гобл').match).toBe('пнуть гоблина свежего');
    expect(history.advanceReverseSearch().match).toBe('сказать про гоблина');
    expect(history.advanceReverseSearch().match).toBe('пнуть гоблина старого');
    expect(history.advanceReverseSearch().match).toBe('пнуть гоблина старого');
  });

  it('query updates recompute reverse search from newest', () => {
    const history = historyWith(['пнуть гоблина старого', 'пнуть орка', 'пнуть гоблина свежего']);

    history.beginReverseSearch('');
    expect(history.updateReverseSearchQuery('гобл').match).toBe('пнуть гоблина свежего');
    expect(history.advanceReverseSearch().match).toBe('пнуть гоблина старого');
    expect(history.updateReverseSearchQuery('пнуть').match).toBe('пнуть гоблина свежего');
  });

  it('reverse search is case-insensitive for Cyrillic queries', () => {
    const history = historyWith(['осмотреться', 'убить гоблина']);

    history.beginReverseSearch('');
    expect(history.updateReverseSearchQuery('ГОБЛ').match).toBe('убить гоблина');
    expect(history.acceptReverseSearch()).toBe('убить гоблина');
  });

  it('acceptReverseSearch closes search mode and returns the selected match', () => {
    const history = historyWith(['look', 'kill goblin']);

    history.beginReverseSearch('draft');
    expect(history.updateReverseSearchQuery('gob').match).toBe('kill goblin');
    expect(history.isReverseSearchActive()).toBe(true);
    expect(history.acceptReverseSearch()).toBe('kill goblin');
    expect(history.isReverseSearchActive()).toBe(false);
    expect(history.acceptReverseSearch()).toBeNull();
  });

  it('cancelReverseSearch closes search mode and returns the original draft', () => {
    const history = historyWith(['look', 'kill goblin']);

    history.beginReverseSearch('draft command');
    expect(history.updateReverseSearchQuery('gob').match).toBe('kill goblin');
    expect(history.cancelReverseSearch()).toBe('draft command');
    expect(history.isReverseSearchActive()).toBe(false);
  });

  it('no-match reverse search returns null and accept does not return a stale command', () => {
    const history = historyWith(['look', 'kill goblin']);

    history.beginReverseSearch('draft');
    expect(history.updateReverseSearchQuery('gob').match).toBe('kill goblin');
    expect(history.updateReverseSearchQuery('dragon').match).toBeNull();
    expect(history.acceptReverseSearch()).toBeNull();
    expect(history.isReverseSearchActive()).toBe(false);
  });

  it('manual reset closes reverse search so the next search starts fresh', () => {
    const history = historyWith(['kill goblin old', 'look', 'say orc', 'kill goblin new']);

    history.beginReverseSearch('');
    expect(history.updateReverseSearchQuery('gob').match).toBe('kill goblin new');
    history.resetNavigation();

    expect(history.isReverseSearchActive()).toBe(false);
    history.beginReverseSearch('');
    expect(history.updateReverseSearchQuery('orc').match).toBe('say orc');
  });
});
