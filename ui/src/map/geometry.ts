import type { SeamTarget, SlimRoom } from './types';

// Axis transposition (see mapper.md §"Факты" and webmap/README.md):
//   in-game X = N/S axis, Y = W/E axis.
//   on screen (north up): screenX = coord `y`, screenY = coord `x`.
// Step deltas: N→x-1, S→x+1, E→y+1, W→y-1, U→l+1, D→l-1.
export function screenX(room: { x: number; y: number }): number {
  return room.y;
}
export function screenY(room: { x: number; y: number }): number {
  return room.x;
}

// Direction unit vectors in SCREEN space (dx, dy), matching the prototype's DIRV.
// Used to draw exit stubs. Screen dx follows coord-y (E=+), screen dy follows
// coord-x (S=+, i.e. north is up).
export const DIR_SCREEN_VEC: Record<string, [number, number]> = {
  N: [0, -1],
  S: [0, 1],
  E: [1, 0],
  W: [-1, 0],
  NE: [1, -1],
  NW: [-1, -1],
  SE: [1, 1],
  SW: [-1, 1],
};

// Coordinate delta of a single step in game (x,y,l) space, per the step-delta
// table above. Returns null for non-cardinal/non-vertical tokens.
export function stepDelta(dir: string): { dx: number; dy: number; dl: number } | null {
  switch (dir) {
    case 'N':
      return { dx: -1, dy: 0, dl: 0 };
    case 'S':
      return { dx: 1, dy: 0, dl: 0 };
    case 'E':
      return { dx: 0, dy: 1, dl: 0 };
    case 'W':
      return { dx: 0, dy: -1, dl: 0 };
    case 'U':
      return { dx: 0, dy: 0, dl: 1 };
    case 'D':
      return { dx: 0, dy: 0, dl: -1 };
    default:
      return null;
  }
}

// Parse an automaps seam string "targetZone|command|targetTag" into a target.
// Mirrors go/internal/mapimport parseSeam: requires >=3 pipe-delimited parts and
// an integer tag. Returns null on malformed input.
export function parseSeam(raw: string): SeamTarget | null {
  const parts = raw.split('|');
  if (parts.length < 3) return null;
  const tag = Number.parseInt(parts[2]!, 10);
  if (!Number.isFinite(tag) || parts[2]!.trim() === '') return null;
  return { zone: parts[0]!, command: parts[1]!, tag };
}

// The first seam target of a room (rooms usually carry a single seam).
export function firstSeam(room: SlimRoom): SeamTarget | null {
  if (!room.a || room.a.length === 0) return null;
  return parseSeam(room.a[0]!);
}

// Resolve a seam target to a concrete room in the target zone's room list, by
// matching Tag. Mirrors the backend's (zone, tag) seam resolution. `targetRooms`
// is the destination zone's slim rooms (already fetched). Returns null when the
// tag is not found (dangling seam).
export function resolveSeamTarget(target: SeamTarget, targetRooms: SlimRoom[]): SlimRoom | null {
  for (const r of targetRooms) {
    if (r.t === target.tag) return r;
  }
  return null;
}

// Bounds of a room list in SCREEN space (minx..maxx over screenX, miny..maxy over
// screenY). Empty list yields a zeroed box.
export interface Bounds {
  minx: number;
  miny: number;
  maxx: number;
  maxy: number;
}
export function computeBounds(rooms: SlimRoom[]): Bounds {
  if (rooms.length === 0) return { minx: 0, miny: 0, maxx: 0, maxy: 0 };
  let minx = Infinity;
  let miny = Infinity;
  let maxx = -Infinity;
  let maxy = -Infinity;
  for (const r of rooms) {
    const sx = screenX(r);
    const sy = screenY(r);
    if (sx < minx) minx = sx;
    if (sx > maxx) maxx = sx;
    if (sy < miny) miny = sy;
    if (sy > maxy) maxy = sy;
  }
  return { minx, miny, maxx, maxy };
}

// Sorted list of distinct levels (floors) present in a room list.
export function levelsOf(rooms: SlimRoom[]): number[] {
  const set = new Set<number>();
  for (const r of rooms) set.add(r.l);
  return [...set].sort((a, b) => a - b);
}

// Pick the most-populated level as the default active floor. Returns 0 for an
// empty list. On a tie, the lower level wins (stable).
export function defaultLevel(rooms: SlimRoom[]): number {
  if (rooms.length === 0) return 0;
  const count = new Map<number, number>();
  for (const r of rooms) count.set(r.l, (count.get(r.l) || 0) + 1);
  let best = rooms[0]!.l;
  let bestCount = -1;
  for (const lvl of [...count.keys()].sort((a, b) => a - b)) {
    const c = count.get(lvl)!;
    if (c > bestCount) {
      bestCount = c;
      best = lvl;
    }
  }
  return best;
}

// Case-insensitive substring search over hints across a room list. Returns the
// matching rooms in list order.
export function searchByHint(rooms: SlimRoom[], query: string): SlimRoom[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  return rooms.filter((r) => r.h.toLowerCase().includes(q));
}

// Resolve a target zone name against a set of known zone names, tolerating
// partial matches (as the prototype's findZone did) so NFC/spelling drift or
// truncation still lands. Exact match wins; else the first zone that contains or
// is contained by the target.
export function findZoneName(target: string, zoneNames: string[]): string | null {
  if (zoneNames.includes(target)) return target;
  return zoneNames.find((z) => z.includes(target) || target.includes(z)) ?? null;
}

// A zone entry from GET /api/map-sets/{id}/zones — an OBJECT `{zone, room_count}`,
// not a bare string. parseZoneList normalizes the response defensively (tolerates
// either shape) so the zone <select> never renders "[object Object]".
export interface ZoneInfo {
  zone: string;
  room_count: number;
}

export function parseZoneList(raw: unknown): ZoneInfo[] {
  if (!Array.isArray(raw)) return [];
  const out: ZoneInfo[] = [];
  for (const z of raw) {
    if (typeof z === 'string') {
      if (z) out.push({ zone: z, room_count: 0 });
    } else if (z && typeof z === 'object') {
      const zone = String((z as { zone?: unknown }).zone ?? '');
      if (zone) out.push({ zone, room_count: Number((z as { room_count?: unknown }).room_count ?? 0) });
    }
  }
  return out;
}
