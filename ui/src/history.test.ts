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
});
