// Log date-range helpers.
//
// The Logs date pickers (`from`/`to`) hold a bare calendar date like
// "2026-07-07". A picked date means that day in the VIEWER's LOCAL timezone, so
// we convert it to the correct UTC instant by parsing a date-time string WITHOUT
// a `Z` (which the browser interprets in its local zone) and then serializing
// with `.toISOString()`. An empty date yields '' (an open bound). Shared by log
// browsing, the .txt download, and the colored HTML export so all three agree.

/** localDayStartISO returns the UTC instant of local midnight on `d` ('' if empty). */
export function localDayStartISO(d: string): string {
  if (!d) return '';
  return new Date(`${d}T00:00:00`).toISOString();
}

/** localDayEndISO returns the UTC instant of the inclusive end of local day `d` ('' if empty). */
export function localDayEndISO(d: string): string {
  if (!d) return '';
  return new Date(`${d}T23:59:59.999`).toISOString();
}

/**
 * localDateTimeISO converts a `datetime-local` value ("2026-07-07T21:30", the
 * viewer's LOCAL wall-clock, no zone) to its UTC instant. Used by the export
 * pickers for precise, to-the-minute ranges (e.g. a single raid). Parsing a
 * string without a `Z` interprets it in the local zone, matching the pickers.
 * '' stays '' (an open bound).
 */
export function localDateTimeISO(dt: string): string {
  if (!dt) return '';
  const parsed = new Date(dt);
  if (Number.isNaN(parsed.getTime())) return ''; // malformed (e.g. from a hand-edited URL) → open bound
  return parsed.toISOString();
}
