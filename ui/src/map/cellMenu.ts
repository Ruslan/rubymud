import type { Confidence, LiveRoom, PlayerPosition, SlimRoom } from "./types";

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

// --- «Записать комнату» autorecord -----------------------------------------

// The candidate "ghost" cell: the player stands at a valid coord on the DISPLAYED
// zone that has NO room in the map (the tracker's 🟡 "edge/cell not in map"
// state). That is the cell the record button would draw, so the pane renders it
// as a dashed outline. Returns null whenever there is nothing to offer — no
// position, another zone, or a real room already there.
export function ghostCandidate(
  pos: PlayerPosition | null,
  zone: string,
  rooms: SlimRoom[],
): { x: number; y: number; l: number } | null {
  if (!pos || !pos.valid) return null;
  if (!zone || pos.zone !== zone) return null;
  const occupied = rooms.some(
    (r) => r.x === pos.x && r.y === pos.y && r.l === pos.l,
  );
  if (occupied) return null;
  return { x: pos.x, y: pos.y, l: pos.l };
}

// The record menu item's label / enabled state / disabled-reason for one cell.
// Two cases, ONE action (the backend upserts either way):
//   - the cell has no room  -> «Записать комнату сюда» (draw a new room);
//   - the cell already has one -> «Обновить комнату из живого» (refresh an
//     outdated room), distinct from the exits-only «Обновить клетку из живого
//     стейта» that the yellow diff offers.
// It is enabled ONLY on the cell the player is standing in and only once a live
// room block has been seen: the live text describes where the player IS, so
// recording it onto any other cell would write the wrong room.
export function recordAction(
  pos: PlayerPosition | null,
  live: LiveRoom | null,
  cell: { z: string; x: number; y: number; l: number },
  cellExists: boolean,
): { label: string; enabled: boolean; reason: string } {
  const label = cellExists
    ? "Обновить комнату из живого"
    : "Записать комнату сюда";
  if (!pos || !pos.valid) {
    return { label, enabled: false, reason: "нет позиции — сначала «Я здесь»" };
  }
  if (!isCurrentCell(pos, cell)) {
    return {
      label,
      enabled: false,
      reason: "можно записать только клетку, в которой вы стоите",
    };
  }
  // 🔴 lost: the tracker still reports a coord, but it is the LAST-KNOWN one —
  // the live text is the room we were teleported INTO. Recording would overwrite
  // a good, unrelated room. (The backend refuses this too; this is the UX half.)
  if (pos.confidence === "red") {
    return {
      label,
      enabled: false,
      reason: "позиция потеряна (🔴) — живая комната описывает не эту клетку",
    };
  }
  // The tracker is merely HOLDING this coord (ambiguous match / hint-less pipe
  // event); the live text describes some other cell.
  if (pos.eventDescribesCell === false) {
    return {
      label,
      enabled: false,
      reason: "живая комната не соответствует этой клетке — сначала «Я здесь»",
    };
  }
  if (!live || !live.hint) {
    return {
      label,
      enabled: false,
      reason: "живая комната ещё не получена — сделайте «осмотреть»",
    };
  }
  // Recording re-anchors, and anchoring flushes the pending queue — doing that
  // mid-speedwalk would silently drop the in-flight moves.
  if (pos.pendingMoves > 0) {
    return {
      label,
      enabled: false,
      reason: `идёт перемещение (${pos.pendingMoves} шаг(ов) в пути) — дождитесь конца`,
    };
  }
  return { label, enabled: true, reason: "" };
}

// --- the top-bar «Записать комнату» button ---------------------------------

// Fixed label. It does NOT switch between "record"/"update" the way the cell
// menu's item does: a toolbar button whose caption changes under the cursor is
// hard to aim at, and the create-vs-update distinction is in the tooltip instead.
export const recordButtonLabel = "Записать комнату";

// State of the top-bar record button. It is the SAME write as the cell menu's
// record item — aimed, unconditionally, at the cell the player is standing in —
// so its gate DELEGATES to recordAction rather than restating the rules. Two
// entry points, one decision.
//
// The button is always VISIBLE and goes disabled when the write cannot land,
// carrying the reason in `title`: `map-record-room` refuses on 🔴, on a hint-less
// live block and on a non-empty pending queue, and a button that looks live and
// then errors is worse than the menu it replaces. 🟡 is deliberately NOT a
// refusal — it is the normal state for recording virgin territory.
//
// `cellExists` is tri-state: true/false when the player's zone is loaded and we
// know whether a room occupies the coord, null when we cannot tell (zone not
// fetched). It affects the wording only, never the gate.
export function recordButtonState(
  pos: PlayerPosition | null,
  live: LiveRoom | null,
  cellExists: boolean | null,
): {
  label: string;
  enabled: boolean;
  title: string;
  cell: { z: string; x: number; y: number; l: number } | null;
} {
  const label = recordButtonLabel;
  if (!pos || !pos.valid) {
    return {
      label,
      enabled: false,
      title: `${label}: нет позиции — сначала «Я здесь» в меню клетки`,
      cell: null,
    };
  }
  const cell = { z: pos.zone, x: pos.x, y: pos.y, l: pos.l };
  const act = recordAction(pos, live, cell, cellExists === true);
  if (!act.enabled) {
    return { label, enabled: false, title: `${label}: ${act.reason}`, cell };
  }
  const verb =
    cellExists === true
      ? "обновит"
      : cellExists === false
        ? "создаст"
        : "запишет";
  const at = `${cell.z} ${cell.x},${cell.y},${cell.l}`;
  return {
    label,
    enabled: true,
    title: `${verb} клетку {${at}} из живого блока «${live?.hint ?? ""}»`,
    cell,
  };
}

