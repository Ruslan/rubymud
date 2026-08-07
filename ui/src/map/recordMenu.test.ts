import { describe, expect, it } from "vitest";

import {
  clampMenuPosition,
  ghostCandidate,
  nextStatusState,
  parseLiveExits,
  recordAction,
  recordButtonLabel,
  recordButtonState,
  recordPreview,
  stickyStatusMs,
} from "./cellMenu";
import type { LiveRoom, PlayerPosition, SlimRoom } from "./types";

// Pure-logic coverage for the «Записать комнату» autorecord
// and for the cell-menu placement fix. Same DOM-free style as cellMenu.test.ts.

function pos(over: Partial<PlayerPosition> = {}): PlayerPosition {
  return {
    valid: true,
    zone: "Z",
    x: 1,
    y: 1,
    l: 0,
    confidence: "yellow",
    pendingMoves: 0,
    eventDescribesCell: true,
    ...over,
  };
}

function room(over: Partial<SlimRoom> = {}): SlimRoom {
  return {
    z: "Z",
    t: 1,
    h: "Комната",
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
    ...over,
  };
}

const live: LiveRoom = { hint: "Новая", desc: "prose", exits: "W E" };

describe("ghostCandidate", () => {
  it("offers the player's coord when no room is mapped there", () => {
    expect(ghostCandidate(pos(), "Z", [room({ x: 0, y: 0 })])).toEqual({
      x: 1,
      y: 1,
      l: 0,
    });
  });

  it("offers nothing when a real room already occupies the coord", () => {
    expect(ghostCandidate(pos(), "Z", [room({ x: 1, y: 1, l: 0 })])).toBeNull();
  });

  it("distinguishes floors — a room on another level does not occupy the cell", () => {
    expect(ghostCandidate(pos(), "Z", [room({ x: 1, y: 1, l: 3 })])).toEqual({
      x: 1,
      y: 1,
      l: 0,
    });
  });

  it("offers nothing without a position, or on a different zone", () => {
    expect(ghostCandidate(null, "Z", [])).toBeNull();
    expect(ghostCandidate(pos({ valid: false }), "Z", [])).toBeNull();
    expect(ghostCandidate(pos({ zone: "Other" }), "Z", [])).toBeNull();
    expect(ghostCandidate(pos(), "", [])).toBeNull();
  });
});

describe("recordAction", () => {
  const here = { z: "Z", x: 1, y: 1, l: 0 };

  it("labels an unmapped cell 'record' and a mapped one 'update'", () => {
    expect(recordAction(pos(), live, here, false).label).toBe(
      "Записать комнату сюда",
    );
    expect(recordAction(pos(), live, here, true).label).toBe(
      "Обновить комнату из живого",
    );
  });

  it("is enabled on the player's own cell once a live room is known", () => {
    const a = recordAction(pos(), live, here, false);
    expect(a.enabled).toBe(true);
    expect(a.reason).toBe("");
  });

  it("refuses a cell the player is not standing in", () => {
    const a = recordAction(pos(), live, { z: "Z", x: 5, y: 5, l: 0 }, true);
    expect(a.enabled).toBe(false);
    expect(a.reason).toContain("стоите");
  });

  it("refuses without a position at all", () => {
    expect(recordAction(null, live, here, false).enabled).toBe(false);
    expect(
      recordAction(pos({ valid: false }), live, here, false).reason,
    ).toContain("Я здесь");
  });

  it("refuses until a live room block has been seen", () => {
    expect(recordAction(pos(), null, here, false).enabled).toBe(false);
    const blank = recordAction(
      pos(),
      { hint: "", desc: "", exits: "" },
      here,
      false,
    );
    expect(blank.enabled).toBe(false);
    expect(blank.reason).toContain("живая комната");
  });
});

