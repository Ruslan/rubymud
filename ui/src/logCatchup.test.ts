import { describe, expect, it } from 'vitest';
import { safeCatchupCursor } from './logCatchup';

describe('safeCatchupCursor', () => {
  it('uses known restore cursor directly for first post-restore catch-up', () => {
    expect(safeCatchupCursor(1002, 1000, true)).toBe(1000);
    expect(safeCatchupCursor(20, 42, true)).toBe(42);
  });

  it('allows trusted after_id=0 only when server provided a known zero restore cursor', () => {
    expect(safeCatchupCursor(0, 0, true)).toBe(0);
    expect(safeCatchupCursor(Number.NaN, 0, true)).toBe(0);
  });

  it('does not allow blind catch-up from after_id=0 without a known restore cursor', () => {
    expect(safeCatchupCursor(0, 0, false)).toBeNull();
    expect(safeCatchupCursor(Number.NaN, 0, false)).toBeNull();
  });

  it('uses rendered latest id when restore cursor is unknown', () => {
    expect(safeCatchupCursor(50, 42, false)).toBe(50);
    expect(safeCatchupCursor(20, 0, false)).toBe(20);
  });
});
