import type { Confidence, PlayerPosition } from "./types";

// Pure logic for the map cell context menu. The DOM rendering lives in the
// controller (pane.ts); this module holds the decision/formatting logic that is
// unit-testable without a canvas or a live DOM.

// Confidence glyphs (mirror CONF_GLYPH in pane.ts; duplicated intentionally so
// this pure module has no DOM dependency).
const CONF_GLYPH: Record<Confidence, string> = {
  green: "🟢",
  yellow: "🟡",
  red: "🔴",
};

// Human-readable status header for the menu's top row.
export function statusHeaderText(pos: PlayerPosition | null): string {
  if (!pos) return "⚪ нет позиции";
  const glyph = CONF_GLYPH[pos.confidence] ?? "⚪";
  const parts = [`${glyph} ${pos.confidence}`];
  if (pos.reason) parts.push(pos.reason);
  return parts.join(" · ");
}

// A yellow position carries an exit diff worth surfacing (added-live and/or
// removed-map) only when the tracker is yellow AND at least one side is non-empty.
export function hasExitDiff(pos: PlayerPosition | null): boolean {
  if (!pos || pos.confidence !== "yellow") return false;
  return (
    (pos.exitsAddedLive?.length ?? 0) > 0 ||
    (pos.exitsRemovedMap?.length ?? 0) > 0
  );
}

// Formats the exit diff like "выходы: +U live / −N map". Returns '' when there is
// nothing to show (caller should gate on hasExitDiff first, but this is safe).
export function formatExitDiff(pos: PlayerPosition | null): string {
  if (!pos) return "";
  const added = pos.exitsAddedLive ?? [];
  const removed = pos.exitsRemovedMap ?? [];
  if (added.length === 0 && removed.length === 0) return "";
  const segs: string[] = [];
  if (added.length > 0) segs.push(`+${added.join("+")} live`);
  if (removed.length > 0) segs.push(`−${removed.join("−")} map`);
  return `выходы: ${segs.join(" / ")}`;
}

// Whether "Идти сюда" (and its send/insert sub-actions) should be enabled: the
// tracker must have a live, non-lost position to route FROM. Clicking a cell is
// meaningless without a known start.
export function canRoute(pos: PlayerPosition | null): boolean {
  return !!pos && pos.valid && pos.confidence !== "red";
}

// Whether the clicked cell IS the player's current cell (a no-op "go here").
export function isCurrentCell(
  pos: PlayerPosition | null,
  cell: { z: string; x: number; y: number; l: number },
): boolean {
  return (
    !!pos &&
    pos.valid &&
    pos.zone === cell.z &&
    pos.x === cell.x &&
    pos.y === cell.y &&
    pos.l === cell.l
  );
}

// The "update this cell from live state" button POSTs this add/remove payload,
// derived straight from the tracker's diff: add what the game shows but the map
// lacks; remove what the map claims but the game did not show.
export function cellPatchPayload(pos: PlayerPosition | null): {
  add_exits: string[];
  remove_exits: string[];
} {
  return {
    add_exits: pos?.exitsAddedLive ?? [],
    remove_exits: pos?.exitsRemovedMap ?? [],
  };
}

// Given the endpoint's directions, split the walk at the first door-flagged hop
// so "send to server" does not blindly batch through a (presumed-)door. Returns
// the leading run of directions safe to send in one batch, and whether it was
// truncated at a door (so the caller can hint the user to open it). `doorAt` is
// the 0-based index of the first door hop, or -1 when there is none.
export function walkUpToFirstDoor(
  directions: string[],
  doorAt: number,
): { walk: string[]; truncatedAtDoor: boolean } {
  if (doorAt < 0 || doorAt >= directions.length) {
    return { walk: directions.slice(), truncatedAtDoor: false };
  }
  // Send everything BEFORE the door hop; the door hop itself is withheld.
  return { walk: directions.slice(0, doorAt), truncatedAtDoor: true };
}