describe("clampMenuPosition", () => {
  const bounds = { width: 400, height: 300 };
  const menu = { width: 220, height: 160 };

  it("keeps the menu at the click when it fits", () => {
    const p = clampMenuPosition({ x: 20, y: 30 }, menu, bounds);
    expect(p).toMatchObject({ left: 20, top: 30 });
  });

  it("flips left of the cursor when it would overflow the right edge", () => {
    // 380 + 220 = 600 > 400 → flip to 380 - 220 = 160.
    const p = clampMenuPosition({ x: 380, y: 10 }, menu, bounds);
    expect(p.left).toBe(160);
    expect(p.left + menu.width).toBeLessThanOrEqual(bounds.width);
  });

  it("flips above the cursor when it would overflow the bottom edge", () => {
    // 290 + 160 = 450 > 300 → flip to 290 - 160 = 130.
    const p = clampMenuPosition({ x: 10, y: 290 }, menu, bounds);
    expect(p.top).toBe(130);
    expect(p.top + menu.height).toBeLessThanOrEqual(bounds.height);
  });

  it("slides back inside when neither side of the click has room", () => {
    // Click near the top-left but a menu too wide/tall to fit after a flip:
    // flipping would go negative, so it clamps to the far edge instead.
    const big = { width: 380, height: 280 };
    const p = clampMenuPosition({ x: 30, y: 20 }, big, bounds);
    expect(p.left).toBeGreaterThanOrEqual(0);
    expect(p.top).toBeGreaterThanOrEqual(0);
    expect(p.left + big.width).toBeLessThanOrEqual(bounds.width);
    expect(p.top + big.height).toBeLessThanOrEqual(bounds.height);
  });

  it("never returns a negative offset, even for a menu larger than the pane", () => {
    const huge = { width: 900, height: 700 };
    const p = clampMenuPosition({ x: 350, y: 280 }, huge, bounds);
    // Width cannot be helped (nothing scrolls horizontally) — pin at 0 rather
    // than let the left edge go negative and become unreachable.
    expect(p.left).toBe(0);
    // Height IS helped: max-height caps it, so it still fits inside the bounds.
    expect(p.top).toBeGreaterThanOrEqual(0);
    expect(p.top + p.maxHeight).toBeLessThanOrEqual(bounds.height);
  });

  it("caps max-height to the bounds so a tall menu scrolls instead of overflowing", () => {
    const tall = { width: 200, height: 5000 };
    const p = clampMenuPosition({ x: 10, y: 150 }, tall, bounds);
    expect(p.maxHeight).toBe(300 - 12); // bounds.height - 2 * margin
    expect(p.top).toBeGreaterThanOrEqual(0);
    expect(p.top + p.maxHeight).toBeLessThanOrEqual(bounds.height);
  });

  it("positions a capped tall menu by its EFFECTIVE (scrolled) height", () => {
    // The raw height (1000) would flip to a wildly negative top; using the capped
    // height (288) instead lands it at the margin, fully inside the pane.
    const tall = { width: 200, height: 1000 };
    const p = clampMenuPosition({ x: 10, y: 250 }, tall, bounds, 6);
    expect(p.top).toBe(6);
    expect(p.top + p.maxHeight).toBeLessThanOrEqual(bounds.height);
  });

  it("honours a custom margin", () => {
    const p = clampMenuPosition({ x: 300, y: 10 }, menu, bounds, 20);
    // 300 + 220 = 520 > 400 - 20 → flip to 80.
    expect(p.left).toBe(80);
    expect(p.maxHeight).toBe(300 - 40);
  });
});

// --- review round 1: guards that keep a record from corrupting a good room ---