// Canonical direction letters, mirroring mapimport.dirMap (Go) so the UI parses a
// live exits display string exactly the way the backend will when it writes it.
const DIR_WORDS: Record<string, string> = {
  n: "N",
  с: "N",
  север: "N",
  north: "N",
  s: "S",
  ю: "S",
  юг: "S",
  south: "S",
  e: "E",
  в: "E",
  восток: "E",
  east: "E",
  w: "W",
  з: "W",
  запад: "W",
  west: "W",
  u: "U",
  вв: "U",
  вверх: "U",
  up: "U",
  d: "D",
  вн: "D",
  вниз: "D",
  down: "D",
};

// Parse a live exits DISPLAY string ("(N) E S W", "none", "с ю вверх") into
// canonical direction letters. Door parentheses are irrelevant here — the tokens
// inside them are still exits. Mirrors Go's mapimport.normExits token scan, so the
// preview shows exactly what the write will record.
export function parseLiveExits(exits: string): string[] {
  const out: string[] = [];
  for (const tok of exits.toLowerCase().match(/[а-яёa-z0-9]+/g) ?? []) {
    const d = DIR_WORDS[tok];
    if (d && !out.includes(d)) out.push(d);
  }
  return out;
}

// What a record is about to write, for the confirm-before-you-write preview. For
// an UPDATE it also diffs the live exits against the cell's CURRENT exits, so the
// operator sees which edges the write adds or drops — the surplus exits are what
// can fabricate routing edges into unrelated neighbours. Returns null when there
// is nothing to preview.
export function recordPreview(
  live: LiveRoom | null,
  room: SlimRoom | null,
): {
  hint: string;
  exits: string[];
  added: string[];
  removed: string[];
} | null {
  if (!live || !live.hint) return null;
  const exits = parseLiveExits(live.exits);
  if (!room) return { hint: live.hint, exits, added: [], removed: [] };
  const have = room.e ?? [];
  return {
    hint: live.hint,
    exits,
    added: exits.filter((d) => !have.includes(d)),
    removed: have.filter((d) => !exits.includes(d)),
  };
}

// --- status-line stickiness -------------------------------------------------

// How long a "sticky" status message is protected from being cleared, in ms.
// It only has to outlive the round trip the write itself triggers (write ->
// rooms_changed broadcast -> reload -> clear), which is milliseconds; the window
// is generous so a slow reload cannot swallow the message either.
export const stickyStatusMs = 12000;

// Route confirmations are useful only long enough to read. Count Unicode code
// points rather than UTF-16 units so Cyrillic and emoji get the same per-symbol
// reading budget as ASCII. The floor avoids a distracting flash for short text;
// the ceiling keeps an accidentally huge route summary from lingering forever.
export function statusAutoHideDelay(text: string): number {
  const delay = 2_000 + Array.from(text).length * 60;
  return Math.min(20_000, Math.max(5_000, delay));
}

// Decide what a setStatus() call should do, given any sticky message in effect.
//
// The problem this solves: a record/undo POSTs, then reports its outcome in the
// status line — but that same write makes the server broadcast rooms_changed,
// which calls reload(), which clears the status. The user therefore never sees
// the neighbour-touches report or the undo label: the write looks silent.
//
// Rules: a sticky message starts its window; a CLEAR ("") inside that window is
// ignored (that is the reload talking); any other non-empty message wins
// immediately and ends the window (a real error or a later action must not be
// suppressed).
export function nextStatusState(
  next: string,
  sticky: boolean,
  stickyAt: number,
  now: number,
  windowMs = stickyStatusMs,
): { apply: boolean; stickyAt: number } {
  if (sticky) return { apply: true, stickyAt: now };
  const inWindow = stickyAt > 0 && now - stickyAt < windowMs;
  if (next === "" && inWindow) return { apply: false, stickyAt };
  return { apply: true, stickyAt: 0 };
}

// --- context-menu placement ------------------------------------------------

// Where to put the cell menu so the WHOLE menu stays visible. All coordinates are
// relative to the pane root (the menu's containing block AND the box that clips
// it — .column/.panes-container use overflow:hidden, so a menu that escapes the
// pane would be cut off, not merely overlap).
//
// Per axis: keep the menu below/right of the click; if that overflows, FLIP it to
// the other side of the click; if the flip does not fit either, slide it back
// inside; and as a last resort pin it at 0 (a menu wider than the pane).
// `maxHeight` is the tallest the menu may be inside the bounds — the caller
// applies it with overflow-y:auto so a menu taller than the pane SCROLLS instead
// of dropping items off the edge (it grows as this menu adds items).
export function clampMenuPosition(
  click: { x: number; y: number },
  menu: { width: number; height: number },
  bounds: { width: number; height: number },
  margin = 6,
): { left: number; top: number; maxHeight: number } {
  const maxHeight = Math.max(0, bounds.height - margin * 2);
  return {
    left: fitAxis(click.x, menu.width, bounds.width, margin),
    // Position using the EFFECTIVE height: once max-height caps a tall menu, the
    // rendered box is shorter, so flipping/clamping must use the capped value or
    // it would push a scrolling menu needlessly far up.
    top: fitAxis(
      click.y,
      Math.min(menu.height, maxHeight),
      bounds.height,
      margin,
    ),
    maxHeight,
  };
}

// One axis of clampMenuPosition: prefer `click`, flip to `click - size` on
// overflow, then clamp into [0, bound - size] (0 when the box cannot fit at all).
function fitAxis(
  click: number,
  size: number,
  bound: number,
  margin: number,
): number {
  let v = click;
  if (v + size > bound - margin) {
    const flipped = click - size;
    v = flipped >= margin ? flipped : bound - margin - size;
  }
  return Math.max(0, Math.min(v, Math.max(0, bound - size)));
}
