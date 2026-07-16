import { describe, expect, it } from 'vitest';

import {
  computeBounds,
  defaultLevel,
  findZoneName,
  firstSeam,
  levelsOf,
  parseZoneList,
  parseSeam,
  resolveSeamTarget,
  screenX,
  screenY,
  searchByHint,
  stepDelta,
} from './geometry';
import type { SlimRoom } from './types';

function room(partial: Partial<SlimRoom>): SlimRoom {
  return {
    z: 'Z',
    t: null,
    h: '',
    x: 0,
    y: 0,
    l: 0,
    e: [],
    d: [],
    ch: 0,
    a: [],
    p: false,
    i: null,
    s: false,
    dx: 0,
    dy: 0,
    dl: 0,
    img: 0,
    ...partial,
  };
}

describe('axis transposition', () => {
  it('screenX = coord y, screenY = coord x', () => {
    const r = room({ x: 3, y: -5 });
    expect(screenX(r)).toBe(-5);
    expect(screenY(r)).toBe(3);
  });
});

describe('stepDelta', () => {
  it('follows N→x-1 S→x+1 E→y+1 W→y-1 U→l+1 D→l-1', () => {
    expect(stepDelta('N')).toEqual({ dx: -1, dy: 0, dl: 0 });
    expect(stepDelta('S')).toEqual({ dx: 1, dy: 0, dl: 0 });
    expect(stepDelta('E')).toEqual({ dx: 0, dy: 1, dl: 0 });
    expect(stepDelta('W')).toEqual({ dx: 0, dy: -1, dl: 0 });
    expect(stepDelta('U')).toEqual({ dx: 0, dy: 0, dl: 1 });
    expect(stepDelta('D')).toEqual({ dx: 0, dy: 0, dl: -1 });
  });
  it('returns null for unknown tokens', () => {
    expect(stepDelta('X')).toBeNull();
    expect(stepDelta('')).toBeNull();
  });
});

describe('parseSeam', () => {
  it('parses "zone|command|tag"', () => {
    expect(parseSeam('Море Сирриона|на восток|163')).toEqual({
      zone: 'Море Сирриона',
      command: 'на восток',
      tag: 163,
    });
  });
  it('rejects malformed seams', () => {
    expect(parseSeam('only|two')).toBeNull();
    expect(parseSeam('zone|cmd|notanumber')).toBeNull();
    expect(parseSeam('zone|cmd|')).toBeNull();
  });
  it('firstSeam returns null when no seams', () => {
    expect(firstSeam(room({ a: [] }))).toBeNull();
    expect(firstSeam(room({ a: ['Дороги|идти|49'] }))).toEqual({
      zone: 'Дороги',
      command: 'идти',
      tag: 49,
    });
  });
});

describe('resolveSeamTarget', () => {
  it('finds the room with matching tag in the target zone', () => {
    const target = { zone: 'Дороги', command: 'go', tag: 49 };
    const rooms = [room({ z: 'Дороги', t: 12 }), room({ z: 'Дороги', t: 49, x: 7, y: 2 })];
    const hit = resolveSeamTarget(target, rooms);
    expect(hit?.x).toBe(7);
    expect(hit?.y).toBe(2);
  });
  it('returns null for a dangling seam (tag absent)', () => {
    const target = { zone: 'Дороги', command: 'go', tag: 999 };
    expect(resolveSeamTarget(target, [room({ z: 'Дороги', t: 1 })])).toBeNull();
  });
});

describe('bounds and levels', () => {
  it('computeBounds uses screen space', () => {
    // room A: x=2,y=1 -> sx=1, sy=2 ; room B: x=-1,y=4 -> sx=4, sy=-1
    const b = computeBounds([room({ x: 2, y: 1 }), room({ x: -1, y: 4 })]);
    expect(b).toEqual({ minx: 1, miny: -1, maxx: 4, maxy: 2 });
  });
  it('empty bounds are zeroed', () => {
    expect(computeBounds([])).toEqual({ minx: 0, miny: 0, maxx: 0, maxy: 0 });
  });
  it('levelsOf returns sorted distinct levels', () => {
    expect(levelsOf([room({ l: 2 }), room({ l: 0 }), room({ l: 2 }), room({ l: 1 })])).toEqual([0, 1, 2]);
  });
  it('defaultLevel picks the most populated floor, lower level on tie', () => {
    expect(defaultLevel([room({ l: 0 }), room({ l: 1 }), room({ l: 1 })])).toBe(1);
    // tie between 0 and 1 -> lower wins
    expect(defaultLevel([room({ l: 0 }), room({ l: 1 })])).toBe(0);
    expect(defaultLevel([])).toBe(0);
  });
});

describe('searchByHint', () => {
  it('is case-insensitive substring over hints', () => {
    const rooms = [room({ h: 'Банк Утехи' }), room({ h: 'Оружейная лавка' }), room({ h: 'банковский свод' })];
    const hits = searchByHint(rooms, 'банк');
    expect(hits.map((r) => r.h)).toEqual(['Банк Утехи', 'банковский свод']);
  });
  it('empty query yields nothing', () => {
    expect(searchByHint([room({ h: 'x' })], '  ')).toEqual([]);
  });
});

describe('findZoneName', () => {
  const zones = ['Утеха', 'Дороги вокруг Балифора', 'Балифор - Лесок'];
  it('exact match wins', () => {
    expect(findZoneName('Утеха', zones)).toBe('Утеха');
  });
  it('partial/containment match falls back (first containing zone wins)', () => {
    expect(findZoneName('Дороги вокруг Балифора и дальше', zones)).toBe('Дороги вокруг Балифора');
    // "Балифор" is a substring of the first zone that contains it, so array
    // order decides — this documents the tolerant-but-first-match behavior.
    expect(findZoneName('Балифор', zones)).toBe('Дороги вокруг Балифора');
    expect(findZoneName('Лесок', zones)).toBe('Балифор - Лесок');
  });
  it('returns null when nothing matches', () => {
    expect(findZoneName('Кринн', zones)).toBeNull();
  });
});

describe('parseZoneList', () => {
  it('extracts zone names from the object-shaped API response', () => {
    // GET /api/map-sets/{id}/zones returns [{zone, room_count}], not string[].
    const raw = [
      { zone: 'Балифор', room_count: 227 },
      { zone: 'Хилло', room_count: 162 },
    ];
    expect(parseZoneList(raw)).toEqual([
      { zone: 'Балифор', room_count: 227 },
      { zone: 'Хилло', room_count: 162 },
    ]);
    // Names must be plain strings (guards against the "[object Object]" bug).
    expect(parseZoneList(raw).map((z) => z.zone)).toEqual(['Балифор', 'Хилло']);
  });
  it('tolerates a bare string[] shape and drops empty/garbage entries', () => {
    expect(parseZoneList(['A', '', 'B'])).toEqual([
      { zone: 'A', room_count: 0 },
      { zone: 'B', room_count: 0 },
    ]);
    expect(parseZoneList([{ zone: '' }, { room_count: 5 }, null])).toEqual([]);
    expect(parseZoneList(null)).toEqual([]);
    expect(parseZoneList('nope')).toEqual([]);
  });
});