describe("recordAction guards", () => {
  const here = { z: "Z", x: 1, y: 1, l: 0 };

  it("refuses while 🔴 lost — the coord is last-known, the text is elsewhere", () => {
    const a = recordAction(
      pos({ confidence: "red", eventDescribesCell: false }),
      live,
      here,
      true,
    );
    expect(a.enabled).toBe(false);
    expect(a.reason).toContain("🔴");
  });

  it("refuses when the event does not describe the held cell", () => {
    const a = recordAction(
      pos({ confidence: "yellow", eventDescribesCell: false }),
      live,
      here,
      true,
    );
    expect(a.enabled).toBe(false);
    expect(a.reason).toContain("не соответствует");
  });

  it("refuses mid-speedwalk, because the re-anchor would flush the queue", () => {
    const a = recordAction(
      pos({ eventDescribesCell: true, pendingMoves: 2 }),
      live,
      here,
      false,
    );
    expect(a.enabled).toBe(false);
    expect(a.reason).toContain("перемещение");
  });

  it("allows the normal case (settled, describing this cell)", () => {
    const a = recordAction(
      pos({ eventDescribesCell: true, pendingMoves: 0 }),
      live,
      here,
      false,
    );
    expect(a.enabled).toBe(true);
  });
});

describe("parseLiveExits", () => {
  it("parses latin letters with door parentheses", () => {
    expect(parseLiveExits("(N) E S W")).toEqual(["N", "E", "S", "W"]);
  });

  it("parses russian direction words", () => {
    expect(parseLiveExits("с ю вверх")).toEqual(["N", "S", "U"]);
  });

  it("yields nothing for a dead end", () => {
    expect(parseLiveExits("none")).toEqual([]);
    expect(parseLiveExits("")).toEqual([]);
  });

  it("de-duplicates repeated directions", () => {
    expect(parseLiveExits("N (N) n")).toEqual(["N"]);
  });
});

describe("recordPreview", () => {
  it("previews a create with just the live name and exits", () => {
    const pv = recordPreview({ hint: "Новая", desc: "", exits: "W E" }, null);
    expect(pv).toEqual({
      hint: "Новая",
      exits: ["W", "E"],
      added: [],
      removed: [],
    });
  });

  it("diffs an update against the cell's current exits", () => {
    const pv = recordPreview(
      { hint: "Первая", desc: "", exits: "S U" },
      room({ x: 1, y: 1, l: 0, e: ["S", "W"] }),
    );
    expect(pv?.added).toEqual(["U"]);
    expect(pv?.removed).toEqual(["W"]);
  });

  it("returns null with no live room to preview", () => {
    expect(recordPreview(null, null)).toBeNull();
    expect(recordPreview({ hint: "", desc: "", exits: "N" }, null)).toBeNull();
  });
});

// --- review round 2: the status message must survive its own write ----------

describe("nextStatusState", () => {
  const T0 = 1_000_000;

  it("applies a sticky message and starts the window", () => {
    const r = nextStatusState("Комната записана", true, 0, T0);
    expect(r).toEqual({ apply: true, stickyAt: T0 });
  });

  it("swallows the clear that the write's own reload triggers", () => {
    // record -> rooms_changed -> reload() -> setStatus("") a moment later.
    const r = nextStatusState("", false, T0, T0 + 200);
    expect(r.apply).toBe(false);
    expect(r.stickyAt).toBe(T0); // window still running
  });

  it("lets a real message override a sticky one immediately", () => {
    const r = nextStatusState("Failed to load zone: boom", false, T0, T0 + 200);
    expect(r).toEqual({ apply: true, stickyAt: 0 });
  });

  it("stops protecting once the window expires", () => {
    const r = nextStatusState("", false, T0, T0 + stickyStatusMs + 1);
    expect(r).toEqual({ apply: true, stickyAt: 0 });
  });

  it("clears normally when nothing is sticky", () => {
    expect(nextStatusState("", false, 0, T0)).toEqual({
      apply: true,
      stickyAt: 0,
    });
  });

  it("a second sticky message replaces the first and restarts the window", () => {
    const r = nextStatusState("Отменено: upsert room", true, T0, T0 + 5000);
    expect(r).toEqual({ apply: true, stickyAt: T0 + 5000 });
  });
});

