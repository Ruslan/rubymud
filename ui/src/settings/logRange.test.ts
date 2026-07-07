// Pin the timezone BEFORE importing anything that touches Date so the
// local-day conversions are deterministic (America/New_York = EDT, UTC-4, in July).
process.env.TZ = 'America/New_York';

import { describe, it, expect } from 'vitest';
import { localDayStartISO, localDayEndISO, localDateTimeISO } from './logRange';

describe('log range local-day helpers (America/New_York, EDT)', () => {
  it('localDayStartISO maps a picked date to local midnight in UTC', () => {
    expect(localDayStartISO('2026-07-07')).toBe('2026-07-07T04:00:00.000Z');
  });

  it('localDayEndISO maps a picked date to the inclusive end of the local day in UTC', () => {
    expect(localDayEndISO('2026-07-07')).toBe('2026-07-08T03:59:59.999Z');
  });

  it('localDateTimeISO maps a picked local wall-clock minute to UTC', () => {
    expect(localDateTimeISO('2026-07-07T21:30')).toBe('2026-07-08T01:30:00.000Z');
  });

  it('treats empty input as an open bound', () => {
    expect(localDayStartISO('')).toBe('');
    expect(localDayEndISO('')).toBe('');
    expect(localDateTimeISO('')).toBe('');
  });

  it('localDateTimeISO treats a malformed value (hand-edited URL) as an open bound', () => {
    expect(localDateTimeISO('not-a-date')).toBe('');
  });
});
