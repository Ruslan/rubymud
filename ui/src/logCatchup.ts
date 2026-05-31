export function safeCatchupCursor(renderedLatestID: number, restoreCursor: number, restoreCursorKnown = false): number | null {
  const rendered = Number.isFinite(renderedLatestID) ? Math.max(0, Math.floor(renderedLatestID)) : 0;
  const restored = Number.isFinite(restoreCursor) ? Math.max(0, Math.floor(restoreCursor)) : 0;
  if (restoreCursorKnown) return restored;
  return rendered > 0 ? rendered : null;
}