// --- the top-bar record button ---------------------------------------------
//
// The button's whole reason to exist is to be reachable without hunting for a
// cell, so what has to hold is: it aims at the cell the PLAYER is in (never the
// one on screen), it refuses exactly what map-record-room refuses (🔴, hint-less
// live block, pending queue) and says why, and it does NOT refuse 🟡 — which is
// the state you record virgin territory from.
describe("recordButtonState", () => {
  it("targets the player's own cell, whatever is on screen", () => {
    const st = recordButtonState(pos({ zone: "Z", x: 4, y: 7, l: 2 }), live, false);
    expect(st.cell).toEqual({ z: "Z", x: 4, y: 7, l: 2 });
    expect(st.enabled).toBe(true);
  });

  it("stays enabled on 🟡 — that is the working state for virgin ground", () => {
    const st = recordButtonState(pos({ confidence: "yellow" }), live, false);
    expect(st.enabled).toBe(true);
  });

  it("is disabled and explains itself when the position is lost (🔴)", () => {
    const st = recordButtonState(
      pos({ confidence: "red", eventDescribesCell: false }),
      live,
      true,
    );
    expect(st.enabled).toBe(false);
    expect(st.title).toContain("🔴");
  });

  it("is disabled and explains itself with no live room block yet", () => {
    const st = recordButtonState(pos(), { hint: "", desc: "", exits: "" }, false);
    expect(st.enabled).toBe(false);
    expect(st.title).toContain("живая комната");
  });

  it("is disabled and explains itself mid-speedwalk", () => {
    const st = recordButtonState(pos({ pendingMoves: 3 }), live, false);
    expect(st.enabled).toBe(false);
    expect(st.title).toContain("перемещение");
  });

  it("is disabled with no position at all, and offers no cell to write to", () => {
    const st = recordButtonState(null, live, null);
    expect(st.enabled).toBe(false);
    expect(st.cell).toBeNull();
    expect(st.title).toContain("Я здесь");
    expect(recordButtonState(pos({ valid: false }), live, null).cell).toBeNull();
  });

  it("keeps one caption and puts create-vs-update in the tooltip", () => {
    expect(recordButtonState(pos(), live, false).label).toBe(recordButtonLabel);
    expect(recordButtonState(pos(), live, true).label).toBe(recordButtonLabel);
    expect(recordButtonState(pos(), live, false).title).toContain("создаст");
    expect(recordButtonState(pos(), live, true).title).toContain("обновит");
    // Unknown occupancy (the player's zone is not loaded) must not fabricate one.
    expect(recordButtonState(pos(), live, null).title).toContain("запишет");
  });

  // The button and the menu item are two doors to ONE write, so a state either
  // opens both or neither. Anything else is a second record path by accident.
  it("agrees with the cell-menu item on the player's own cell", () => {
    const states: PlayerPosition[] = [
      pos(),
      pos({ confidence: "green" }),
      pos({ confidence: "red", eventDescribesCell: false }),
      pos({ eventDescribesCell: false }),
      pos({ pendingMoves: 1 }),
      pos({ valid: false }),
    ];
    const rooms: (LiveRoom | null)[] = [live, null, { hint: "", desc: "", exits: "N" }];
    for (const p of states) {
      for (const lr of rooms) {
        const here = { z: p.zone, x: p.x, y: p.y, l: p.l };
        expect(recordButtonState(p, lr, false).enabled).toBe(
          recordAction(p, lr, here, false).enabled,
        );
      }
    }
  });
});

// The menu must never offer a clickable write it cannot preview. recordAction
// and recordPreview are separately responsible, so pin that they agree.
describe("record action and preview agree", () => {
  const here = { z: "Z", x: 1, y: 1, l: 0 };

  it("no live room => no preview AND not enabled", () => {
    expect(recordPreview(null, null)).toBeNull();
    expect(recordAction(pos(), null, here, false).enabled).toBe(false);
  });

  it("blank hint => no preview AND not enabled", () => {
    const blank = { hint: "", desc: "d", exits: "N" };
    expect(recordPreview(blank, null)).toBeNull();
    expect(recordAction(pos(), blank, here, false).enabled).toBe(false);
  });

  it("a live room with a hint => preview AND enabled", () => {
    expect(recordPreview(live, null)).not.toBeNull();
    expect(recordAction(pos(), live, here, false).enabled).toBe(true);
  });
});
