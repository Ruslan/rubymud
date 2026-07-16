import { describe, expect, it } from 'vitest';

import { parseRoomPosition } from './wsPosition';

describe('parseRoomPosition', () => {
  it('maps a valid green position with all fields', () => {
    const pos = parseRoomPosition({
      type: 'room_position',
      confidence: 'green',
      pending_moves: 2,
      position_valid: true,
      zone: 'Утеха',
      room_x: 3,
      room_y: -4,
      room_l: 1,
      is_dt: true,
      pipe: false,
      room_hint: 'Банк Утехи',
    });
    expect(pos).toEqual({
      valid: true,
      zone: 'Утеха',
      x: 3,
      y: -4,
      l: 1,
      confidence: 'green',
      pendingMoves: 2,
      reason: undefined,
      hint: 'Банк Утехи',
      isDT: true,
      pipe: false,
    });
  });

  it('applies omitempty defaults (missing coords/flags → 0/false)', () => {
    // A "lost" red broadcast typically omits zero coords and false flags.
    const pos = parseRoomPosition({ type: 'room_position', confidence: 'red', position_reason: 'lost — mismatch' });
    expect(pos.valid).toBe(false);
    expect(pos.x).toBe(0);
    expect(pos.y).toBe(0);
    expect(pos.l).toBe(0);
    expect(pos.confidence).toBe('red');
    expect(pos.pendingMoves).toBe(0);
    expect(pos.reason).toBe('lost — mismatch');
    expect(pos.isDT).toBe(false);
    expect(pos.pipe).toBe(false);
  });

  it('normalizes an unknown/empty confidence to red', () => {
    expect(parseRoomPosition({}).confidence).toBe('red');
    expect(parseRoomPosition({ confidence: 'bogus' }).confidence).toBe('red');
    expect(parseRoomPosition({ confidence: 'yellow' }).confidence).toBe('yellow');
  });
});
