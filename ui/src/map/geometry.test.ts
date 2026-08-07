import { describe, expect, it } from 'vitest';

import {
  computeBounds,
  defaultLevel,
  findZoneName,
  seamsOf,
  levelsOf,
  parseZoneList,
  resolveSeamTarget,
  seamDirection,
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
    se: [],
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

describe('normalized seams', () => {
  it('returns every stored occurrence including an unresolved seam', () => {
    expect(seamsOf(room({ se: [] }))).toEqual([]);
    expect(
      seamsOf(
        room({
          se: [
            { o: 0, k: 'seam', r: 'parsed', p: true, tz: 'broken', c: '' },
            { o: 1, k: 'seam', r: 'parsed', p: true, tz: 'Восточные ворота', tt: 49, c: 'на восток' },
            { o: 2, k: 'seam', r: 'manual', p: true, tz: 'Северная дорога', tt: 73, c: 'на север' },
          ],
        }),
      ),
    ).toEqual([
      { zone: 'broken', command: '', tag: null, source: 'parsed', passable: true, order: 0, to: undefined },
      { zone: 'Восточные ворота', command: 'на восток', tag: 49, source: 'parsed', passable: true, order: 1, to: undefined },
      { zone: 'Северная дорога', command: 'на север', tag: 73, source: 'manual', passable: true, order: 2, to: undefined },
    ]);
  });

  it('ignores conflicting legacy automaps data when normalized edges exist', () => {
    const conflicting = {
      ...room({ se: [{ o: 4, k: 'seam', r: 'manual', p: true, tz: 'Authoritative', tt: 9, c: 'portal' }] }),
      a: ['Wrong|north|1'],
    } as SlimRoom & { a: string[] };
    expect(seamsOf(conflicting)).toMatchObject([
      { zone: 'Authoritative', command: 'portal', tag: 9, source: 'manual', order: 4 },
    ]);
  });
});

describe('resolveSeamTarget', () => {
  it('does not infer a dead target from legacy tag metadata', () => {
    const target = { zone: 'Дороги', command: 'go', tag: 49, source: 'parsed', passable: true, order: 0 };
    const rooms = [room({ z: 'Дороги', t: 12 }), room({ z: 'Дороги', t: 49, x: 7, y: 2 })];
    expect(resolveSeamTarget(target, rooms)).toBeNull();
  });
  it('returns null for a dangling seam (tag absent)', () => {
    const target = { zone: 'Дороги', command: 'go', tag: 999, source: 'parsed', passable: true, order: 0 };
    expect(resolveSeamTarget(target, [room({ z: 'Дороги', t: 1 })])).toBeNull();
  });
  it('uses the resolved coordinate for same-zone portals before tag matching', () => {
    const target = { zone: 'A', command: 'portal', tag: 1, source: 'manual', passable: true, order: 3,
      to: { zone: 'A', x: 8, y: 9, l: 2 } };
    expect(resolveSeamTarget(target, [room({ z: 'A', t: 1, x: 0, y: 0 }), room({ z: 'A', t: 1, x: 8, y: 9, l: 2 })])?.x).toBe(8);
  });
});

describe('seamDirection', () => {
  it('mirrors backend movement words and final-token seam parsing', () => {
    expect(seamDirection('на восток')).toBe('E');
    expect(seamDirection(' подняться ')).toBe('U');
    expect(seamDirection('GO NORTH')).toBe('N');
    expect(seamDirection('teleport')).toBe('');
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
