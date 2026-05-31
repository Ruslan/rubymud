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

  it('reverse search returns the newest command containing the query substring', () => {
    const history = historyWith([
      'осмотреться',
      'убить гоблина старого',
      'сказать про гоблина',
      'убить гоблина свежего',
    ]);

    expect(history.reverseSearch('гобл')).toBe('убить гоблина свежего');
  });

  it('repeated reverse search moves to older substring matches and clamps at oldest', () => {
    const history = historyWith([
      'убить гоблина старого',
      'убить орка',
      'сказать про гоблина',
      'убить гоблина свежего',
    ]);

    expect(history.reverseSearch('гобл')).toBe('убить гоблина свежего');
    expect(history.reverseSearch('убить гоблина свежего')).toBe('сказать про гоблина');
    expect(history.reverseSearch('сказать про гоблина')).toBe('убить гоблина старого');
    expect(history.reverseSearch('убить гоблина старого')).toBe('убить гоблина старого');
  });

  it('reverse search is case-insensitive', () => {
    const history = historyWith(['Kill Goblin', 'look', 'say GOBLIN']);

    expect(history.reverseSearch('goblin')).toBe('say GOBLIN');
    expect(history.reverseSearch('say GOBLIN')).toBe('Kill Goblin');
  });

  it('acceptSearch closes search mode and returns the selected command', () => {
    const history = historyWith(['look', 'kill goblin']);

    expect(history.reverseSearch('gob')).toBe('kill goblin');
    expect(history.isSearchActive()).toBe(true);
    expect(history.acceptSearch()).toBe('kill goblin');
    expect(history.isSearchActive()).toBe(false);
    expect(history.acceptSearch()).toBeNull();
  });

  it('cancelSearch closes search mode and restores the original draft', () => {
    const history = historyWith(['look', 'kill goblin']);

    expect(history.reverseSearch('draft gob')).toBe('draft gob');
    expect(history.cancelSearch()).toBe('draft gob');
    expect(history.isSearchActive()).toBe(false);
    expect(history.cancelSearch()).toBeNull();
  });

  it('manual reset closes reverse search so the next search uses the new query from newest', () => {
    const history = historyWith([
      'kill goblin old',
      'look',
      'say orc',
      'kill goblin new',
    ]);

    expect(history.reverseSearch('gob')).toBe('kill goblin new');
    history.resetNavigation();

    expect(history.isSearchActive()).toBe(false);
    expect(history.reverseSearch('orc')).toBe('say orc');
  });
});
